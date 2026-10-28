package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type leaderCheckKey struct{}

// SetLeaderStore binds an embedded or standalone gateway to the shared control plane.
// Gateway credentials stay separate from the Controller's internal identity.
func (a *Agent) SetLeaderStore(store controlplane.StateStore) { a.leaderStore = store }

func (a *Agent) connectLeader(ctx context.Context, transport *tls.Config) error {
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	leader, err := a.leaderStore.Current(check)
	if err == nil {
		err = a.leaderStore.Validate(check, leader.ID, leader.Term)
	}
	cancel()
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(leader.GRPCAddress)
	if err != nil || host == "" {
		return errors.New("elected Controller has no gateway address")
	}
	transport = transport.Clone()
	transport.ServerName = host
	transport.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return errors.New("unverified gateway control connection")
		}
		id, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil || id != leader.ID {
			return errors.New("gateway control peer is not the elected Controller")
		}
		return nil
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	validate := func() error {
		check, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return a.leaderStore.Validate(check, leader.ID, leader.Term)
	}
	ctx = context.WithValue(ctx, leaderCheckKey{}, validate)
	finished := make(chan struct{})
	defer func() { stop(); <-finished }()
	go func() {
		defer close(finished)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if validate() != nil {
					stop()
					return
				}
			}
		}
	}()
	connection, err := grpc.NewClient(leader.GRPCAddress, grpc.WithTransportCredentials(credentials.NewTLS(transport)), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(gateway.MaxCompressedSize+gateway.DefaultChunkSize), grpc.MaxCallSendMsgSize(gateway.MaxCompressedSize+gateway.DefaultChunkSize)))
	if err != nil {
		return err
	}
	defer connection.Close()
	return a.runStream(ctx, gatewayv2.NewGatewayControlClient(connection))
}

func checkLeader(ctx context.Context) error {
	if check, ok := ctx.Value(leaderCheckKey{}).(func() error); ok {
		return check()
	}
	return ctx.Err()
}
