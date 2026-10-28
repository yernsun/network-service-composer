package core

import (
	"context"
	"errors"

	"github.com/yernsun/network-service-composer/internal/storage"
)

// A batch prepares all outbox mutations before any document is committed.
// Encoding failure, dedupe collision or a stale receipt aborts the whole unit.
type notificationBatch struct {
	writes []storage.ControlWrite
	err    error
}

func (b *notificationBatch) add(c *Core, key, kind, parent string, value any, expected int64) {
	b.prepared(c.controlWrite(key, kind, parent, value, expected))
}
func (b *notificationBatch) prepared(write storage.ControlWrite, err error) {
	if err != nil {
		b.err = errors.Join(b.err, err)
		return
	}
	b.writes = append(b.writes, write)
}
func (b *notificationBatch) commit(ctx context.Context, c *Core) error {
	if b.err != nil {
		return b.err
	}
	return c.store.WriteControlDocuments(ctx, b.writes)
}

// All receiver, route and silence edits publish their configuration and wake
// generation together. A shared transaction prevents an edited scope from
// becoming visible before the dispatcher knows it must recheck pending work.
func (c *Core) commitNotificationConfiguration(ctx context.Context, batch notificationBatch) error {
	if batch.err != nil {
		return batch.err
	}
	writes, err := c.notificationRoutingWrites(ctx, batch.writes)
	if err != nil {
		return err
	}
	return c.store.WriteControlDocuments(ctx, writes)
}
