package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestCatalogSourceTrustAndImmutablePinBoundaries(t *testing.T) {
	for _, name := range []string{"name", "type", "trust", "url", "endpoint", "pin", "commit path", "valid git", "valid manifest"} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			in := CreateCatalogSourceInput{Name: "  Source  ", Type: model.CatalogGit, URL: " https://market.example/index.json ", PinnedRef: "SHA256:" + strings.Repeat("A", 64), Trusted: true}
			switch name {
			case "name":
				in.Name = " "
			case "type":
				in.Type = model.CatalogBuiltin
			case "trust":
				in.Trusted = false
			case "url":
				in.URL = "https://localhost/index"
			case "endpoint":
				in.ApprovedEndpoint = "http://unsafe.example/"
			case "pin":
				in.PinnedRef = "latest"
			case "commit path":
				in.PinnedRef = strings.Repeat("a", 40)
			case "valid git":
				in.PinnedRef = strings.Repeat("a", 40)
				in.URL = "https://market.example/" + in.PinnedRef + "/index.json"
			case "valid manifest":
				in.Type = model.CatalogManifest
				in.PinnedRef = ""
			}
			v, err := c.CreateCatalogSource(context.Background(), in)
			valid := strings.HasPrefix(name, "valid ")
			if (err == nil) != valid {
				t.Fatalf("source validation = %+v %v", v, err)
			}
			if valid {
				stored, err := c.store.GetCatalogSource(context.Background(), v.ID)
				if err != nil || stored.Name != "Source" || !stored.Enabled || !stored.Trusted || strings.TrimSpace(stored.URL) != stored.URL {
					t.Fatal("source policy not persisted", err)
				}
			}
		})
	}
	c, _, _ := controlFixture(t)
	for _, test := range []struct {
		source model.CatalogSource
		valid  bool
	}{
		{model.CatalogSource{Type: model.CatalogManifest, URL: "https://market.example/index.yaml"}, true},
		{model.CatalogSource{Type: model.CatalogManifest, URL: "http://market.example/index.yaml"}, false},
		{model.CatalogSource{Type: model.CatalogManifest, URL: "https://market.example/index.yaml", ApprovedEndpoint: "https://localhost/"}, false},
		{model.CatalogSource{Type: model.CatalogBuiltin}, false},
		{model.CatalogSource{Type: model.CatalogGit, URL: "http://market.example/"}, false},
		{model.CatalogSource{Type: model.CatalogGit, URL: "https://market.example/", PinnedRef: "latest"}, false},
		{model.CatalogSource{Type: model.CatalogGit, URL: "https://market.example/", PinnedRef: strings.Repeat("a", 40)}, false},
		{model.CatalogSource{Type: model.CatalogGit, URL: "https://market.example/", PinnedRef: strings.Repeat("a", 64)}, true},
		{model.CatalogSource{Type: model.CatalogGit, URL: "https://market.example/" + strings.Repeat("a", 40) + "/index", PinnedRef: strings.Repeat("a", 40)}, true},
	} {
		if err := c.ValidateCatalogSourceUpdate(test.source); (err == nil) != test.valid {
			t.Fatalf("update policy %+v: %v", test.source, err)
		}
	}
}

func TestCatalogAddressAndMetadataEdgeCases(t *testing.T) {
	for _, u := range []string{"https://%", "https:///missing", "https://market.example/#fragment", "https://app.localhost/index", "https://0.0.0.0/", "https://[fe80::1]/", "https://10.1.2.3/"} {
		if err := validateCatalogURL(u); err == nil {
			t.Fatalf("unsafe URL accepted %q", u)
		}
	}
	for _, ip := range []net.IP{nil, {1, 2, 3}, net.ParseIP("224.0.0.1"), net.ParseIP("0.0.0.0"), net.ParseIP("fc00::1"), net.ParseIP("fe80::1"), net.ParseIP("::ffff:127.0.0.1"), net.ParseIP("198.51.100.1"), net.ParseIP("100::1")} {
		if publicCatalogAddress(ip) {
			t.Fatalf("nonpublic IP accepted %v", ip)
		}
	}
	if !publicCatalogAddress(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("public IPv6 rejected")
	}
	for _, pin := range []string{"", strings.Repeat("z", 40), strings.Repeat("a", 41)} {
		if isDigest(pin) {
			t.Fatalf("invalid pin %q", pin)
		}
	}
	if catalogURLContainsCommit("https://%", strings.Repeat("a", 40)) || catalogURLContainsCommit("https://market.example/a", "short") {
		t.Fatal("invalid commit binding accepted")
	}
	name, description := inferREADMEMetadata("\r\n## Section\r# Name\r\n### More\nDescription\nignored")
	if name != "Name" || description != "Description" {
		t.Fatalf("README metadata %q %q", name, description)
	}
	for _, test := range []struct {
		key, compose, manifest, name, description, version, wantName, wantDescription string
		valid                                                                         bool
	}{
		{"", "services: {}", "", "", "", "1", "", "", false},
		{"app", " ", "", "", "", "1", "", "", false},
		{"app", "services: [", "", "", "", "1", "", "", false},
		{"app", "services:\n  web:\n    image: nginx:alpine", "[", "", "", "1", "", "", false},
		{"app", "services:\n  web:\n    image: nginx:alpine", "name: Manifest\ndescription: Details\nversion: '1'", "", "", "", "Manifest", "Details", true},
		{"app", "services:\n  web:\n    image: nginx:alpine", "", "", "", "1", "app", "", true},
		{"app", "services:\n  web:\n    image: nginx:alpine", "name: Ignored", "Explicit", "Selected", "1", "Explicit", "Selected", true},
	} {
		v, err := (&Core{}).catalogVersionFromFiles("source", test.key, test.name, test.description, test.version, "commit", "artifact", "", map[string]string{"docker/docker-compose.yml": test.compose, "nsc-app.yaml": test.manifest}, time.Now())
		if (err == nil) != test.valid {
			t.Fatalf("catalog analysis validity: %v", err)
		}
		if test.valid && (v.Name != test.wantName || v.Description != test.wantDescription || len(v.Services) != 1) {
			t.Fatalf("catalog metadata lost precedence: %+v", v)
		}
	}
	client := catalogHTTPClient()
	if err := client.CheckRedirect(&http.Request{}, nil); err == nil {
		t.Fatal("catalog redirect bypassed origin boundary")
	}
	transport := client.Transport.(*http.Transport)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, address := range []string{"missing-port", "127.0.0.1:443", "8.8.8.8:443"} {
		conn, err := transport.DialContext(ctx, "tcp", address)
		if conn != nil || err == nil {
			if conn != nil {
				conn.Close()
			}
			t.Fatal("invalid/canceled dial connected")
		}
	}
}

type catalogBoundaryStore struct{ *nginxBoundaryStore }

func (s *catalogBoundaryStore) CreateCatalogSource(ctx context.Context, v *model.CatalogSource) error {
	if err := s.fail("create source", v.ID); err != nil {
		return err
	}
	return s.Store.CreateCatalogSource(ctx, v)
}
func (s *catalogBoundaryStore) ListCatalogSources(ctx context.Context, p storage.Page) ([]model.CatalogSource, error) {
	if err := s.fail("sources", ""); err != nil {
		return nil, err
	}
	return s.Store.ListCatalogSources(ctx, p)
}
func (s *catalogBoundaryStore) UpsertCatalogAppVersion(ctx context.Context, v *model.CatalogAppVersion) error {
	if err := s.fail("upsert version", v.ID); err != nil {
		return err
	}
	return s.Store.UpsertCatalogAppVersion(ctx, v)
}
func (s *catalogBoundaryStore) UpdateCatalogSource(ctx context.Context, v *model.CatalogSource, expected int64) error {
	if err := s.fail("update source", v.ID); err != nil {
		return err
	}
	return s.Store.UpdateCatalogSource(ctx, v, expected)
}

func TestBuiltinCatalogStorageFailuresRemainFailures(t *testing.T) {
	for _, test := range []struct {
		method string
		nth    int
	}{{"sources", 1}, {"create source", 1}, {"upsert version", 1}, {"upsert version", 2}, {"update source", 1}} {
		t.Run(test.method+string(rune('0'+test.nth)), func(t *testing.T) {
			c, _, _ := controlFixture(t)
			failure := errors.New("catalog storage unavailable")
			c.store = &catalogBoundaryStore{&nginxBoundaryStore{Store: c.store, method: test.method, nth: test.nth, failure: failure}}
			if err := c.EnsureBuiltinCatalog(context.Background()); !errors.Is(err, failure) {
				t.Fatal("catalog initialization concealed failure", err)
			}
		})
	}
}
