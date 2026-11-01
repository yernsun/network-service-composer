package install

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// This fixture checks authenticated PostgreSQL transport and read-only startup
// parameters, not SQL/schema conformance. Real PostgreSQL is used in acceptance.
func postgresWire(t *testing.T, password string) (string, func() []map[string]string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	connections := []net.Conn{}
	startups := []map[string]string{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				backend := pgproto3.NewBackend(conn, conn)
				startup, err := backend.ReceiveStartupMessage()
				if err != nil {
					return
				}
				message, ok := startup.(*pgproto3.StartupMessage)
				if !ok {
					return
				}
				mu.Lock()
				startups = append(startups, message.Parameters)
				mu.Unlock()
				backend.Send(&pgproto3.AuthenticationCleartextPassword{})
				if backend.Flush() != nil {
					return
				}
				response, err := backend.Receive()
				if err != nil {
					return
				}
				credential, ok := response.(*pgproto3.PasswordMessage)
				if !ok || credential.Password != password {
					backend.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "28P01", Message: "fixture authentication rejected"})
					backend.Flush()
					return
				}
				backend.Send(&pgproto3.AuthenticationOk{})
				backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "18.6"})
				backend.Send(&pgproto3.BackendKeyData{ProcessID: 123, SecretKey: []byte{0, 0, 1, 200}})
				backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if backend.Flush() != nil {
					return
				}
				for {
					request, err := backend.Receive()
					if err != nil {
						return
					}
					switch request.(type) {
					case *pgproto3.Terminate:
						return
					case *pgproto3.Query:
						backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					case *pgproto3.Sync:
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					default:
						backend.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "0A000", Message: "fixture supports ping only"})
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					}
					if backend.Flush() != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String(), func() []map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]string(nil), startups...)
	}
}

func TestSSHDatabaseAuthenticatesEveryConnectionAndPinsReadOnly(t *testing.T) {
	address, startups := postgresWire(t, "isolated-wire-password")
	host := &fakeHost{stream: func(ctx context.Context, command string) (net.Conn, error) {
		if command != bootstrap.LocalDockerCLI+" exec -i 'pinned-container' /bin/busybox nc 127.0.0.1 5432" {
			t.Error("unexpected database target")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}}
	cfg := config.Defaults()
	cfg.Storage.DSN = "postgres://nsc:isolated-wire-password@unresolvable.example.invalid/nsc?sslmode=disable"
	for _, readOnly := range []bool{true, false} {
		store, err := (SSHDatabase{}).Open(t.Context(), cfg, host, "pinned-container", readOnly)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Ping(t.Context()); err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	observed := startups()
	if len(observed) < 2 || observed[0]["default_transaction_read_only"] != "on" || observed[1]["default_transaction_read_only"] == "on" {
		t.Fatal("database connection mode lost")
	}
	cfg.Storage.DSN = "postgres://nsc:wrong@unresolvable.example.invalid/nsc?sslmode=disable"
	if _, err := (SSHDatabase{}).Open(t.Context(), cfg, host, "pinned-container", true); err == nil {
		t.Fatal("invalid existing credentials accepted")
	}
	cfg.Storage.DSN = "postgres://%invalid"
	if _, err := (SSHDatabase{}).Open(t.Context(), cfg, host, "pinned-container", true); err == nil {
		t.Fatal("invalid DSN accepted")
	}
	cfg.Storage.DSN = "postgres://nsc:fixture@127.0.0.1/nsc?sslmode=disable"
	host.stream = func(context.Context, string) (net.Conn, error) { return nil, io.EOF }
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := (SSHDatabase{}).Open(ctx, cfg, host, "pinned-container", true); err == nil {
		t.Fatal("unavailable SSH relay accepted")
	}
}

func TestDatabaseTargetFailureNeverCreatesAnotherVolume(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	before := len(i.Hosts["one"].(*fakeHost).volumes)
	delete(c.services, i.Record.Resources["service:postgresql"].ID)
	if _, err := i.databaseContainer(t.Context()); err == nil {
		t.Fatal("missing database task accepted")
	}
	if len(i.Hosts["one"].(*fakeHost).volumes) != before {
		t.Fatal("empty replacement volume created")
	}
	i.Database = sqliteConnector{fail: errors.New("unavailable database")}
	if err := i.withStore(t.Context(), true, func(_ storage.Store, _ config.Config) error { return nil }); err == nil {
		t.Fatal("unavailable database accepted")
	}
}
