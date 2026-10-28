package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTP3IsolationAndTCPFallbackWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated NGINX")
	}
	if !supportsHTTP3(binary) {
		t.Fatal("NGINX HTTP/3 module missing")
	}
	m := NewGenerationManager(config.NGINXConfig{Binary: binary, GenerationDir: t.TempDir(), WorkerProcesses: "1", KeepGenerations: 3, HTTP3AdvertisedPort: 18443})
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	}()
	cert, key, err := sandboxCertificate()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(cert))
	snap := &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 1, ChallengeUpstream: "http://127.0.0.1:1", Certificates: []*gatewayv2.SnapshotCertificate{{Id: "cert_test", CertificatePem: cert, PrivateKeyPem: key}}, Domains: []*gatewayv2.SnapshotDomain{
		{Id: "dom_on", Host: "sandbox.nsc.test", HttpsEnabled: true, Http3Enabled: true, TlsMode: "custom", CertificateId: "cert_test"},
		{Id: "dom_off", Host: "off.sandbox.nsc.test", HttpsEnabled: true, TlsMode: "custom", CertificateId: "cert_test"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := m.Validate(ctx, &gatewayv2.ValidationRequest{TaskId: "h3", Snapshot: snap, ActivateSandbox: true, HoldUntilUnix: time.Now().Add(time.Minute).Unix()}, true)
	if !result.Success {
		t.Fatal(result.Error)
	}
	for _, host := range []string{"sandbox.nsc.test", "off.sandbox.nsc.test"} {
		tr := http3Transport(&tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS13}, "127.0.0.1:443")
		client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
		resp, err := client.Get("https://" + host + "/")
		if host == "off.sandbox.nsc.test" {
			if err == nil {
				resp.Body.Close()
				t.Fatal("disabled domain negotiated HTTP/3")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.ProtoMajor != 3 || resp.StatusCode != 404 || resp.Header.Get("Alt-Svc") != `h3=":18443"; ma=300` {
				t.Fatalf("HTTP/3 response: %+v", resp)
			}
		}
		_ = tr.Close()
		for _, h2 := range []bool{false, true} {
			tcp := &http.Transport{ForceAttemptHTTP2: h2, TLSClientConfig: &tls.Config{ServerName: host, RootCAs: roots}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:443")
			}}
			resp, err := (&http.Client{Transport: tcp, Timeout: 3 * time.Second}).Get("https://" + host + "/")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			tcp.CloseIdleConnections()
			want := 1
			if h2 {
				want = 2
			}
			if resp.ProtoMajor != want || resp.StatusCode != 404 {
				t.Fatalf("TCP fallback: %s %d", resp.Proto, resp.StatusCode)
			}
			if host == "off.sandbox.nsc.test" && resp.Header.Get("Alt-Svc") != "" {
				t.Fatal("disabled domain advertised HTTP/3")
			}
		}
	}
	if err := m.CancelValidation(ctx, "h3"); err != nil {
		t.Fatal(err)
	}
}

func TestRendererUpgradeRetainsValidatedGeneration(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated NGINX")
	}
	cfg := config.NGINXConfig{Binary: binary, GenerationDir: t.TempDir(), WorkerProcesses: "1", KeepGenerations: 3}
	m := NewGenerationManager(cfg)
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(cfg.GenerationDir, "gen-previous")
	snapshot := &gatewayv2.Snapshot{PoolId: "pool_upgrade", ConfigRevision: 7, ChallengeUpstream: "http://127.0.0.1:1", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_old", Host: "old.example.com", HttpEnabled: true}}}
	if err := m.renderer.Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	if err := m.linkGenerationMainConfig(directory); err != nil {
		t.Fatal(err)
	}
	digest, err := digestGeneration(directory)
	if err != nil {
		t.Fatal(err)
	}
	state := GenerationState{PoolID: "pool_upgrade", ConfigRevision: 7, ModelDigest: strings.Repeat("a", 64), RenderedDigest: digest, RendererVersion: "previous-renderer"}
	if err = writeGenerationStateFile(directory, "generation.json", state); err != nil {
		t.Fatal(err)
	}
	if err = replaceSymlink(cfg.GenerationDir, "gen-previous"); err != nil {
		t.Fatal(err)
	}
	restored, err := NewGenerationManager(cfg).Prepare()
	if err != nil || restored.ConfigRevision != 7 || restored.RenderedDigest != digest || restored.RendererVersion != "previous-renderer" {
		t.Fatal("lost prior generation", restored, err)
	}
	if err = os.WriteFile(filepath.Join(directory, "domains/old.example.com.conf"), []byte("invalid_directive;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = NewGenerationManager(cfg).Prepare(); err == nil {
		t.Fatal("invalid previous generation accepted")
	}
	target, _ := os.Readlink(filepath.Join(cfg.GenerationDir, "current"))
	if target != "gen-previous" {
		t.Fatal("failed upgrade silently replaced configuration", target)
	}
}

func TestRendererUpgradeReappliesIdenticalModelWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated NGINX")
	}
	cfg := config.NGINXConfig{Binary: binary, GenerationDir: t.TempDir(), WorkerProcesses: "1", KeepGenerations: 3}
	m := NewGenerationManager(cfg)
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	snapshot := &gatewayv2.Snapshot{PoolId: "pool_upgrade", ConfigRevision: 7, ChallengeUpstream: "http://127.0.0.1:1", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_old", Host: "old.example.com", HttpEnabled: true}}}
	dir := filepath.Join(cfg.GenerationDir, "gen-previous")
	if err := m.renderer.Render(snapshot, dir); err != nil {
		t.Fatal(err)
	}
	if err := m.linkGenerationMainConfig(dir); err != nil {
		t.Fatal(err)
	}
	// Simulate actual older renderer bytes, not just an older metadata label.
	oldSystem := filepath.Join(dir, "system", "default.conf")
	content, err := os.ReadFile(oldSystem)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), nginxx.RendererVersion) {
		t.Fatal("fixture does not contain the current renderer marker")
	}
	content = []byte(strings.ReplaceAll(string(content), nginxx.RendererVersion, "previous-renderer"))
	content = []byte(strings.ReplaceAll(string(content), "variables_hash_bucket_size 128;\nvariables_hash_max_size 8192;\n", ""))
	if err = os.WriteFile(oldSystem, content, 0640); err != nil {
		t.Fatal(err)
	}
	// The deployed v7 renderer did not put IP-access variable hash settings in
	// its fixed main file. Recreate those actual legacy bytes before recovery.
	legacyMain := strings.ReplaceAll(string(m.renderer.MainConfig(filepath.Join(m.root, "nginx.pid"))), "  variables_hash_bucket_size 128;\n  variables_hash_max_size 8192;\n", "")
	if err = os.WriteFile(m.mainConfigPath(), []byte(legacyMain), 0640); err != nil {
		t.Fatal(err)
	}
	digest, err := digestGeneration(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := GenerationState{PoolID: "pool_upgrade", ConfigRevision: 7, ModelDigest: strings.Repeat("a", 64), RenderedDigest: digest, RendererVersion: "previous-renderer"}
	if err = writeGenerationStateFile(dir, "generation.json", state); err != nil {
		t.Fatal(err)
	}
	if err = replaceSymlink(cfg.GenerationDir, "gen-previous"); err != nil {
		t.Fatal(err)
	}
	m = NewGenerationManager(cfg)
	if _, err = m.Prepare(); err != nil {
		t.Fatal(err)
	}
	if recoveredDigest, err := digestGeneration(dir); err != nil || recoveredDigest != state.RenderedDigest {
		t.Fatal("renderer upgrade modified the legacy generation before receiving a snapshot", recoveredDigest, err)
	}
	if _, err = m.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer m.Stop(context.Background())
	if _, err = m.Apply(ctx, snapshot, state.ModelDigest); err != nil {
		t.Fatal("same model upgrade", err)
	}
	got := m.State()
	if got.ConfigRevision != 7 || got.ModelDigest != state.ModelDigest || got.RendererVersion == "previous-renderer" || got.RenderedDigest == state.RenderedDigest {
		t.Fatal("incorrect upgraded state", got)
	}
	evidence := m.Observe(ctx)
	if !evidence.LoadingConfirmed {
		t.Fatal("upgraded generation not loaded", evidence)
	}
	if _, err = m.Apply(ctx, snapshot, state.ModelDigest); err == nil {
		t.Fatal("replay accepted after upgrade")
	}
	current, err := filepath.EvalSymlinks(filepath.Join(m.root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(current, "custom", "external.conf"), []byte("# external modification\n"), 0640); err != nil {
		t.Fatal(err)
	}
	snapshot.ConfigRevision++
	result, err := m.Apply(ctx, snapshot, strings.Repeat("d", 64))
	if err == nil || result.FailedPhase != "external-change" || m.State() != got {
		t.Fatal("external modification protection was weakened by renderer recovery", result, err)
	}
}
