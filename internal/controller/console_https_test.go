package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/pki"
)

func TestIndependentConsoleHTTPSDoesNotDependOnGateway(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.ConsoleHTTPSListen = "127.0.0.1:0"
	cert, key, err := pki.SelfSigned(nil, []net.IP{net.ParseIP("127.0.0.1")}, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.ConsoleHTTPSCertFile = filepath.Join(t.TempDir(), "console.crt")
	cfg.Server.ConsoleHTTPSKeyFile = cfg.Server.ConsoleHTTPSCertFile + ".key"
	os.WriteFile(cfg.Server.ConsoleHTTPSCertFile, cert, 0o600)
	os.WriteFile(cfg.Server.ConsoleHTTPSKeyFile, key, 0o600)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("unencrypted recovery listener")
		}
		w.Write([]byte("authenticated handler boundary"))
	})
	server, listener, err := consoleHTTPS(cfg, handler)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("https://" + listener.Addr().String() + "/login")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(data) != "authenticated handler boundary" {
		t.Fatal("console handler mismatch")
	}
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	cfg.Server.ConsoleHTTPSListen = other.Addr().String()
	if _, _, err = consoleHTTPS(cfg, handler); err == nil {
		t.Fatal("occupied HTTPS listener accepted")
	}
	cfg.Server.ConsoleHTTPSCertFile = "missing"
	if _, _, err = consoleHTTPS(cfg, handler); err == nil {
		t.Fatal("invalid identity accepted")
	}
	cfg.Server.ConsoleHTTPSListen = ""
	s, l, err := consoleHTTPS(cfg, handler)
	if err != nil || s != nil || l != nil {
		t.Fatal("disabled listener started")
	}
}

func TestControllerRunStartsAndClosesIndependentConsoleListener(t *testing.T) {
	for _, scenario := range []string{"valid", "invalid-key", "occupied"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := controllerConfigFixture(t)
			// Independent listeners cannot use the same textual bind, even when both
			// would otherwise request an ephemeral port.
			cfg.Server.ConsoleHTTPSListen = "localhost:0"
			cert, key, err := pki.SelfSigned(nil, []net.IP{net.ParseIP("127.0.0.1")}, 20, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			cfg.Server.ConsoleHTTPSCertFile = filepath.Join(t.TempDir(), "console.crt")
			cfg.Server.ConsoleHTTPSKeyFile = cfg.Server.ConsoleHTTPSCertFile + ".key"
			os.WriteFile(cfg.Server.ConsoleHTTPSCertFile, cert, 0600)
			os.WriteFile(cfg.Server.ConsoleHTTPSKeyFile, key, 0600)
			if scenario == "invalid-key" {
				os.WriteFile(cfg.Server.ConsoleHTTPSKeyFile, []byte("invalid"), 0600)
			}
			if scenario == "occupied" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				cfg.Server.ConsoleHTTPSListen = listener.Addr().String()
			}
			controller, err := New(t.Context(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			err = controller.Run(ctx)
			lock, lockErr := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
			if lockErr != nil {
				t.Fatal("listener failure retained database process ownership", lockErr)
			}
			lock.Close()
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid independent listener reported successful startup")
			}
		})
	}
}
