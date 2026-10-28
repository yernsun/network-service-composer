package storage

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yernsun/network-service-composer/internal/model"
)

const poolChangeChannel = "nsc_pool_changes"

func notifyPoolChangeTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, revision int64) error {
	if store.dialect != Postgres {
		return nil
	}
	payload := poolID + ":" + strconv.FormatInt(revision, 10)
	_, err := tx.ExecContext(ctx, store.bind(`SELECT pg_notify('nsc_pool_changes', :payload)`), payload)
	return err
}

// ListenPoolChanges is an optional PostgreSQL fast path. All callers retain
// revision polling, so transient listener failures never compromise delivery.
func (s *SQLStore) ListenPoolChanges(ctx context.Context, publish func(model.StateChange)) error {
	if s.dialect != Postgres {
		<-ctx.Done()
		return ctx.Err()
	}
	backoff := time.Second
	for ctx.Err() == nil {
		connection, err := pgx.Connect(ctx, s.dsn)
		if err == nil {
			_, err = connection.Exec(ctx, "LISTEN "+poolChangeChannel)
		}
		if err != nil {
			if connection != nil {
				_ = connection.Close(context.Background())
			}
			if err = waitNotificationRetry(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for ctx.Err() == nil {
			notification, waitErr := connection.WaitForNotification(ctx)
			if waitErr != nil {
				break
			}
			poolID, revision, ok := parsePoolChange(notification.Payload)
			if ok && publish != nil {
				publish(model.StateChange{PoolID: poolID, Revision: revision, Kind: "postgres-notify"})
			}
		}
		_ = connection.Close(context.Background())
		if ctx.Err() == nil {
			if err = waitNotificationRetry(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, 30*time.Second)
		}
	}
	return ctx.Err()
}

func parsePoolChange(payload string) (string, int64, bool) {
	separator := strings.LastIndexByte(payload, ':')
	if separator <= 0 || separator == len(payload)-1 {
		return "", 0, false
	}
	revision, err := strconv.ParseInt(payload[separator+1:], 10, 64)
	if err != nil || revision < 0 {
		return "", 0, false
	}
	return payload[:separator], revision, true
}

func waitNotificationRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
