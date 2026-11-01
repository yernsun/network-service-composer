package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/moby/moby/api/types/swarm"
	"net/url"
	"strings"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) prepareImages(ctx context.Context) (bootstrap.Resource, error) {
	credentials := []byte(`{"auths":{}}`)
	defer func() { clear(credentials) }()
	if i.Config.RegistryAuthFile != "" {
		var err error
		credentials, err = bootstrap.ReadPrivate(i.Config.RegistryAuthFile)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(credentials, &raw) != nil || raw["credsStore"] != nil || raw["credHelpers"] != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "registry preparation accepts inline auth entries, not credential helpers"}
		}
		var document struct {
			Auths map[string]json.RawMessage `json:"auths"`
		}
		if json.Unmarshal(credentials, &document) != nil || len(document.Auths) == 0 {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "registry credentials require a private Docker auths configuration"}
		}
	}
	for _, node := range i.Config.Nodes {
		host := i.Hosts[node.Name]
		var info NodeInfo
		if err := host.Engine(ctx, "GET", "/info", nil, &info); err != nil {
			return bootstrap.Resource{}, err
		}
		architecture := map[string]string{"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[info.Architecture]
		for _, component := range []string{"controller", "gateway", "postgresql"} {
			image := i.Config.Images.Controller
			if component == "gateway" {
				image = i.Config.Images.Gateway
			}
			if component == "postgresql" {
				image = i.Config.Images.PostgreSQL
			}
			command := `set -eu; umask 077; nsc_auth_dir=$(mktemp -d); trap 'rm -f "$nsc_auth_dir/config.json"; rmdir "$nsc_auth_dir"' EXIT; cat >"$nsc_auth_dir/config.json"; ` + bootstrap.LocalDockerCLI + ` --config "$nsc_auth_dir" image pull ` + bootstrap.ShellQuote(image) + " >/dev/null"
			if _, err := host.Run(ctx, host.Privileged(command), credentials); err != nil {
				return bootstrap.Resource{}, fmt.Errorf("node %s could not pull the approved %s image", node.Name, component)
			}
			var actual struct {
				Os, Architecture string
				Config           struct {
					Labels map[string]string
					Env    []string
				}
			}
			if err := host.Engine(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, &actual); err != nil {
				return bootstrap.Resource{}, err
			}
			if component == "controller" && actual.Config.Labels["io.nsc.bootstrap.maintenance"] != "1" {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller image does not implement the independent maintenance contract"}
			}
			if component == "controller" && i.Record.Release == nil {
				version := actual.Config.Labels["org.opencontainers.image.version"]
				if version == "" {
					version = "unversioned"
				}
				i.Record.Release = &bootstrap.Release{Version: version, Images: i.Config.Images, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
			}
			if actual.Os != "linux" || actual.Architecture != architecture || architecture == "" {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "image platform is unsupported"}
			}
			if component == "postgresql" {
				if !contains(actual.Config.Env, "PG_MAJOR=18") {
					return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "bundled PostgreSQL requires the approved major 18 image layout"}
				}
			} else if actual.Config.Labels["io.nsc.bootstrap.contract"] != "1" || actual.Config.Labels["io.nsc.bootstrap.protocol"] != "2" || component == "controller" && actual.Config.Labels["io.nsc.bootstrap.schema"] != "18" {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "image bootstrap, protocol or schema compatibility is unverified"}
			}
		}
	}
	return bootstrap.Resource{Kind: "images", ID: i.Record.ID, SpecDigest: bootstrap.Digest(i.Config.Images)}, nil
}

func contains(values []string, expected string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == expected {
			return true
		}
	}
	return false
}

func (i *Installer) authorizedSpec(spec swarm.ServiceSpec) (any, error) {
	if i.Config.RegistryAuthFile == "" {
		return spec, nil
	}
	data, err := bootstrap.ReadPrivate(i.Config.RegistryAuthFile)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("registry configuration is invalid")
	}
	image := spec.TaskTemplate.ContainerSpec.Image
	repository, _, _ := strings.Cut(image, "@")
	parts := strings.SplitN(repository, "/", 2)
	server := parts[0]
	if len(parts) == 1 || !strings.ContainsAny(server, ".:") && server != "localhost" {
		server = "https://index.docker.io/v1/"
	}
	value, ok := config.Auths[server]
	if !ok {
		return spec, nil // Public registries remain anonymous; preparation already verified access.
	}
	var auth map[string]string
	if json.Unmarshal(value, &auth) != nil {
		return nil, fmt.Errorf("registry authentication entry is invalid")
	}
	if encoded := auth["auth"]; encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("registry authentication encoding is invalid")
		}
		user, password, ok := strings.Cut(string(decoded), ":")
		clear(decoded)
		if !ok {
			return nil, fmt.Errorf("registry authentication requires a username and password")
		}
		auth["username"], auth["password"] = user, password
		delete(auth, "auth")
	}
	auth["serveraddress"] = server
	encoded, _ := json.Marshal(auth)
	return bootstrap.RegistryRequest{Body: spec, Authorization: base64.URLEncoding.EncodeToString(encoded)}, nil
}
