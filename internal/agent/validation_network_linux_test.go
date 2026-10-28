package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

// Standalone package tests re-exec inside a network namespace. The coverage
// gate enters the namespace before running Go, keeping native statement and
// decision measurements in the same process. Missing isolation is a failure.
func insideAgentNetworkFixture(t *testing.T) bool {
	t.Helper()
	if os.Getenv("NSC_TEST_ISOLATED_NETWORK") == "1" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "unshare", "-Urn", "--", "sh", "-c", `ip link set lo up && NSC_TEST_ISOLATED_NETWORK=1 exec "$@"`, "sh", os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated Agent network fixture failed: %v\n%s", err, output)
	}
	t.Logf("isolated fixture:\n%s", output)
	return false
}

func TestSandboxValidationChecksAndRecoveryInIsolatedNetwork(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	m := validationManagerFixture(t, "exit 0")
	previous, err := os.Readlink(filepath.Join(m.root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	var healthFailure atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_nsc/generation-health" {
			current, _ := os.Readlink(filepath.Join(m.root, "current"))
			revision := "0"
			if strings.HasPrefix(current, ".validation-") {
				revision = "1"
			}
			if healthFailure.Load() {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("X-NSC-Config-Revision", revision)
			return
		}
		w.Header().Set("X-Fixture", "verified")
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "https://unreachable.example.test/")
			w.WriteHeader(302)
			return
		}
		if r.Header.Get("X-Request-Fixture") == "echo" {
			w.Header().Set("X-Request-Observed", "echo")
		}
		w.WriteHeader(204)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	plain := httptest.NewUnstartedServer(handler)
	plain.Listener = listener
	plain.Start()
	defer plain.Close()
	certPEM, keyPEM := testCertificate(t, "app.example.com")
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
	tlsListener, err := net.Listen("tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	secure := httptest.NewUnstartedServer(handler)
	secure.Listener = tlsListener
	secure.TLS = tlsConfig.Clone()
	secure.StartTLS()
	defer secure.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	h3 := &http3.Server{Handler: handler, TLSConfig: tlsConfig.Clone()}
	done := make(chan error, 1)
	go func() { done <- h3.Serve(udp) }()
	defer func() { _ = h3.Close(); _ = udp.Close(); <-done }()
	baseCheck := validationCheck{Host: "app.example.com", Path: "/ready", Method: "GET", Scheme: "http", ExpectedStatus: 204, Headers: map[string]string{"X-Fixture": "verified"}}
	for _, tc := range []struct {
		name   string
		mutate func(*validationCheck)
		valid  bool
	}{
		{"HTTP GET", func(*validationCheck) {}, true},
		{"HEAD", func(c *validationCheck) { c.Method = "HEAD" }, true},
		{"HTTPS", func(c *validationCheck) { c.Scheme = "https" }, true},
		{"HTTP3", func(c *validationCheck) { c.Scheme = "http3" }, true},
		{"request headers", func(c *validationCheck) {
			c.RequestHeaders = map[string]string{"X-Request-Fixture": "echo"}
			c.Headers = map[string]string{"X-Request-Observed": "echo"}
		}, true},
		{"redirect not followed", func(c *validationCheck) { c.Path = "/redirect"; c.ExpectedStatus = 302 }, true},
		{"wrong response status", func(c *validationCheck) { c.ExpectedStatus = 200 }, false},
		{"wrong response header", func(c *validationCheck) { c.Headers = map[string]string{"X-Fixture": "wrong"} }, false},
		{"unsafe method", func(c *validationCheck) { c.Method = "POST" }, false},
		{"unsafe scheme", func(c *validationCheck) { c.Scheme = "file" }, false},
		{"relative path", func(c *validationCheck) { c.Path = "ready" }, false},
		{"network path", func(c *validationCheck) { c.Path = "//outside" }, false},
		{"malformed URL", func(c *validationCheck) { c.Host = "bad\nheader" }, false},
		{"TLS identity mismatch", func(c *validationCheck) { c.Scheme = "https"; c.Host = "other.example.com" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := baseCheck
			tc.mutate(&check)
			checks, err := json.Marshal([]validationCheck{check})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := validationSnapshot()
			snapshot.Certificates = []*gatewayv2.SnapshotCertificate{{Id: "cert_test", CertificatePem: certPEM, PrivateKeyPem: keyPEM}}
			result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{TaskId: "task", Snapshot: snapshot, ActivateSandbox: true, ChecksJson: string(checks)}, true)
			if result.Success != tc.valid {
				t.Fatalf("check result=%#v, expected valid=%t", result, tc.valid)
			}
			var observed []checkResult
			if err := json.Unmarshal([]byte(result.ChecksJson), &observed); err != nil || len(observed) != 1 || observed[0].Success != tc.valid {
				t.Fatalf("per-check evidence=%#v, %v", observed, err)
			}
			if check.Scheme == "http3" && tc.valid && observed[0].Protocol != "HTTP/3.0" {
				t.Fatalf("HTTP3 check fell back to %s", observed[0].Protocol)
			}
			assertValidationCleaned(t, m, previous)
		})
	}
	for _, checks := range []string{"", "[]", "{"} {
		result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{Snapshot: validationSnapshot(), ActivateSandbox: true, ChecksJson: checks}, true)
		if result.Success != (checks != "{") {
			t.Fatalf("empty/invalid checks result=%#v", result)
		}
		assertValidationCleaned(t, m, previous)
	}
	for _, seconds := range []int64{2, 600} {
		t.Run(fmt.Sprintf("hold=%d", seconds), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := m.Validate(ctx, &gatewayv2.ValidationRequest{TaskId: "held", Snapshot: validationSnapshot(), ActivateSandbox: true, HoldUntilUnix: time.Now().Unix() + seconds}, true)
			if !result.Success || m.held == nil {
				t.Fatalf("sandbox not held: %#v", result)
			}
			if seconds == 600 {
				if err := m.CancelValidation(context.Background(), "held"); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				m.operationMu.Lock()
				held := m.held != nil
				m.operationMu.Unlock()
				if !held {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("held sandbox did not restore after cancellation")
				}
				time.Sleep(time.Millisecond)
			}
			assertValidationCleaned(t, m, previous)
		})
	}
	// Loading evidence must reject an unhealthy endpoint and a healthy endpoint
	// still reporting the previous generation.
	for _, unhealthy := range []bool{true, false} {
		healthFailure.Store(unhealthy)
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		err := waitForGenerationHealth(ctx, 9)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "did not load config revision 9") {
			t.Fatalf("unconfirmed generation accepted: %v", err)
		}
	}
	healthFailure.Store(false)
	plain.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := waitForGenerationHealth(ctx, 0); err == nil {
		t.Fatal("unreachable generation health endpoint accepted")
	}
}
