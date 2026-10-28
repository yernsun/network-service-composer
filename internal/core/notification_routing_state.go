package core

import (
	"context"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

const notificationRoutingKey = "notification:routing"
const notificationRoutingKind = "notification-routing"

type notificationRoutingState struct {
	Since       time.Time `json:"since"`
	LegacySince time.Time `json:"legacy_since"`
	Generation  int64     `json:"generation"`
	Initialized bool      `json:"initialized"`
}

func (c *Core) notificationRoutingState(ctx context.Context) (notificationRoutingState, storage.ControlDocument, error) {
	v, d, err := readControl[notificationRoutingState](ctx, c.store, notificationRoutingKey, notificationRoutingKind)
	if errors.Is(err, storage.ErrNotFound) {
		return v, storage.ControlDocument{}, nil
	}
	return v, d, err
}

func (c *Core) notificationRoutingWrites(ctx context.Context, writes []storage.ControlWrite) ([]storage.ControlWrite, error) {
	v, d, err := c.notificationRoutingState(ctx)
	if err != nil {
		return nil, err
	}
	if d.Revision == 0 {
		legacy, _, err := c.notificationSettings(ctx)
		if err != nil {
			return nil, err
		}
		v.Since, v.LegacySince = legacy.Since, legacy.Since
		if v.Since.IsZero() {
			v.Since = c.now()
		}
	}
	v.Generation++
	w, err := c.controlWrite(notificationRoutingKey, notificationRoutingKind, "", v, d.Revision)
	if err != nil {
		return nil, err
	}
	return append(writes, w), nil
}

func (c *Core) notificationRoutingEnabled(ctx context.Context) (bool, error) {
	_, d, err := c.notificationRoutingState(ctx)
	return d.Revision != 0, err
}

func (c *Core) readyNotificationRouting(ctx context.Context) (notificationRoutingState, error) {
	state, _, err := c.notificationRoutingState(ctx)
	if err != nil || state.Initialized {
		return state, err
	}
	if err = c.initializeNotificationRouting(ctx); err != nil && !errors.Is(err, storage.ErrConflict) && !errors.Is(err, storage.ErrPrecondition) {
		return state, err
	}
	state, _, err = c.notificationRoutingState(ctx)
	return state, err
}
