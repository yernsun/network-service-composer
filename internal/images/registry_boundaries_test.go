package images

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type registryTransportFunc func(*http.Request) (*http.Response, error)

func (f registryTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type registryBody struct {
	io.Reader
	closed bool
}

func (b *registryBody) Close() error { b.closed = true; return nil }

type failedManifestReader struct{}

func (failedManifestReader) Read([]byte) (int, error) { return 0, errors.New("manifest interrupted") }
func registryCredentialFixture(t *testing.T, raw string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "credentials.json")
	if e := os.WriteFile(p, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestRegistryBearerExchangeProtectsCredentialsAndClosesBodies(t *testing.T) {
	for _, stage := range []string{"same host", "cross host", "Docker Hub", "access token", "no credentials", "insecure same host", "bad realm", "missing host", "userinfo", "HTTP disallowed", "insecure foreign HTTP", "token transport", "token status", "token invalid", "token empty", "manifest transport"} {
		t.Run(stage, func(t *testing.T) {
			host, realm := "registry.example.test", "https://registry.example.test/token"
			withCredentials := stage != "no credentials"
			switch stage {
			case "cross host":
				realm = "https://tokens.example.test/token"
			case "Docker Hub":
				host = "docker.io"
				realm = "https://auth.docker.io/token"
			case "insecure same host":
				realm = "http://registry.example.test/token"
			case "bad realm":
				realm = "https://%"
			case "missing host":
				realm = "/token"
			case "userinfo":
				realm = "https://user@registry.example.test/token"
			case "HTTP disallowed":
				realm = "http://registry.example.test/token"
			case "insecure foreign HTTP":
				realm = "http://elsewhere.example.test/token"
			}
			r := Resolver{Insecure: stage == "insecure same host" || stage == "insecure foreign HTTP"}
			if withCredentials {
				r.ConfigFile = registryCredentialFixture(t, `{"auths":{"`+host+`":{"username":"ci","password":"private-password"}}}`)
			}
			calls := 0
			bodies := []*registryBody{}
			manifest := `{"schemaVersion":2,"config":{"digest":"fixture"}}`
			r.Client = &http.Client{Transport: registryTransportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				status, body := 200, ""
				headers := http.Header{}
				switch calls {
				case 1:
					if req.URL.Scheme == "https" && r.Insecure {
						t.Error("explicit insecure registry ignored")
					}
					u, p, ok := req.BasicAuth()
					if ok != withCredentials || ok && (u != "ci" || p != "private-password") {
						t.Error("registry basic auth lost")
					}
					status = 401
					headers.Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="registry"`)
				case 2:
					if stage == "token transport" {
						return nil, errors.New("token offline")
					}
					_, _, auth := req.BasicAuth()
					want := withCredentials && stage != "cross host"
					if auth != want {
						t.Error("registry credential crossed token host boundary")
					}
					if req.URL.Query().Get("scope") != "repository:team/demo:pull" || req.URL.Query().Get("service") != "registry" {
						t.Error("token request lost repository scope")
					}
					body = `{"token":"fixture-token"}`
					switch stage {
					case "access token":
						body = `{"access_token":"fixture-token"}`
					case "token status":
						status = 403
					case "token invalid":
						body = "{"
					case "token empty":
						body = `{}`
					}
				case 3:
					if req.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("token not used for manifest")
					}
					if stage == "manifest transport" {
						return nil, errors.New("manifest offline")
					}
					body = manifest
				default:
					t.Fatal("unbounded registry authentication requests")
				}
				b := &registryBody{Reader: strings.NewReader(body)}
				bodies = append(bodies, b)
				return &http.Response{StatusCode: status, Header: headers, Body: b, Request: req}, nil
			})}
			got, e := r.Resolve(context.Background(), host+"/team/demo:stable")
			success := stage == "same host" || stage == "cross host" || stage == "Docker Hub" || stage == "access token" || stage == "no credentials" || stage == "insecure same host"
			if success {
				sum := sha256.Sum256([]byte(manifest))
				if e != nil || got != host+"/team/demo@sha256:"+hex.EncodeToString(sum[:]) || calls != 3 {
					t.Fatalf("authenticated resolution=%s %v calls=%d", got, e, calls)
				}
			} else if e == nil || got != "" {
				t.Fatal("invalid authentication accepted")
			}
			for _, b := range bodies {
				if !b.closed {
					t.Fatal("registry response body leaked")
				}
			}
			if e != nil && strings.Contains(e.Error(), "private-password") {
				t.Fatal("credential exposed in error")
			}
		})
	}
}

func TestRegistryManifestFailureAndRetryEvidence(t *testing.T) {
	for _, stage := range []string{"transport", "unauthorized basic", "retry seconds", "retry date", "retry invalid", "read failure", "oversize", "invalid JSON", "old schema", "missing descriptors", "valid index", "digest mismatch"} {
		t.Run(stage, func(t *testing.T) {
			b := &registryBody{Reader: strings.NewReader(`{"schemaVersion":2,"config":{}}`)}
			header := http.Header{}
			status := 200
			switch stage {
			case "unauthorized basic":
				status = 401
				header.Set("WWW-Authenticate", `Basic realm="registry"`)
			case "retry seconds":
				status = 429
				header.Set("Retry-After", "120")
			case "retry date":
				status = 503
				header.Set("Retry-After", time.Now().Add(2*time.Minute).UTC().Format(http.TimeFormat))
			case "retry invalid":
				status = 503
				header.Set("Retry-After", "later")
			case "read failure":
				b.Reader = failedManifestReader{}
			case "oversize":
				b.Reader = strings.NewReader(strings.Repeat("x", (4<<20)+1))
			case "invalid JSON":
				b.Reader = strings.NewReader("{")
			case "old schema":
				b.Reader = strings.NewReader(`{"schemaVersion":1,"config":{}}`)
			case "missing descriptors":
				b.Reader = strings.NewReader(`{"schemaVersion":2}`)
			case "valid index":
				b.Reader = strings.NewReader(`{"schemaVersion":2,"manifests":[{}]}`)
			case "digest mismatch":
				header.Set("Docker-Content-Digest", "sha256:wrong")
			}
			r := Resolver{Client: &http.Client{Transport: registryTransportFunc(func(req *http.Request) (*http.Response, error) {
				if stage == "transport" {
					return nil, errors.New("offline")
				}
				return &http.Response{StatusCode: status, Header: header, Body: b, Request: req}, nil
			})}}
			got, e := r.Resolve(context.Background(), "registry.example.test/demo:stable")
			if stage == "valid index" {
				if e != nil || !strings.Contains(got, "@sha256:") {
					t.Fatal(got, e)
				}
			} else if e == nil || got != "" {
				t.Fatal("invalid manifest accepted")
			}
			if stage != "transport" && !b.closed {
				t.Fatal("failed manifest leaked body")
			}
			if strings.HasPrefix(stage, "retry") {
				var re *RegistryError
				if !errors.As(e, &re) || !strings.Contains(re.Error(), "HTTP") {
					t.Fatal("retry status lost", e)
				}
				if stage == "retry seconds" && re.RetryAfter != 2*time.Minute || stage == "retry date" && (re.RetryAfter < time.Minute || re.RetryAfter > 2*time.Minute) || stage == "retry invalid" && re.RetryAfter != 0 {
					t.Fatalf("retry evidence=%v", re)
				}
			}
		})
	}
}

func TestRegistryCredentialFormatsAndComposeShapeFailures(t *testing.T) {
	for _, tc := range []struct {
		raw, host, user, password string
		bad                       bool
	}{
		{`{"auths":{"https://index.docker.io/v1/":{"username":"ci","password":"pw"}}}`, "docker.io", "ci", "pw", false},
		{`{"auths":{"http://registry.example.test/v1/":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("ci:pw:rest")) + `"}}}`, "registry.example.test", "ci", "pw:rest", false},
		{`{"auths":{"other":{"username":"other"}}}`, "registry.example.test", "", "", false},
		{`{"auths":{}}`, "registry.example.test", "", "", false},
		{`{`, "registry.example.test", "", "", true},
		{`{"auths":{"registry.example.test":{"auth":"!!!"}}}`, "registry.example.test", "", "", true},
		{`{"auths":{"registry.example.test":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("no-colon")) + `"}}}`, "registry.example.test", "", "", true},
	} {
		r := Resolver{ConfigFile: registryCredentialFixture(t, tc.raw)}
		u, p, e := r.credentials(tc.host)
		if (e != nil) != tc.bad || u != tc.user || p != tc.password {
			t.Fatalf("credential parsing mismatch: user=%q error=%v", u, e)
		}
	}
	r := Resolver{ConfigFile: filepath.Join(t.TempDir(), "missing")}
	if _, _, e := r.credentials("host"); e == nil {
		t.Fatal("missing credentials ignored")
	}
	if _, e := r.Resolve(context.Background(), "example.test/image:tag"); e == nil {
		t.Fatal("missing registry credentials ignored by resolve")
	}
	for _, raw := range []string{"[", "name: app", "services: {web: scalar}", "services: {web: {image: 123}}"} {
		if value, refs, e := Pin(raw, nil); e == nil || value != "" || refs != nil {
			t.Fatal("invalid Compose image definition accepted")
		}
	}
}
