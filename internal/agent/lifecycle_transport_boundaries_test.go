package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func agentTransportFixture(t *testing.T, a *Agent) tls.Certificate {
	t.Helper()
	root := t.TempDir()
	a.config.Agent.TLSCertFile = filepath.Join(root, "cert.pem")
	a.config.Agent.TLSKeyFile = filepath.Join(root, "key.pem")
	a.config.Agent.CACertFile = filepath.Join(root, "ca.pem")
	caKey := filepath.Join(root, "ca.key")
	if err := pki.InitCA(a.config.Agent.CACertFile, caKey, "fixture CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	uri, err := controlplane.IdentityURI("controller")
	if err != nil {
		t.Fatal(err)
	}
	if err := pki.Issue(a.config.Agent.CACertFile, caKey, a.config.Agent.TLSCertFile, a.config.Agent.TLSKeyFile, pki.CertificateOptions{CommonName: "controller", DNSNames: []string{"controller.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	a.SetLeaderStore(agentLeaderFixture{agent: a})
	a.config.Agent.ServerName = "controller.example.test"
	pair, e := tls.LoadX509KeyPair(a.config.Agent.TLSCertFile, a.config.Agent.TLSKeyFile)
	if e != nil {
		t.Fatal(e)
	}
	return pair
}

type agentLeaderFixture struct {
	controlplane.StateStore
	agent *Agent
}

func (f agentLeaderFixture) Current(context.Context) (controlplane.State, error) {
	return controlplane.State{Identity: controlplane.Identity{ID: "controller", GRPCAddress: f.agent.config.Agent.ControllerAddress}, Term: 1}, nil
}
func (f agentLeaderFixture) Validate(ctx context.Context, id string, term int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id != "controller" || term != 1 {
		return controlplane.ErrNotLeader
	}
	return nil
}
func TestAgentTransportCredentialsRejectMissingOrInvalidTrust(t *testing.T) {
	for _, mode := range []string{"valid", "missing key", "missing CA", "invalid CA"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := agentStreamFixture(t)
			agentTransportFixture(t, a)
			switch mode {
			case "missing key":
				a.config.Agent.TLSKeyFile += ".missing"
			case "missing CA":
				a.config.Agent.CACertFile += ".missing"
			case "invalid CA":
				if e := os.WriteFile(a.config.Agent.CACertFile, []byte("not a certificate"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			cfg, e := a.transportCredentials()
			if mode != "valid" {
				if e == nil || cfg != nil {
					t.Fatal("invalid trust silently downgraded to insecure transport")
				}
				return
			}
			if e != nil || cfg.MinVersion != tls.VersionTLS13 || cfg.ServerName != a.config.Agent.ServerName || cfg.InsecureSkipVerify || len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
				t.Fatal("transport lost peer identity or TLS policy", cfg, e)
			}
		})
	}
}

type reconnectBoundaryServer struct {
	gatewayv2.UnimplementedGatewayControlServer
	calls  atomic.Int64
	cancel context.CancelFunc
}

func (s *reconnectBoundaryServer) Connect(stream grpc.BidiStreamingServer[gatewayv2.AgentMessage, gatewayv2.ControllerMessage]) error {
	_, e := stream.Recv()
	if e != nil {
		return e
	}
	if s.calls.Add(1) >= 2 {
		s.cancel()
	}
	return status.Error(codes.Unavailable, "isolated controller restart")
}
func TestAgentConnectionLoopRetriesIsolatedTLSControllerAndCancels(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	for _, mode := range []string{"reconnect", "short heartbeat", "canceled before connect", "invalid target", "missing credentials"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := agentStreamFixture(t)
			pair := agentTransportFixture(t, a)
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})))
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			impl := &reconnectBoundaryServer{cancel: cancel}
			gatewayv2.RegisterGatewayControlServer(server, impl)
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			a.config.Agent.ControllerAddress = listener.Addr().String()
			switch mode {
			case "short heartbeat":
				a.config.Agent.HeartbeatInterval = config.Duration(time.Nanosecond)
			case "canceled before connect":
				cancel()
			case "invalid target":
				a.config.Agent.ControllerAddress = "dns:///%"
				short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
				ctx = short
			case "missing credentials":
				a.config.Agent.TLSKeyFile += ".missing"
			}
			e = a.connectionLoop(ctx)
			if mode == "missing credentials" {
				if e == nil || !strings.Contains(e.Error(), "load agent TLS") {
					t.Fatal("missing credentials were retried", e)
				}
			} else if !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("connection cancellation lost", e)
			}
			if mode == "reconnect" || mode == "short heartbeat" {
				if impl.calls.Load() != 2 || !errors.Is(e, context.Canceled) {
					t.Fatal("controller reconnection did not stop after cancellation", impl.calls.Load(), e)
				}
			} else if impl.calls.Load() != 0 {
				t.Fatal("invalid or canceled connection reached controller")
			}
			if a.applied.Load() != 0 || a.generations.State().ConfigRevision != 0 {
				t.Fatal("disconnect falsely advanced active configuration")
			}
		})
	}
}
func TestAgentRunReportsProcessAndCredentialFailuresAndStops(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	for _, mode := range []string{"clean unexpected exit", "failed process", "start failure", "credential failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := agentStreamFixture(t)
			agentTransportFixture(t, a)
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			a.config.Agent.ControllerAddress = listener.Addr().String()
			listener.Close()
			command := "exec sleep 30"
			switch mode {
			case "clean unexpected exit":
				command = "exit 0"
			case "failed process":
				command = "exit 9"
			case "credential failure":
				a.config.Agent.TLSKeyFile += ".missing"
			}
			if e = os.WriteFile(a.generations.config.Binary, []byte("#!/bin/sh\n"+command+"\n"), 0700); e != nil {
				t.Fatal(e)
			}
			if mode == "start failure" {
				a.generations.config.Binary += ".missing"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "canceled" {
				short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
				defer stop()
				ctx = short
			}
			e = a.Run(ctx)
			switch mode {
			case "canceled":
				if e != nil {
					t.Fatal("requested shutdown failed", e)
				}
			case "clean unexpected exit":
				if e == nil || !strings.Contains(e.Error(), "NGINX exited unexpectedly") {
					t.Fatal("unexpected clean exit hidden", e)
				}
			case "failed process":
				if e == nil || !strings.Contains(e.Error(), "NGINX exited:") {
					t.Fatal("process failure hidden", e)
				}
			case "start failure":
				if e == nil || !strings.Contains(e.Error(), "start NGINX") {
					t.Fatal("start failure hidden", e)
				}
			case "credential failure":
				if e == nil || !strings.Contains(e.Error(), "load agent TLS") {
					t.Fatal("connection failure hidden", e)
				}
			}
			if p := a.generations.process; p != nil && p.Process != nil && !errors.Is(p.Process.Signal(syscall.Signal(0)), os.ErrProcessDone) {
				t.Fatal("Agent returned before its process exited")
			}
			if a.generations.State().ConfigRevision != 0 {
				t.Fatal("Agent lifecycle advanced configuration without a publication")
			}
		})
	}
}
