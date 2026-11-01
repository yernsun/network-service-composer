package bootstrap

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ImageRegistry identifies the registry of an immutable image. It does not
// resolve credentials, run helpers or contact an arbitrary endpoint.
func ImageRegistry(image string) string {
	repository, _, _ := strings.Cut(image, "@")
	first, _, found := strings.Cut(repository, "/")
	if !found || !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	if first == "index.docker.io" {
		return "docker.io"
	}
	return strings.ToLower(first)
}

// RegistryAuthorizations selects only explicit auth entries for approved images.
// The result belongs in a protected Secret or request header, never a journal.
func RegistryAuthorizations(data []byte, images ...string) (map[string]string, error) {
	var document struct {
		Auths   map[string]map[string]string `json:"auths"`
		Store   json.RawMessage              `json:"credsStore"`
		Helpers json.RawMessage              `json:"credHelpers"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Auths) == 0 || document.Store != nil || document.Helpers != nil {
		return nil, fmt.Errorf("registry preparation requires explicit private auth entries without credential helpers")
	}
	result := map[string]string{}
	for _, image := range images {
		registry := ImageRegistry(image)
		var selected map[string]string
		server := ""
		for address, auth := range document.Auths {
			host := strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
			host, _, _ = strings.Cut(host, "/")
			if ImageRegistry(strings.ToLower(host)+"/image") != registry {
				continue
			}
			if selected != nil {
				return nil, fmt.Errorf("registry authentication has ambiguous entries")
			}
			selected, server = auth, address
		}
		if selected == nil {
			continue
		}
		auth := map[string]string{}
		for key, value := range selected {
			auth[key] = value
		}
		if encoded := auth["auth"]; encoded != "" {
			value, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, fmt.Errorf("registry authentication encoding is invalid")
			}
			user, password, valid := strings.Cut(string(value), ":")
			clear(value)
			if !valid {
				return nil, fmt.Errorf("registry authentication requires a username and password")
			}
			auth["username"], auth["password"] = user, password
			delete(auth, "auth")
		}
		auth["serveraddress"] = server
		encoded, _ := json.Marshal(auth)
		result[registry] = base64.URLEncoding.EncodeToString(encoded)
		clear(encoded)
	}
	return result, nil
}

func ImageArchitecture(value string) string {
	switch value {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return ""
	}
}
