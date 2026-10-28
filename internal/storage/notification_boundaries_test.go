package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestPoolNotificationPayloadAndCancellationBoundaries(t *testing.T) {
	for _, item := range []struct {
		payload, pool string
		revision      int64
		valid         bool
	}{
		{"", "", 0, false}, {"pool", "", 0, false}, {":7", "", 0, false}, {"pool:", "", 0, false}, {"pool:-1", "", 0, false}, {"pool:no", "", 0, false}, {"pool:9223372036854775808", "", 0, false}, {"pool:0", "pool", 0, true}, {"pool:9223372036854775807", "pool", 9223372036854775807, true}, {"team:pool:9", "team:pool", 9, true},
	} {
		t.Run(item.payload, func(t *testing.T) {
			p, r, ok := parsePoolChange(item.payload)
			if p != item.pool || r != item.revision || ok != item.valid {
				t.Fatal("incorrect notification identity", p, r, ok)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, dialect := range []Dialect{SQLite, MySQL, Postgres} {
		s := &SQLStore{dialect: dialect}
		if e := s.ListenPoolChanges(ctx, func(model.StateChange) { t.Error("canceled listener published") }); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	if e := waitNotificationRetry(ctx, time.Hour); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := waitNotificationRetry(context.Background(), time.Millisecond); e != nil {
		t.Fatal(e)
	}
}

// An ephemeral PostgreSQL protocol peer exercises the real pgx notification
// client without a database, credentials, or access to a business runtime.
func TestPoolNotificationListenerReconnectsWithoutInventingEvents(t *testing.T) {
	for _, stage := range []string{"events", "nil callback", "listen rejected", "connection dropped", "invalid DSN"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := &SQLStore{dialect: Postgres}
			var accepted atomic.Int32
			var events []model.StateChange
			if stage == "invalid DSN" {
				s.dsn = "postgres://%invalid"
				short, stop := context.WithTimeout(ctx, 1100*time.Millisecond)
				defer stop()
				if e := s.ListenPoolChanges(short, func(model.StateChange) { t.Error("invalid connection published") }); !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
				return
			}
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			s.dsn = fmt.Sprintf("postgres://fixture@%s/fixture?sslmode=disable&connect_timeout=1", listener.Addr())
			errorsOut := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, e := listener.Accept()
					if e != nil {
						return
					}

					_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
					backend := pgproto3.NewBackend(conn, conn)
					startup, e := backend.ReceiveStartupMessage()
					if e != nil {
						conn.Close()
						errorsOut <- e
						return
					}
					if _, ok := startup.(*pgproto3.CancelRequest); ok {
						conn.Close()
						continue
					}
					attempt := accepted.Add(1)
					backend.Send(&pgproto3.AuthenticationOk{})
					backend.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
					backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0"})
					backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					if e = backend.Flush(); e != nil {
						conn.Close()
						errorsOut <- e
						return
					}
					message, e := backend.Receive()
					if e != nil {
						conn.Close()
						errorsOut <- e
						return
					}
					query, ok := message.(*pgproto3.Query)
					if !ok || query.String != "LISTEN nsc_pool_changes" {
						conn.Close()
						errorsOut <- fmt.Errorf("unexpected listen request: %#v", message)
						return
					}
					if stage == "listen rejected" && attempt == 1 {
						backend.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42501", Message: "fixture rejects LISTEN"})
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
						_ = backend.Flush()
						conn.Close()
						continue
					}
					backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("LISTEN")})
					backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					if e = backend.Flush(); e != nil {
						conn.Close()
						errorsOut <- e
						return
					}
					if stage == "connection dropped" && attempt == 1 {
						conn.Close()
						continue
					}
					for _, payload := range []string{"bad", "pool:-3", "pool:7", "pool:8"} {
						backend.Send(&pgproto3.NotificationResponse{PID: 1, Channel: poolChangeChannel, Payload: payload})
					}
					if e = backend.Flush(); e != nil {
						conn.Close()
						errorsOut <- e
						return
					}
					if stage == "nil callback" {
						time.AfterFunc(30*time.Millisecond, cancel)
					}
					_, _ = backend.Receive()
					conn.Close()
					return
				}
			}()
			callback := func(event model.StateChange) {
				events = append(events, event)
				if len(events) == 2 {
					cancel()
				}
			}
			if stage == "nil callback" {
				callback = nil
			}
			e = s.ListenPoolChanges(ctx, callback)
			listener.Close()
			<-done
			select {
			case issue := <-errorsOut:
				t.Fatal(issue)
			default:
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatal("listener did not stop after cancellation", e)
			}
			if stage == "nil callback" {
				if len(events) != 0 {
					t.Fatal(events)
				}
			} else if len(events) != 2 || events[0].PoolID != "pool" || events[0].Revision != 7 || events[1].Revision != 8 || events[0].Kind != "postgres-notify" {
				t.Fatal("invalid payload leaked or valid evidence lost", events)
			}
			expected := int32(1)
			if strings.Contains(stage, "rejected") || strings.Contains(stage, "dropped") {
				expected = 2
			}
			if accepted.Load() != expected {
				t.Fatal("listener did not retry once", accepted.Load())
			}
		})
	}
}
