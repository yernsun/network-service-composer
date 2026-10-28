package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"google.golang.org/protobuf/proto"
)

type sandboxFixtureContext struct{}

// The fixture lives only inside the dedicated sandbox process, on loopback.
// No test Service, Domain, Certificate or discovery entry is written to NSC.
func (m *GenerationManager) ValidateSandbox(ctx context.Context, request *gatewayv2.ValidationRequest) *gatewayv2.ValidationResult {
	return m.validateSandbox(ctx, request, m.Validate)
}

func (m *GenerationManager) validateSandbox(ctx context.Context, request *gatewayv2.ValidationRequest, validate func(context.Context, *gatewayv2.ValidationRequest, bool) *gatewayv2.ValidationResult) *gatewayv2.ValidationResult {
	if request == nil || request.Snapshot == nil {
		return &gatewayv2.ValidationResult{TaskId: request.GetTaskId(), Error: "sandbox validation requires a snapshot"}
	}
	failed := func(message string) *gatewayv2.ValidationResult {
		return &gatewayv2.ValidationResult{TaskId: request.TaskId, Error: message}
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return failed("sandbox fixture listener unavailable")
	}
	backend := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/slow") {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/ws") {
			if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				http.Error(w, "upgrade required", 426)
				return
			}
			conn, buffer, e := w.(http.Hijacker).Hijack()
			if e == nil {
				defer conn.Close()
				_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				_ = buffer.Flush()
			}
			return
		}
		w.Header().Set("X-Sandbox-Fixture", "ready")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("sandbox fixture response\n", 100)))
	})}
	defer backend.Close()
	go func() { _ = backend.Serve(listener) }()
	if err := waitSandboxBackend(ctx, "http://"+listener.Addr().String()); err != nil {
		return failed("sandbox fixture readiness: " + err.Error())
	}
	certificate, key, e := sandboxCertificate()
	if e != nil {
		return failed("sandbox test certificate unavailable")
	}
	port := int32(listener.Addr().(*net.TCPAddr).Port)
	snapshot := &gatewayv2.Snapshot{PoolId: request.Snapshot.PoolId, ConfigRevision: time.Now().UnixNano(), ChallengeUpstream: "http://127.0.0.1:1", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_sandbox", Host: "sandbox.nsc.test", HttpEnabled: true, HttpsEnabled: true, TlsMode: "custom", CertificateId: "cert_sandbox"}}, Certificates: []*gatewayv2.SnapshotCertificate{{Id: "cert_sandbox", Domains: []string{"sandbox.nsc.test"}, CertificatePem: certificate, PrivateKeyPem: key}}, Services: []*gatewayv2.SnapshotService{{Id: "svc_sandbox", QualifiedName: "nsc_sandbox_fixture", DnsName: "127.0.0.1"}}}
	for i, path := range []string{"/", "/slow", "/ws"} {
		snapshot.Routes = append(snapshot.Routes, &gatewayv2.SnapshotRoute{Id: fmt.Sprintf("route_sandbox_%d", i), Host: "sandbox.nsc.test", Path: path, ServiceId: "svc_sandbox", TargetPort: port, UpstreamProtocol: "http", UpstreamName: "nsc_sandbox_fixture", WebsocketEnabled: true})
	}
	snapshot.Snippets = []*gatewayv2.ConfigSnippet{{Scope: "server", ResourceId: "dom_sandbox", Content: "add_header X-Sandbox-Version candidate always; client_max_body_size 1k; gzip on; gzip_types text/plain; gzip_min_length 100;"}, {Scope: "location", ResourceId: "route_sandbox_1", Content: "proxy_read_timeout 100ms;"}}
	checks := []validationCheck{{Host: "sandbox.nsc.test", Path: "/", Method: "GET", Scheme: "http", ExpectedStatus: 200, Headers: map[string]string{"X-Sandbox-Version": "candidate", "X-Sandbox-Fixture": "ready"}}, {Host: "sandbox.nsc.test", Path: "/", Method: "GET", Scheme: "https", ExpectedStatus: 200}, {Host: "sandbox.nsc.test", Path: "/slow", Method: "GET", Scheme: "http", ExpectedStatus: 504}, {Host: "sandbox.nsc.test", Path: "/ws", Method: "GET", Scheme: "http", ExpectedStatus: 101, RequestHeaders: map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}}, {Host: "sandbox.nsc.test", Path: "/", Method: "GET", Scheme: "http", ExpectedStatus: 200, Headers: map[string]string{"Content-Encoding": "gzip"}, RequestHeaders: map[string]string{"Accept-Encoding": "gzip"}}, {Host: "sandbox.nsc.test", Path: "/", Method: "GET", Scheme: "http", ExpectedStatus: 413, Body: strings.Repeat("x", 2048)}}
	h3 := supportsHTTP3(m.config.Binary)
	if h3 {
		snapshot.Domains[0].Http3Enabled = true
		checks = append(checks, validationCheck{Host: "sandbox.nsc.test", Path: "/", Method: "GET", Scheme: "http3", ExpectedStatus: 200, Headers: map[string]string{"X-Sandbox-Version": "candidate", "X-Sandbox-Fixture": "ready"}})
	}
	body, _ := json.Marshal(checks)
	fixtureCtx := context.WithValue(ctx, sandboxFixtureContext{}, true)
	fixture := validate(fixtureCtx, &gatewayv2.ValidationRequest{TaskId: request.TaskId, Snapshot: snapshot, ActivateSandbox: true, ChecksJson: string(body)}, true)
	if !fixture.Success {
		fixture.Error = "isolated fixture: " + fixture.Error
		return fixture
	}
	broken := proto.Clone(snapshot).(*gatewayv2.Snapshot)
	broken.Snippets = append(broken.Snippets, &gatewayv2.ConfigSnippet{Scope: "http", Content: "proxy_buffers 2 impossible;"})
	rejected := validate(fixtureCtx, &gatewayv2.ValidationRequest{TaskId: request.TaskId, Snapshot: broken}, true)
	if rejected.Success {
		return failed("invalid candidate was not rejected")
	}
	// Validate again after rejection to verify restoration and subsequent reuse.
	restored := validate(fixtureCtx, &gatewayv2.ValidationRequest{TaskId: request.TaskId, Snapshot: snapshot, ActivateSandbox: true, ChecksJson: string(body)}, true)
	if !restored.Success {
		restored.Error = "fixture restore: " + restored.Error
		return restored
	}
	result := validate(ctx, request, true)
	var realChecks any
	_ = json.Unmarshal([]byte(result.ChecksJson), &realChecks)
	report, _ := json.Marshal(map[string]any{"fixture_passed": true, "http3_passed": h3, "invalid_candidate_rejected": true, "restore_passed": true, "real_route_checks": realChecks, "real_route_count": lenJSONChecks(request.ChecksJson)})
	result.ChecksJson = string(report)
	return result
}

func waitSandboxBackend(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err == nil {
			ready := response.StatusCode == http.StatusOK && response.Header.Get("X-Sandbox-Fixture") == "ready"
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
			response.Body.Close()
			if ready {
				return nil
			}
		}
		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return fmt.Errorf("backend did not become ready: %w", err)
		}
	}
}
func lenJSONChecks(raw string) int {
	var checks []json.RawMessage
	_ = json.Unmarshal([]byte(raw), &checks)
	return len(checks)
}
func sandboxCertificate() (string, string, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", "", e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return "", "", e
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "sandbox.nsc.test"}, DNSNames: []string{"sandbox.nsc.test", "off.sandbox.nsc.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		return "", "", e
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return "", "", e
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), nil
}
