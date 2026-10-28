// Package images resolves mutable image references into reproducible content
// addresses without pulling layers or executing registry-supplied content.
package images

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/reference"
	"gopkg.in/yaml.v3"
)

type Resolver struct {
	Client     *http.Client
	ConfigFile string
	Insecure   bool
}
type RegistryError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *RegistryError) Error() string { return fmt.Sprintf("registry returned HTTP %d", e.Status) }

var authParameter = regexp.MustCompile(`([a-z]+)="([^"]*)"`)

func (r Resolver) Resolve(ctx context.Context, image string) (string, error) {
	named, e := reference.ParseNormalizedNamed(image)
	if e != nil {
		return "", fmt.Errorf("invalid image reference")
	}
	if v, ok := named.(reference.Canonical); ok {
		return v.String(), nil
	}
	named = reference.TagNameOnly(named)
	tag := named.(reference.Tagged).Tag()
	host := reference.Domain(named)
	repo := reference.Path(named)
	server := host
	if host == "docker.io" {
		server = "registry-1.docker.io"
	}
	scheme := "https"
	if r.Insecure {
		scheme = "http"
	}
	endpoint := scheme + "://" + server + "/v2/" + repo + "/manifests/" + url.PathEscape(tag)
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	user, password, e := r.credentials(host)
	if e != nil {
		return "", e
	}
	fetch := func(token string) (*http.Response, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if user != "" {
			req.SetBasicAuth(user, password)
		}
		return client.Do(req)
	}
	response, e := fetch("")
	if e != nil {
		return "", fmt.Errorf("registry request failed: %w", e)
	}
	if response.StatusCode == http.StatusUnauthorized && strings.HasPrefix(strings.ToLower(response.Header.Get("WWW-Authenticate")), "bearer ") {
		challenge := response.Header.Get("WWW-Authenticate")
		response.Body.Close()
		parameters := map[string]string{}
		for _, m := range authParameter.FindAllStringSubmatch(challenge, -1) {
			parameters[m[1]] = m[2]
		}
		realm, e := url.Parse(parameters["realm"])
		if e != nil || realm.Host == "" || realm.User != nil || (realm.Scheme != "https" && !(r.Insecure && realm.Scheme == "http" && realm.Host == server)) {
			return "", fmt.Errorf("invalid registry authentication endpoint")
		}
		// Cross-host token services are allowed, but registry passwords are only
		// forwarded to its own host (or Docker Hub's documented token service).
		q := realm.Query()
		q.Set("service", parameters["service"])
		q.Set("scope", "repository:"+repo+":pull")
		realm.RawQuery = q.Encode()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
		if e != nil {
			return "", e
		}
		if user != "" && (realm.Host == server || (host == "docker.io" && realm.Host == "auth.docker.io")) {
			req.SetBasicAuth(user, password)
		}
		tokenResponse, e := client.Do(req)
		if e != nil {
			return "", fmt.Errorf("registry authentication request failed")
		}
		defer tokenResponse.Body.Close()
		if tokenResponse.StatusCode != 200 {
			return "", &RegistryError{Status: tokenResponse.StatusCode}
		}
		var token struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if e = json.NewDecoder(io.LimitReader(tokenResponse.Body, 1<<20)).Decode(&token); e != nil {
			return "", fmt.Errorf("invalid registry authentication response")
		}
		if token.Token == "" {
			token.Token = token.AccessToken
		}
		if token.Token == "" {
			return "", fmt.Errorf("empty registry token")
		}
		response, e = fetch(token.Token)
		if e != nil {
			return "", fmt.Errorf("registry manifest request failed")
		}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		retry := time.Duration(0)
		if seconds, err := time.ParseDuration(response.Header.Get("Retry-After") + "s"); err == nil {
			retry = seconds
		} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			retry = time.Until(at)
		}
		return "", &RegistryError{Status: response.StatusCode, RetryAfter: retry}
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if e != nil || len(body) > 4<<20 {
		return "", fmt.Errorf("registry manifest exceeds size limit")
	}
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
		Config        json.RawMessage   `json:"config"`
	}
	if json.Unmarshal(body, &manifest) != nil || manifest.SchemaVersion != 2 || (len(manifest.Manifests) == 0 && len(manifest.Config) == 0) {
		return "", fmt.Errorf("unsupported registry manifest")
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if claimed := response.Header.Get("Docker-Content-Digest"); claimed != "" && claimed != digest {
		return "", fmt.Errorf("registry manifest digest mismatch")
	}
	return host + "/" + repo + "@" + digest, nil
}
func (r Resolver) credentials(host string) (string, string, error) {
	if r.ConfigFile == "" {
		return "", "", nil
	}
	raw, e := os.ReadFile(r.ConfigFile)
	if e != nil {
		return "", "", fmt.Errorf("registry credential unavailable")
	}
	var cfg struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return "", "", fmt.Errorf("invalid registry credentials")
	}
	for key, v := range cfg.Auths {
		normalized := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(key, "https://"), "http://"), "/v1/")
		if normalized == "index.docker.io" {
			normalized = "docker.io"
		}
		if normalized != host {
			continue
		}
		if v.Auth != "" {
			decoded, e := base64.StdEncoding.DecodeString(v.Auth)
			if e != nil {
				return "", "", fmt.Errorf("invalid registry authentication")
			}
			user, password, ok := strings.Cut(string(decoded), ":")
			if !ok {
				return "", "", fmt.Errorf("invalid registry authentication")
			}
			return user, password, nil
		}
		return v.Username, v.Password, nil
	}
	return "", "", nil
}

// Pin changes only service image fields in an already resolved Compose file.
func Pin(compose string, locks map[string]string) (string, map[string]string, error) {
	var doc map[string]any
	if e := yaml.Unmarshal([]byte(compose), &doc); e != nil {
		return "", nil, e
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("Compose services are missing")
	}
	refs := map[string]string{}
	for name, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("invalid service")
		}
		ref, ok := service["image"].(string)
		if !ok {
			return "", nil, fmt.Errorf("service %s requires an image", name)
		}
		refs[name] = ref
		if lock, ok := locks[name]; ok {
			service["image"] = lock
		}
	}
	for name := range locks {
		if _, ok := services[name]; !ok {
			return "", nil, fmt.Errorf("unknown service %s", name)
		}
	}
	raw, e := yaml.Marshal(doc)
	return string(raw), refs, e
}
