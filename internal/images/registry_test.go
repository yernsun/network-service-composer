package images

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveManifestDigestAndRetag(t *testing.T) {
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:abc","platform":{"os":"linux","architecture":"amd64"}}]}`
	bad := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/demo/manifests/dev" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		digest := sha256.Sum256([]byte(manifest))
		header := "sha256:" + hex.EncodeToString(digest[:])
		if bad {
			header = "sha256:" + strings.Repeat("f", 64)
		}
		w.Header().Set("Docker-Content-Digest", header)
		_, _ = w.Write([]byte(manifest))
	}))
	defer server.Close()
	resolver := Resolver{Insecure: true}
	ref := strings.TrimPrefix(server.URL, "http://") + "/demo:dev"
	first, e := resolver.Resolve(context.Background(), ref)
	if e != nil || !strings.Contains(first, "@sha256:") {
		t.Fatal(first, e)
	}
	manifest = strings.Replace(manifest, "abc", "def", 1)
	second, e := resolver.Resolve(context.Background(), ref)
	if e != nil || first == second {
		t.Fatal(second, e)
	}
	bad = true
	if _, e = resolver.Resolve(context.Background(), ref); e == nil {
		t.Fatal("manifest digest mismatch accepted")
	}
}
func TestPinPreservesOtherServiceSettings(t *testing.T) {
	compose := "services:\n  web:\n    image: example/web:dev\n    environment:\n      LITERAL: '$$VALUE'\n  worker:\n    image: example/worker:prod\n"
	result, refs, e := Pin(compose, map[string]string{"web": "example/web@sha256:" + strings.Repeat("a", 64)})
	if e != nil || refs["web"] != "example/web:dev" || !strings.Contains(result, "$$VALUE") || !strings.Contains(result, "example/worker:prod") {
		t.Fatal(result, refs, e)
	}
	if _, _, e = Pin(compose, map[string]string{"missing": "x"}); e == nil {
		t.Fatal("unknown service accepted")
	}
}

func TestPrivateMultiArchitectureManifestAndRateLimit(t *testing.T) {
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:` + strings.Repeat("a", 64) + `","platform":{"os":"linux","architecture":"amd64"}},{"digest":"sha256:` + strings.Repeat("b", 64) + `","platform":{"os":"linux","architecture":"arm64"}}]}`
	limited := false
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v2/private/manifests/test" {
			t.Errorf("unexpected layer request: %s", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "ci" || password != "isolated-password" {
			w.WriteHeader(401)
			return
		}
		if limited {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Write([]byte(manifest))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	path := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("ci:isolated-password"))}}})
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	resolver := Resolver{Client: server.Client(), ConfigFile: path}
	ref, e := resolver.Resolve(context.Background(), host+"/private:test")
	sum := sha256.Sum256([]byte(manifest))
	if e != nil || ref != host+"/private@sha256:"+hex.EncodeToString(sum[:]) || calls != 1 {
		t.Fatal(ref, e, calls)
	}
	limited = true
	_, e = resolver.Resolve(context.Background(), host+"/private:test")
	var limit *RegistryError
	if !errors.As(e, &limit) || limit.RetryAfter != 2*time.Minute {
		t.Fatal(e)
	}
}
