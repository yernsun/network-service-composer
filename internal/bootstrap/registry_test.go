package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRegistryCredentialsStayScopedToApprovedImageOrigins(t *testing.T) {
	password := rand.Text()
	data, _ := json.Marshal(map[string]any{"auths": map[string]any{
		"registry.example.test": map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("fixture-user:" + password))},
		"unused.example.test":   map[string]string{"identitytoken": rand.Text()},
	}})
	auths, err := RegistryAuthorizations(data, "registry.example.test/controller@sha256:fixture", "registry.example.test/gateway@sha256:fixture")
	if err != nil || len(auths) != 1 {
		t.Fatal("explicit registry scope was not retained", err)
	}
	raw, err := base64.URLEncoding.DecodeString(auths["registry.example.test"])
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	if json.Unmarshal(raw, &value) != nil || value["username"] != "fixture-user" || value["password"] != password || value["auth"] != "" || value["serveraddress"] != "registry.example.test" {
		t.Fatal("the private Docker authorization was not preserved")
	}
	for _, address := range []string{"https://index.docker.io/v1/", "docker.io"} {
		input, _ := json.Marshal(map[string]any{"auths": map[string]any{address: map[string]string{"identitytoken": password}}})
		result, err := RegistryAuthorizations(input, "namespace/controller@sha256:fixture")
		if err != nil || len(result) != 1 || result["docker.io"] == "" {
			t.Fatal("Docker Hub auth alias was not resolved", err)
		}
	}
	result, err := RegistryAuthorizations(data, "public.example.test/controller@sha256:fixture")
	if err != nil || len(result) != 0 {
		t.Fatal("credentials escaped their approved origin", err)
	}
}
func TestRegistryCredentialFilesCannotRunHelpersOrGuessInvalidCredentials(t *testing.T) {
	for _, input := range []string{
		`{`, `{}`, `{"auths":{}}`,
		`{"auths":{"registry.example.test":{"auth":"!"}}}`,
		`{"auths":{"registry.example.test":{"auth":"bm8tY29sb24="}}}`,
		`{"auths":{"registry.example.test":{"username":"fixture"}},"credsStore":"fixture-helper"}`,
		`{"auths":{"registry.example.test":{"username":"fixture"}},"credHelpers":{}}`,
		`{"auths":{"registry.example.test":{"username":"fixture"},"https://registry.example.test":{"username":"fixture"}}}`,
	} {
		if _, err := RegistryAuthorizations([]byte(input), "registry.example.test/image@sha256:fixture"); err == nil {
			t.Fatal("unsafe registry configuration accepted")
		}
	}
	for input, expected := range map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64", "unsupported": ""} {
		if ImageArchitecture(input) != expected {
			t.Fatal("unknown or mismatched platform")
		}
	}
	if ImageRegistry("index.docker.io/namespace/image") != "docker.io" || ImageRegistry("nginx") != "docker.io" || ImageRegistry("localhost:5000/image") != "localhost:5000" {
		t.Fatal("registry normalization changed its origin")
	}
}
