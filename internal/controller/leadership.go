package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func leadershipConfig(cfg *config.Config) (*tls.Config, error) {
	// SQLite has exactly one process owner and needs no peer transport.
	if cfg.Storage.Driver == string(storage.SQLite) && cfg.Controller.InstanceID == "" {
		cfg.Controller.InstanceID = "local"
	}
	if _, err := controlplane.IdentityURI(cfg.Controller.InstanceID); err != nil {
		return nil, err
	}
	in := cfg.Controller.Internal
	if cfg.Storage.Driver == string(storage.SQLite) && in == (config.ControllerInternalConfig{}) {
		return nil, nil
	}
	if in.Listen == "" {
		return nil, errors.New("controller.internal.listen is required for shared database instances")
	}
	if err := controlplane.ValidateInternalURL(in.URL); err != nil {
		return nil, err
	}
	return controlplane.InternalTLS(in.CAFile, in.CertFile, in.KeyFile, cfg.Controller.InstanceID)
}

func (c *Controller) runLeader(ctx context.Context, term *controlplane.Term) error {
	if err := term.Check(ctx); err != nil {
		return err
	}
	prepared, err := c.store.RecoveryPrepared(ctx)
	if err != nil {
		return err
	}
	if prepared {
		return errors.New("restored database is held for recovery review; leader workers cannot start")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, len(c.background))
	var workers sync.WaitGroup
	for _, worker := range c.background {
		workers.Add(1)
		go func(worker Background) {
			defer workers.Done()
			err := worker.Run(ctx)
			if ctx.Err() == nil && err == nil {
				err = fmt.Errorf("leader worker %T stopped unexpectedly", worker)
			}
			done <- err
		}(worker)
	}
	c.schedulerTerm.Store(term.State.Term)
	c.metrics.SchedulerActive.Set(1)
	defer c.metrics.SchedulerActive.Set(0)
	defer c.schedulerTerm.CompareAndSwap(term.State.Term, 0)
	err = nil
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-done:
	}
	cancel()
	workers.Wait()
	return err
}

func (c *Controller) isReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.haController(ctx).State == "READY"
}

type termStream struct {
	grpc.ServerStream
	ctx  context.Context
	term *controlplane.Term
}

func (s termStream) Context() context.Context { return s.ctx }

func (s termStream) SendMsg(message any) error {
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	if err := s.term.Check(ctx); err != nil {
		return status.Error(codes.Unavailable, "controller leadership unavailable")
	}
	return s.ServerStream.SendMsg(message)
}

func (c *Controller) leaderStream(service any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
	term := c.election.Active()
	check, cancel := context.WithTimeout(stream.Context(), 3*time.Second)
	err := term.Check(check)
	cancel()
	if err != nil {
		return status.Error(codes.Unavailable, "controller leadership unavailable")
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	stop := context.AfterFunc(term.Context, cancel)
	defer stop()
	return next(service, termStream{ServerStream: stream, ctx: ctx, term: term})
}
