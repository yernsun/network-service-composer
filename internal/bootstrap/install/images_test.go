package install

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/moby/moby/api/types/swarm"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type imageHost struct {
	Engine
	component              string
	platform               string
	schema                 string
	maintenance            string
	os, contract, protocol string
	seen                   [][]byte
	commands               []string
}

func (h *imageHost) Run(_ context.Context, command string, input []byte) ([]byte, error) {
	h.seen = append(h.seen, append([]byte(nil), input...))
	h.commands = append(h.commands, command)
	if strings.Contains(command, "must-not-be-argument") {
		panic("registry credential leaked to command")
	}
	return nil, nil
}
func (h *imageHost) EngineCall(ctx context.Context, method, path string, input, out any) error {
	return h.Engine.Engine(ctx, method, path, input, out)
}

// Override the complete Engine interface without exposing arbitrary Docker input.
type preparedHost struct {
	base  Engine
	state *imageHost
}

func (h preparedHost) Privileged(s string) string { return h.base.Privileged(s) }
func (h preparedHost) Run(c context.Context, s string, b []byte) ([]byte, error) {
	return h.state.Run(c, s, b)
}
func (h preparedHost) Stream(c context.Context, s string) (net.Conn, error) {
	return h.base.Stream(c, s)
}
func (h preparedHost) Engine(c context.Context, m, p string, in, out any) error {
	if !strings.HasPrefix(p, "/images/") {
		return h.base.Engine(c, m, p, in, out)
	}
	osType, contract, protocol := h.state.os, h.state.contract, h.state.protocol
	if osType == "" {
		osType = "linux"
	}
	if contract == "" {
		contract = "1"
	}
	if protocol == "" {
		protocol = "2"
	}
	maintenance := h.state.maintenance
	if maintenance == "" {
		maintenance = "1"
	}
	return encode(map[string]any{"Os": osType, "Architecture": h.state.platform, "Config": map[string]any{"Labels": map[string]string{"io.nsc.bootstrap.contract": contract, "io.nsc.bootstrap.protocol": protocol, "io.nsc.bootstrap.maintenance": maintenance, "io.nsc.bootstrap.schema": h.state.schema}, "Env": []string{h.state.component}}}, out)
}
func TestImagePreparationChecksPinnedContractAndProtectsRegistryCredentials(t *testing.T) {
	i, _ := installerFixture(t, 1)
	state := &imageHost{Engine: i.Hosts["one"], platform: "amd64", schema: "18", component: "PG_MAJOR=18"}
	i.Hosts["one"] = preparedHost{base: state.Engine, state: state}
	i.Config.RegistryAuthFile = filepath.Join(t.TempDir(), "auth.json")
	secret, _ := json.Marshal(map[string]any{"auths": map[string]any{"registry.example.test": map[string]string{"auth": "must-not-be-argument"}}})
	os.WriteFile(i.Config.RegistryAuthFile, secret, 0600)
	if _, err := i.Apply(t.Context(), bootstrap.Step{Key: "images"}); err != nil {
		t.Fatal(err)
	}
	if len(state.seen) != 3 || string(state.seen[0]) != string(secret) {
		t.Fatal("registry reference not transported through stdin")
	}
	for _, failure := range []string{"platform", "schema", "postgres"} {
		state.platform = "amd64"
		state.schema = "18"
		state.component = "PG_MAJOR=18"
		switch failure {
		case "platform":
			state.platform = "unsupported"
		case "schema":
			state.schema = "19"
		case "postgres":
			state.component = "PG_MAJOR=19"
		}
		if _, err := i.prepareImages(t.Context()); err == nil {
			t.Fatal("unverified target image accepted", failure)
		}
	}
	os.WriteFile(i.Config.RegistryAuthFile, []byte(`{"auths":{}}`), 0600)
	if _, err := i.prepareImages(t.Context()); err == nil {
		t.Fatal("invalid registry authority accepted")
	}
	os.Remove(i.Config.RegistryAuthFile)
	if _, err := i.prepareImages(t.Context()); err == nil {
		t.Fatal("missing credential ignored")
	}
	if receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "images", State: bootstrap.Unknown}); err == nil || receipt != nil {
		t.Fatal("image pull interruption cannot be retried safely")
	}
}

func TestServiceRegistryAuthIsTransportOnlyAndPublicImagesRemainAnonymous(t *testing.T) {
	i, _ := installerFixture(t, 1)
	i.Config.RegistryAuthFile = filepath.Join(t.TempDir(), "auth.json")
	cases := []struct {
		name, data        string
		valid, authorized bool
	}{
		{"basic", `{"auths":{"registry.example.test":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password")) + `"}}}`, true, true},
		{"token", `{"auths":{"registry.example.test":{"identitytoken":"fixture-token"}}}`, true, true},
		{"public", `{"auths":{"other.example.test":{"identitytoken":"fixture-token"}}}`, true, false},
		{"invalid-json", `{`, false, false},
		{"invalid-entry", `{"auths":{"registry.example.test":42}}`, false, false},
		{"invalid-encoding", `{"auths":{"registry.example.test":{"auth":"%%%"}}}`, false, false},
		{"invalid-identity", `{"auths":{"registry.example.test":{"auth":"eA=="}}}`, false, false},
	}
	spec := swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: i.Config.Images.Controller}}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.WriteFile(i.Config.RegistryAuthFile, []byte(c.data), 0600)
			actual, err := i.authorizedSpec(spec)
			if !c.valid {
				if err == nil {
					t.Fatal("invalid credentials accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			private, ok := actual.(bootstrap.RegistryRequest)
			if ok != c.authorized {
				t.Fatal("incorrect registry authorization selection")
			}
			if ok {
				decoded, err := base64.URLEncoding.DecodeString(private.Authorization)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(decoded, []byte("fixture-")) {
					t.Fatal("private registry credential lost")
				}
				public, _ := json.Marshal(private.Body)
				if bytes.Contains(public, []byte("fixture-password")) || bytes.Contains(public, []byte("fixture-token")) {
					t.Fatal("credential entered the service spec")
				}
			}
		})
	}
	os.Remove(i.Config.RegistryAuthFile)
	if _, err := i.authorizedSpec(spec); err == nil {
		t.Fatal("missing private registry reference ignored")
	}
}

func TestImageCompatibilityRejectsEveryUnsupportedBoundaryBeforeIdentityCreation(t *testing.T) {
	i, _ := installerFixture(t, 1)
	state := &imageHost{Engine: i.Hosts["one"], platform: "amd64", schema: "18", component: "PG_MAJOR=18"}
	i.Hosts["one"] = preparedHost{base: state.Engine, state: state}
	for _, field := range []string{"os", "contract", "protocol", "maintenance", "architecture"} {
		state.os = "linux"
		state.contract = "1"
		state.protocol = "2"
		state.platform = "amd64"
		state.maintenance = "1"
		switch field {
		case "os":
			state.os = "windows"
		case "contract":
			state.contract = "2"
		case "protocol":
			state.protocol = "3"
		case "maintenance":
			state.maintenance = "0"
		case "architecture":
			state.platform = "arm64"
		}
		if _, err := i.prepareImages(t.Context()); err == nil {
			t.Fatal("unverified image compatibility accepted", field)
		}
		if _, err := os.Stat(i.materialDir()); !os.IsNotExist(err) {
			t.Fatal("incompatible image generated identities")
		}
	}
	i.Config.RegistryAuthFile = filepath.Join(t.TempDir(), "registry.json")
	for _, document := range []string{`{`, `{"auths":{},"credsStore":"external-helper"}`, `{"auths":{},"credHelpers":{"registry.example.test":"external-helper"}}`, `{"auths":42}`} {
		os.WriteFile(i.Config.RegistryAuthFile, []byte(document), 0600)
		if _, err := i.prepareImages(t.Context()); err == nil {
			t.Fatal("unsupported registry credential source accepted")
		}
	}
}

func TestDockerHubAndLocalRegistryAuthResolveTheRepositoryBeforeItsDigest(t *testing.T) {
	i, _ := installerFixture(t, 1)
	i.Config.RegistryAuthFile = filepath.Join(t.TempDir(), "registry.json")
	os.WriteFile(i.Config.RegistryAuthFile, []byte(`{"auths":{"https://index.docker.io/v1/":{"identitytoken":"fixture-hub-token"},"localhost":{"identitytoken":"fixture-local-token"}}}`), 0600)
	for _, repository := range []string{"postgres", "library/postgres", "localhost/nsc"} {
		spec := swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: repository + "@sha256:" + strings.Repeat("a", 64)}}}
		actual, err := i.authorizedSpec(spec)
		if err != nil {
			t.Fatal(err)
		}
		private, ok := actual.(bootstrap.RegistryRequest)
		if !ok || private.Authorization == "" {
			t.Fatal("digest was confused with the registry address", repository)
		}
	}
}

func TestImageTransportFailuresDoNotCreateInstallationIdentities(t *testing.T) {
	for _, scenario := range []string{"pull", "inspect"} {
		i, c := installerFixture(t, 1)
		if scenario == "pull" {
			i.Hosts["one"].(*fakeHost).runErr = context.DeadlineExceeded
		} else {
			c.fail = "GET /images/" + url.PathEscape(i.Config.Images.Controller) + "/json"
		}
		if _, err := i.prepareImages(t.Context()); err == nil {
			t.Fatal("unverified image transport accepted", scenario)
		}
		if _, err := os.Stat(i.materialDir()); !os.IsNotExist(err) {
			t.Fatal("image failure generated credentials")
		}
	}
}

func TestAnonymousPreparationCannotUseImplicitDockerContextsOrCredentials(t *testing.T) {
	installer, _ := installerFixture(t, 1)
	state := &imageHost{Engine: installer.Hosts["one"], platform: "amd64", schema: "18", component: "PG_MAJOR=18"}
	installer.Hosts["one"] = preparedHost{base: state.Engine, state: state}
	if _, err := installer.prepareImages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(state.seen) != 3 {
		t.Fatal("not all pinned images were prepared")
	}
	for n, command := range state.commands {
		if !strings.Contains(command, bootstrap.LocalDockerCLI) || !strings.Contains(command, `--config "$nsc_auth_dir"`) || string(state.seen[n]) != `{"auths":{}}` {
			t.Fatal("anonymous preparation inherited a Docker context or credentials")
		}
	}
}
