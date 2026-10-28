package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var catalogFixtureTrust struct {
	sync.Once
	certificate tls.Certificate
	err         error
}

// Exercise the production SSRF transport without a public network connection.
// A globally routable-looking address exists only on an isolated loopback device.
func catalogHTTPSFixture(t *testing.T, handler http.Handler) (*httptest.Server, bool) {
	t.Helper()
	if os.Getenv("NSC_TEST_ISOLATED_NETWORK") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "-Urn", "--", "sh", "-c", `ip link set lo up && NSC_TEST_ISOLATED_NETWORK=1 exec "$@"`, "sh", os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v")
		output, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("isolated catalog fixture: %v\n%s", e, output)
		}
		t.Logf("isolated catalog fixture:\n%s", output)
		return nil, false
	}
	uidMap, e := os.ReadFile("/proc/self/uid_map")
	if e != nil {
		t.Fatal(e)
	}
	fields := strings.Fields(string(uidMap))
	interfaces, e := net.Interfaces()
	if e != nil || len(fields) != 3 || fields[2] != "1" || len(interfaces) != 1 || interfaces[0].Flags&net.FlagLoopback == 0 {
		t.Fatal("catalog fixture requires a private user/network namespace with loopback only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, e := exec.CommandContext(ctx, "ip", "addr", "add", "93.184.216.34/32", "dev", "lo").CombinedOutput(); e != nil {
		t.Fatalf("isolated fixture address: %v %s", e, output)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if output, e := exec.CommandContext(ctx, "ip", "addr", "del", "93.184.216.34/32", "dev", "lo").CombinedOutput(); e != nil {
			t.Errorf("remove isolated fixture address: %v %s", e, output)
		}
	})
	t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",x509usefallbackroots=1")
	catalogFixtureTrust.Do(func() {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			catalogFixtureTrust.err = e
			return
		}
		now := time.Now()
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "NSC isolated catalog fixture"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("93.184.216.34")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			catalogFixtureTrust.err = e
			return
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			catalogFixtureTrust.err = e
			return
		}
		pool := x509.NewCertPool()
		pool.AddCert(cert)
		x509.SetFallbackRoots(pool)
		catalogFixtureTrust.certificate = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}
	})
	if catalogFixtureTrust.err != nil {
		t.Fatal(catalogFixtureTrust.err)
	}
	listener, e := net.Listen("tcp", "93.184.216.34:0")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{catalogFixtureTrust.certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, true
}

type catalogSyncBoundaryStore struct {
	storage.Store
	stage   string
	failure error
	reads   int
}

func (s *catalogSyncBoundaryStore) SyncCatalogAppVersions(ctx context.Context, source *model.CatalogSource, versions []model.CatalogAppVersion, expected int64) error {
	if s.stage == "write" {
		return s.failure
	}
	return s.Store.SyncCatalogAppVersions(ctx, source, versions, expected)
}
func (s *catalogSyncBoundaryStore) GetCatalogSource(ctx context.Context, key string) (model.CatalogSource, error) {
	s.reads++
	if s.stage == "reread" && s.reads > 1 {
		return model.CatalogSource{}, s.failure
	}
	return s.Store.GetCatalogSource(ctx, key)
}

func TestCatalogHTTPSAcquisitionAndManifestSynchronization(t *testing.T) {
	var manifestPayload atomic.Value
	compose := "services:\n  web:\n    image: example.test/web:fixed\n"
	readme := "# Web\n\nAn isolated marketplace fixture.\n"
	server, inside := catalogHTTPSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(503)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
		case "/cut":
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte("partial"))
		case "/redirect":
			http.Redirect(w, r, "/ok", 302)
		case "/market.yaml":
			_, _ = w.Write(manifestPayload.Load().([]byte))
		case "/compose.yaml":
			_, _ = w.Write([]byte(compose))
		case "/README.md":
			_, _ = w.Write([]byte(readme))
		default:
			_, _ = w.Write([]byte("fixture"))
		}
	}))
	if !inside {
		return
	}
	ctx := context.Background()
	for _, path := range []string{"/ok", "/status", "/large", "/cut", "/redirect"} {
		data, e := fetchMarketFile(ctx, server.URL+path)
		if path == "/ok" {
			if e != nil || string(data) != "fixture" {
				t.Fatal("verified HTTPS fetch failed", e)
			}
		} else if e == nil || data != nil {
			t.Fatal("failed market fetch returned trusted content", path)
		}
	}
	if data, e := fetchMarketFile(ctx, "https://localhost/private"); e == nil || data != nil {
		t.Fatal("market fetch bypassed SSRF policy")
	}
	if data, e := fetchMarketFile(nil, server.URL+"/ok"); e == nil || data != nil {
		t.Fatal("nil request context accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := fetchMarketFile(cancelled, server.URL+"/ok"); e == nil {
		t.Fatal("cancelled market fetch succeeded")
	}
	m := marketplace.Manifest{SchemaVersion: 1, Marketplace: marketplace.Metadata{ID: "fixture", Name: marketplace.Text{"en": "Fixture"}, Endpoint: server.URL + "/", DefaultLocale: "en", Locales: []string{"en"}}, Apps: []marketplace.App{{ID: "web", Name: marketplace.Text{"en": "Web"}, Versions: []marketplace.Version{{Version: "1", Files: marketplace.Files{Compose: marketplace.File{Path: "compose.yaml", SHA256: marketplace.Digest([]byte(compose))}, Readme: map[string]marketplace.File{"en": {Path: "README.md", SHA256: marketplace.Digest([]byte(readme))}}}}}}}}
	valid, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	for _, stage := range []string{"success", "fetch", "invalid manifest", "write", "reread"} {
		t.Run(stage, func(t *testing.T) {
			manifestPayload.Store(valid)
			if stage == "invalid manifest" {
				manifestPayload.Store([]byte("schema_version: -1"))
			}
			c, _, _ := controlFixture(t)
			base := c.store
			url := server.URL + "/market.yaml"
			if stage == "fetch" {
				url = server.URL + "/status"
			}
			source, e := c.CreateCatalogSource(ctx, CreateCatalogSourceInput{Name: "TLS market", Type: model.CatalogManifest, URL: url, Trusted: true})
			if e != nil {
				t.Fatal(e)
			}
			failure := errors.New("catalog commit unavailable")
			wrapped := &catalogSyncBoundaryStore{Store: base, stage: stage, failure: failure}
			c.store = wrapped
			got, count, e := c.syncCatalogSource(ctx, source.ID)
			if stage == "success" {
				if e != nil || count != 1 || got.LastSyncedAt == nil || got.MarketJSON == "" {
					t.Fatal("manifest synchronization not persisted", e)
				}
				versions, e := base.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 10})
				if e != nil || len(versions) != 1 || versions[0].ComposeYAML != compose || versions[0].README != readme {
					t.Fatal("market version content changed", e)
				}
				return
			}
			if e == nil {
				t.Fatal("failed catalog sync reported success")
			}
			if (stage == "write" || stage == "reread") && !errors.Is(e, failure) {
				t.Fatal("storage failure lost", e)
			}
			if stage != "reread" {
				if count != 0 {
					t.Fatal("failed sync reported installed versions")
				}
				versions, e := base.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 10})
				if e != nil || len(versions) != 0 {
					t.Fatal("failed sync persisted partial versions", e)
				}
			}
		})
	}
}

func TestCatalogHTTPSPinnedIndexValidationAndAtomicCommit(t *testing.T) {
	var body atomic.Value
	var mode atomic.Value
	body.Store([]byte{})
	mode.Store("")
	server, inside := catalogHTTPSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "status":
			w.WriteHeader(503)
			return
		case "cut":
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte("partial"))
			return
		}
		_, _ = w.Write(body.Load().([]byte))
	}))
	if !inside {
		return
	}
	ctx := context.Background()
	commit := strings.Repeat("a", 40)
	for _, stage := range []string{"success", "status", "cut", "oversize", "transport", "index hash", "missing Git pin", "Git pin success", "Git commit mismatch", "malformed", "trailing", "unknown field", "schema", "duplicate", "missing compose", "empty compose", "invalid commit", "compose hash", "artifact hash", "invalid compose", "missing version", "write", "reread"} {
		t.Run(stage, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			base := c.store
			compose := "services:\n  web:\n    image: example.test/web:fixed\n"
			files := map[string]string{"docker/docker-compose.yml": compose, "README.md": "# Web"}
			remote := remoteCatalogVersion{AppKey: "web", Name: "Web", Version: "1", Commit: commit, Files: files}
			switch stage {
			case "missing compose":
				delete(files, "docker/docker-compose.yml")
			case "empty compose":
				files["docker/docker-compose.yml"] = " "
			case "invalid commit":
				remote.Commit = "not-a-commit"
			case "Git commit mismatch":
				remote.Commit = strings.Repeat("b", 40)
			case "invalid compose":
				files["docker/docker-compose.yml"] = "services: [broken"
			case "missing version":
				remote.Version = ""
			}
			remote.ComposeSHA256 = digestString(files["docker/docker-compose.yml"])
			remote.ArtifactDigest = catalogArtifactDigest(files["docker/docker-compose.yml"], "", "", files["README.md"])
			if stage == "compose hash" {
				remote.ComposeSHA256 = strings.Repeat("f", 64)
			}
			if stage == "artifact hash" {
				remote.ArtifactDigest = strings.Repeat("f", 64)
			}
			index := remoteCatalogIndex{SchemaVersion: 1, Versions: []remoteCatalogVersion{remote}}
			if stage == "schema" {
				index.SchemaVersion = 2
			}
			if stage == "duplicate" {
				index.Versions = append(index.Versions, remote)
			}
			payload, e := json.Marshal(index)
			if e != nil {
				t.Fatal(e)
			}
			switch stage {
			case "oversize":
				payload = []byte(strings.Repeat("x", (4<<20)+1))
			case "malformed":
				payload = []byte("{")
			case "trailing":
				payload = append(payload, []byte(" {}")...)
			case "unknown field":
				payload = []byte(`{"schema_version":1,"unexpected":true}`)
			}
			body.Store(payload)
			mode.Store(stage)
			now := time.Now().UTC()
			previous := now.Add(-time.Hour)
			source := model.CatalogSource{ID: "source", Name: "Pinned fixture", Type: model.CatalogGit, URL: server.URL + "/index.json", PinnedRef: digestBytes(payload), Enabled: true, Trusted: true, Revision: 1, CreatedAt: now, UpdatedAt: now, LastSyncedAt: &previous}
			if stage == "index hash" {
				source.PinnedRef = strings.Repeat("f", 64)
			}
			if strings.HasPrefix(stage, "Git ") {
				source.PinnedRef = commit
				source.URL = server.URL + "/" + commit + "/index.json"
			}
			if stage == "missing Git pin" {
				source.PinnedRef = commit
			}
			if stage == "transport" {
				source.URL = "https://93.184.216.34:1/index.json"
			}
			if e = base.CreateCatalogSource(ctx, &source); e != nil {
				t.Fatal(e)
			}
			failure := errors.New("catalog transaction unavailable")
			wrapped := &catalogSyncBoundaryStore{Store: base, stage: stage, failure: failure}
			c.store = wrapped
			got, count, e := c.syncCatalogSource(ctx, source.ID)
			success := stage == "success" || stage == "Git pin success"
			if success {
				if e != nil || count != 1 || got.LastSyncedAt == nil || !got.LastSyncedAt.After(previous) || got.LastError != "" {
					t.Fatal("valid pinned index not synchronized", e)
				}
				versions, e := base.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 10})
				if e != nil || len(versions) != 1 || versions[0].Commit != commit || versions[0].ComposeYAML != compose {
					t.Fatal("pinned artifact content not preserved", e)
				}
				return
			}
			if e == nil {
				t.Fatal("unverified catalog index committed")
			}
			if stage == "write" || stage == "reread" {
				if !errors.Is(e, failure) {
					t.Fatal("commit failure cause lost", e)
				}
			}
			if stage != "reread" {
				if count != 0 {
					t.Fatal("failed sync reported installed artifacts")
				}
				stored, e := base.GetCatalogSource(ctx, source.ID)
				if e != nil || stored.LastError == "" || stored.LastSyncedAt == nil || !stored.LastSyncedAt.Equal(previous) {
					t.Fatal("failure erased last successful synchronization", e)
				}
				versions, e := base.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 10})
				if e != nil || len(versions) != 0 {
					t.Fatal("invalid index created partial catalog versions", e)
				}
			}
		})
	}
}
