package agent

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"golang.org/x/net/dns/dnsmessage"
)

// TestGenerationManagerWithRealNGINX is opt-in so ordinary unit tests do not
// require a system NGINX. CI and release validation run the compiled test
// binary inside the pinned gateway image with NSC_TEST_NGINX_BINARY set.
func TestGenerationManagerWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("set NSC_TEST_NGINX_BINARY to run the real NGINX integration test")
	}
	if os.Getenv("NSC_TEST_LOCAL_DNS") == "true" {
		startIntegrationDNS(t)
	}
	httpPort := 80
	httpsPort := 443
	root := t.TempDir()
	cfg := config.NGINXConfig{Binary: binary, GenerationDir: root, KeepGenerations: 3, WorkerProcesses: "1"}

	plainBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "plain:"+r.URL.Path)
	}))
	defer plainBackend.Close()
	rootBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "root:"+r.URL.Path)
	}))
	defer rootBackend.Close()
	upstreamPEM, upstreamKey := testCertificate(t, "localhost")
	upstreamCertificate, err := tls.X509KeyPair([]byte(upstreamPEM), []byte(upstreamKey))
	if err != nil {
		t.Fatal(err)
	}
	cfg.UpstreamCAFile = filepath.Join(root, "upstream-ca.pem")
	if err := os.WriteFile(cfg.UpstreamCAFile, []byte(upstreamPEM), 0600); err != nil {
		t.Fatal(err)
	}
	tlsBackend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tls:"+r.URL.Path)
	}))
	tlsBackend.TLS = &tls.Config{Certificates: []tls.Certificate{upstreamCertificate}, MinVersion: tls.VersionTLS12}
	tlsBackend.StartTLS()
	defer tlsBackend.Close()
	websocketBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			http.Error(w, "missing websocket upgrade headers", http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking is unavailable", http.StatusInternalServerError)
			return
		}
		connection, buffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = buffer.Flush()
	}))
	defer websocketBackend.Close()
	plainPort := serverPort(t, plainBackend.Listener.Addr())
	rootPort := serverPort(t, rootBackend.Listener.Addr())
	tlsPort := serverPort(t, tlsBackend.Listener.Addr())
	websocketPort := serverPort(t, websocketBackend.Listener.Addr())
	certificatePEM, keyPEM := testCertificate(t, "app.example.com")

	manager := NewGenerationManager(cfg)
	if _, err := manager.Prepare(); err != nil {
		t.Fatal(err)
	}
	done, err := manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	stopManager := func(value *GenerationManager) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := value.Stop(ctx); err != nil {
			t.Errorf("stop NGINX: %v", err)
		}
	}
	defer func() { stopManager(manager) }()
	waitForHTTPStatus(t, httpPort, "unknown.example.com", "/", http.StatusNotFound, nil)
	select {
	case err := <-done:
		t.Fatalf("NGINX exited before apply: %v", err)
	default:
	}

	snapshot := integrationSnapshot(1, rootPort, plainPort, tlsPort, websocketPort, certificatePEM, keyPEM)
	if _, err := manager.Apply(context.Background(), snapshot, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := Healthcheck(context.Background(), config.Config{NGINX: cfg, Agent: config.AgentConfig{PoolID: snapshot.PoolId}}); err != nil {
		t.Fatalf("real loaded generation failed read-only healthcheck: %v", err)
	}
	waitForHTTPBody(t, httpPort, "app.example.com", "/other", "root:/other", nil)

	// These directives pass NSC's bounded snippet parser but violate NGINX's
	// buffer relationship. Real syntax rejection must preserve the complete LKG
	// tuple on disk and keep the previously loaded HTTP/TLS generation serving.
	invalidNGINX := integrationSnapshot(2, rootPort, plainPort, tlsPort, websocketPort, certificatePEM, keyPEM)
	invalidNGINX.Snippets = []*gatewayv2.ConfigSnippet{{Scope: "http", Content: "proxy_buffers 2 1k; proxy_buffer_size 8k; proxy_busy_buffers_size 8k;"}}
	lkgBefore := manager.State()
	result, err := manager.Apply(context.Background(), invalidNGINX, strings.Repeat("c", 64))
	if err == nil || result.FailedPhase != "validate" {
		t.Fatalf("real NGINX accepted invalid buffer configuration: %#v, %v", result, err)
	}
	persisted, persistErr := readGenerationState(filepath.Join(root, "state.json"))
	if persistErr != nil || persisted != lkgBefore || manager.State() != lkgBefore {
		t.Fatalf("NGINX rejection changed persisted LKG: %#v, %#v, %v", manager.State(), persisted, persistErr)
	}
	evidence := manager.Observe(context.Background())
	if !evidence.LoadingConfirmed || evidence.StartupState != "CONFIRMED" || evidence.DiskDigest != lkgBefore.RenderedDigest {
		t.Fatalf("NGINX rejection damaged loaded/next-start LKG: %#v", evidence)
	}
	waitForHTTPBody(t, httpPort, "app.example.com", "/other", "root:/other", nil)
	evidence = manager.Observe(context.Background())
	if !evidence.LoadingConfirmed || evidence.StartupState != "CONFIRMED" || evidence.DiskDigest != manager.State().RenderedDigest {
		t.Fatalf("new generation has no trustworthy loading/startup evidence: %#v", evidence)
	}
	waitForHTTPBody(t, httpPort, "app.example.com", "/api/value", "plain:/value", nil)
	waitForHTTPBody(t, httpPort, "alias.example.com", "/api/value", "plain:/value", nil)
	waitForHTTPBody(t, httpPort, "second-alias.example.com", "/api/value", "plain:/value", nil)
	waitForWebSocketSwitch(t, httpPort, "app.example.com", "/ws/socket")
	tlsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "app.example.com"}}, Timeout: 2 * time.Second} // #nosec G402 -- loopback test certificate
	waitForHTTPBody(t, httpsPort, "app.example.com", "/tls/value", "tls:/tls/value", tlsClient)

	plainBackend.Close()
	waitForHTTPStatus(t, httpPort, "app.example.com", "/api/value", http.StatusBadGateway, nil)

	broken := integrationSnapshot(2, rootPort, plainPort, tlsPort, websocketPort, certificatePEM, keyPEM)
	broken.Routes[0].Path = "/; return 200;"
	if _, err := manager.Apply(context.Background(), broken, strings.Repeat("c", 64)); err == nil {
		t.Fatal("invalid candidate unexpectedly applied")
	}
	if manager.State().ConfigRevision != 1 {
		t.Fatalf("failed candidate changed LKG revision to %d", manager.State().ConfigRevision)
	}
	waitForHTTPBody(t, httpPort, "app.example.com", "/other", "root:/other", nil)

	// A running old worker does not make a modified next-start configuration safe.
	main := filepath.Join(root, "current", generationMainConfigName)
	original, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, append(original, []byte("\ninvalid_external_directive;\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	evidence = manager.Observe(context.Background())
	if !evidence.LoadingConfirmed || evidence.StartupState != "RISK" || evidence.DiskDigest == manager.State().RenderedDigest {
		t.Fatalf("external change was hidden by successful running requests: %#v", evidence)
	}
	result, err = manager.Apply(context.Background(), integrationSnapshot(3, rootPort, plainPort, tlsPort, websocketPort, certificatePEM, keyPEM), strings.Repeat("d", 64))
	if err == nil || result.FailedPhase != "external-change" {
		t.Fatalf("external change was overwritten: %#v, %v", result, err)
	}
	if err := os.WriteFile(main, original, 0600); err != nil {
		t.Fatal(err)
	}

	stopManager(manager)
	manager = NewGenerationManager(cfg)
	state, err := manager.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if state.PoolID != "pool_test" || state.ConfigRevision != 1 || state.ModelDigest != strings.Repeat("a", 64) {
		t.Fatalf("did not restore LKG state: %#v", state)
	}
	if _, err = manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForHTTPBody(t, httpPort, "app.example.com", "/other", "root:/other", nil)
}

// Used with --network none to keep the real-NGINX test independent of upstream
// DNS and Docker's treatment of localhost. Never enabled in normal Agent runs.
func startIntegrationDNS(t *testing.T) {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.11:53")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buffer[:n]) != nil {
				continue
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true}, Questions: request.Questions}
			for _, question := range request.Questions {
				if question.Name.String() == "localhost." && question.Type == dnsmessage.TypeA {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				} else {
					response.RCode = dnsmessage.RCodeNameError
				}
			}
			if packet, err := response.Pack(); err == nil {
				_, _ = conn.WriteTo(packet, addr)
			}
		}
	}()
}

func integrationSnapshot(revision int64, rootPort, plainPort, tlsPort, websocketPort int, certificatePEM, keyPEM string) *gatewayv2.Snapshot {
	const certificateID = "cert_test"
	return &gatewayv2.Snapshot{
		PoolId:         "pool_test",
		ConfigRevision: revision,
		// This name deliberately does not resolve. ACME proxy DNS resolution must
		// be deferred so an unavailable Controller cannot prevent an LKG start.
		ChallengeUpstream: "http://controller-is-offline.invalid:8080",
		Domains: []*gatewayv2.SnapshotDomain{
			{Id: "dom_app", Host: "app.example.com", HttpEnabled: true, HttpsEnabled: true, TlsMode: "custom", CertificateId: certificateID},
			{Id: "dom_alias", Host: "alias.example.com", HttpEnabled: true, TlsMode: "disabled"},
			{Id: "dom_second_alias", Host: "second-alias.example.com", HttpEnabled: true, TlsMode: "disabled"},
		},
		Services: []*gatewayv2.SnapshotService{
			{Id: "svc_root", QualifiedName: "test_root", DnsName: "localhost"},
			{Id: "svc_plain", QualifiedName: "test_plain", DnsName: "localhost"},
			{Id: "svc_tls", QualifiedName: "test_tls", DnsName: "localhost"},
			{Id: "svc_websocket", QualifiedName: "test_websocket", DnsName: "localhost"},
		},
		Routes: []*gatewayv2.SnapshotRoute{
			{Id: "route_root", Host: "app.example.com", Path: "/", ServiceId: "svc_root", TargetPort: int32(rootPort), UpstreamProtocol: "http", UpstreamName: "nsc_test_root_a1b2c3d4"},
			{Id: "route_plain", Host: "app.example.com", Path: "/api/", ServiceId: "svc_plain", TargetPort: int32(plainPort), UpstreamProtocol: "http", UpstreamName: "nsc_test_plain_a1b2c3d4", StripPath: true},
			{Id: "route_tls", Host: "app.example.com", Path: "/tls/", ServiceId: "svc_tls", TargetPort: int32(tlsPort), UpstreamProtocol: "https", UpstreamName: "nsc_test_tls_a1b2c3d4"},
			{Id: "route_websocket", Host: "app.example.com", Path: "/ws/", ServiceId: "svc_websocket", TargetPort: int32(websocketPort), UpstreamProtocol: "http", UpstreamName: "nsc_test_websocket_a1b2c3d4", StripPath: true, WebsocketEnabled: true},
			{Id: "route_alias", Host: "alias.example.com", Path: "/api/", ServiceId: "svc_plain", TargetPort: int32(plainPort), UpstreamProtocol: "http", UpstreamName: "nsc_test_plain_a1b2c3d4", StripPath: true},
			{Id: "route_second_alias", Host: "second-alias.example.com", Path: "/api/", ServiceId: "svc_plain", TargetPort: int32(plainPort), UpstreamProtocol: "http", UpstreamName: "nsc_test_plain_a1b2c3d4", StripPath: true},
		},
		Certificates: []*gatewayv2.SnapshotCertificate{{Id: certificateID, CertificatePem: certificatePEM, PrivateKeyPem: keyPEM}},
	}
}

func waitForWebSocketSwitch(t *testing.T, port int, host, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			_ = connection.SetDeadline(time.Now().Add(time.Second))
			_, writeErr := fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", path, host)
			statusLine, readErr := bufio.NewReader(connection).ReadString('\n')
			_ = connection.Close()
			last = strings.TrimSpace(statusLine)
			if writeErr == nil && readErr == nil && strings.Contains(statusLine, " 101 ") {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for WebSocket upgrade: %s", last)
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func serverPort(t *testing.T, address net.Addr) int {
	t.Helper()
	_, portValue, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func testCertificate(t *testing.T, host string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
}

func waitForHTTPStatus(t *testing.T, port int, host, path string, status int, client *http.Client) {
	t.Helper()
	waitForHTTP(t, port, host, path, client, func(response *http.Response, _ string) bool { return response.StatusCode == status }, fmt.Sprintf("HTTP %d", status))
}

func waitForHTTPBody(t *testing.T, port int, host, path, body string, client *http.Client) {
	t.Helper()
	waitForHTTP(t, port, host, path, client, func(response *http.Response, value string) bool {
		return response.StatusCode == http.StatusOK && value == body
	}, body)
}

func waitForHTTP(t *testing.T, port int, host, path string, client *http.Client, accept func(*http.Response, string) bool, expected string) {
	t.Helper()
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	scheme := "http"
	if client.Transport != nil {
		scheme = "https"
	}
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, port, path), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = host
		response, err := client.Do(request)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			last = fmt.Sprintf("status=%d body=%q", response.StatusCode, string(data))
			if readErr == nil && accept(response, string(data)) {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %s", expected, last)
}
