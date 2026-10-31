package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (i *Installer) Verify(ctx context.Context, output io.Writer) error {
	for key, receipt := range i.Record.Resources {
		if err := i.Check(ctx, bootstrap.Step{Key: key, State: bootstrap.Succeeded, Receipt: &receipt}); err != nil {
			return fmt.Errorf("installation resource %s has drift: %w", key, err)
		}
	}
	if err := i.verifyHandoff(ctx); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{"installation_id": i.Record.ID, "state": "VERIFIED", "observed_at": timeNow(), "database_single_point_of_failure": true})
}

func (i *Installer) apiClient() (*http.Client, error) {
	data, err := os.ReadFile(i.material("console.crt"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("console trust certificate is invalid")
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Proxy: nil}, Timeout: 10 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (i *Installer) consoleURL() string {
	for _, n := range i.Config.Nodes {
		if n.Name == i.Config.MaintenanceNode {
			return "https://" + net.JoinHostPort(n.Address, strconv.Itoa(i.Config.ConsolePort))
		}
	}
	return ""
}

func (i *Installer) api(ctx context.Context, method, path string, input, output any, revision *int64) error {
	client, err := i.apiClient()
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, i.consoleURL()+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	password, err := i.apiCredential()
	if err != nil {
		return err
	}
	defer clear(password)
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(password)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cache-Control", "no-cache")
	if revision != nil {
		request.Header.Set("If-Match", fmt.Sprintf(`"rev-%d"`, *revision))
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("verified console request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("console %s returned HTTP %d", path, response.StatusCode)
	}
	if output == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Data, output)
}

func (i *Installer) verifyController(ctx context.Context) error {
	return wait(ctx, func() error {
		var overview core.HAOverview
		if err := i.api(ctx, "GET", "/system/ha", nil, &overview, nil); err != nil {
			return err
		}
		controller, quorum := false, false
		for _, layer := range overview.Layers {
			if layer.Name == "controller" {
				controller = layer.State == "READY" && len(layer.Evidence) == len(i.controllerNodes())
			}
			if layer.Name == "swarm_quorum" {
				quorum = layer.State == "READY"
			}
		}
		if !controller || !quorum {
			return fmt.Errorf("controller election or Manager evidence is not ready")
		}
		keys := map[string]bool{}
		after := ""
		for {
			var applications []model.Application
			if err := i.api(ctx, "GET", "/applications?limit=200&runtime_id="+url.QueryEscape(i.Record.RuntimeID)+"&after="+url.QueryEscape(after), nil, &applications, nil); err != nil {
				return err
			}
			for _, app := range applications {
				if app.RuntimeID == i.Record.RuntimeID && app.System && !app.Managed {
					expected := i.seedRecord().Components[app.SystemKey]
					if expected.ID == "" {
						continue
					}
					var services []model.Service
					if err := i.api(ctx, "GET", "/services?limit=200&application_id="+url.QueryEscape(app.ID), nil, &services, nil); err != nil {
						return err
					}
					for _, service := range services {
						if service.RuntimeID == i.Record.RuntimeID && service.ApplicationID == app.ID && service.RuntimeObjectID == expected.ID && service.System && !service.Managed {
							keys[app.SystemKey] = true
						}
					}
				}
			}
			if len(applications) < 200 || len(keys) == 4 {
				break
			}
			next := applications[len(applications)-1].ID
			if next == "" || next == after {
				return fmt.Errorf("application pagination did not advance")
			}
			after = next
		}
		for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
			if !keys[key] {
				return fmt.Errorf("system application registration is incomplete")
			}
		}
		return nil
	})
}

func (i *Installer) controllerNodes() []bootstrap.Node {
	nodes := []bootstrap.Node{}
	for _, n := range i.Config.Nodes {
		if n.Manager {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func (i *Installer) verifyHandoff(ctx context.Context) error {
	if err := i.verifyController(ctx); err != nil {
		return err
	}
	if err := i.verifyLogin(ctx); err != nil {
		return err
	}
	if err := i.verifyGateways(ctx); err != nil {
		return err
	}

	client, err := i.apiClient()
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	for _, n := range i.controllerNodes() {
		request, err := http.NewRequestWithContext(ctx, "GET", "https://"+net.JoinHostPort(n.Address, strconv.Itoa(i.Config.ConsolePort))+"/login", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("console HTTPS access failed")
		}
		probe := i.GatewayClient
		if probe == nil {
			probe = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		}
		request, err = http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort(n.Address, "80")+"/", nil)
		if err != nil {
			return err
		}
		response, err = probe.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		probe.CloseIdleConnections()
		if response.StatusCode != 404 || !strings.Contains(strings.ToLower(response.Header.Get("Server")), "nginx") {
			return fmt.Errorf("initial Gateway HTTP access did not reach NGINX")
		}
	}
	return nil
}

func (i *Installer) verifyLogin(ctx context.Context) error {
	client, err := i.apiClient()
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	password, err := i.apiCredential()
	if err != nil {
		return err
	}
	defer clear(password)
	data, _ := json.Marshal(map[string]string{"username": "admin", "password": strings.TrimSpace(string(password))})
	request, err := http.NewRequestWithContext(ctx, "POST", i.consoleURL()+"/api/v1/auth/login", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("administrator login verification failed")
	}
	var result struct {
		Data struct {
			Authenticated bool
			Role          string
		}
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return err
	}
	if !result.Data.Authenticated || result.Data.Role != "administrator" {
		return fmt.Errorf("administrator session was not established")
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "nsc_session" && cookie.Secure && cookie.HttpOnly {
			return nil
		}
	}
	return fmt.Errorf("console session cookie is not protected for HTTPS")
}

func (i *Installer) Handoff(output io.Writer) error {
	data, err := os.ReadFile(i.material("console.crt"))
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("console certificate is invalid")
	}
	fingerprint := sha256.Sum256(block.Bytes)
	return json.NewEncoder(output).Encode(map[string]any{"installation_id": i.Record.ID, "console_urls": i.seedRecord().ConsoleURLs, "certificate_sha256": hex.EncodeToString(fingerprint[:]), "recovery_directory": i.Directory, "administrator_credential_file": filepath.Join(i.materialDir(), "admin-password"), "database_single_point_of_failure": true, "limits": []string{"Controller/Gateway redundancy does not provide PostgreSQL failover.", "Database backups and business volume recovery are separate."}})
}

func (i *Installer) createValidation(ctx context.Context) (bootstrap.Resource, error) {
	base := "/gateway-pools/" + i.Record.PoolID + "/nginx"
	var draft struct {
		Revision int64 `json:"revision"`
	}
	if err := i.api(ctx, "GET", base+"/draft", nil, &draft, nil); err != nil {
		return bootstrap.Resource{}, err
	}
	var validation struct {
		ID string `json:"id"`
	}
	if err := i.api(ctx, "POST", base+"/validations", map[string]string{}, &validation, &draft.Revision); err != nil {
		return bootstrap.Resource{}, err
	}
	return bootstrap.Resource{Kind: "sandbox-validation", ID: validation.ID, SpecDigest: i.validationDigest(draft.Revision)}, nil
}

func (i *Installer) verifyValidation(ctx context.Context) error {
	id := i.Record.Resources["validation:bootstrap"].ID
	if id == "" {
		return fmt.Errorf("Sandbox validation receipt is missing")
	}
	return wait(ctx, func() error {
		var validation core.NginxValidation
		if err := i.api(ctx, "GET", "/gateway-pools/"+i.Record.PoolID+"/nginx/validations/"+url.PathEscape(id), nil, &validation, nil); err != nil {
			return err
		}
		if validation.ID != id || validation.PoolID != i.Record.PoolID || validation.SandboxID != i.Record.Nodes[i.Config.DataNode].SandboxID || validation.RendererVersion != nginxx.RendererVersion || i.validationDigest(validation.DraftRevision) != i.Record.Resources["validation:bootstrap"].SpecDigest {
			return &bootstrap.Rejected{Reason: "Sandbox validation source or identity differs from its recorded receipt"}
		}
		if validation.Status != "SUCCEEDED" {
			if validation.Status == "FAILED" || validation.Status == "EXPIRED" || validation.Status == "CANCELLED" {
				return &bootstrap.Rejected{Reason: "Sandbox validation finished without success"}
			}
			return fmt.Errorf("Sandbox NGINX validation is not successful")
		}
		result, ok := validation.Results[validation.SandboxID]
		if !ok || result.Status != "SUCCEEDED" || result.RenderedDigest == "" || !timeNow().Before(validation.ExpiresAt) {
			return &bootstrap.Rejected{Reason: "Sandbox NGINX result is missing, expired or unconfirmed"}
		}
		var draft struct {
			Revision int64 `json:"revision"`
		}
		if err := i.api(ctx, "GET", "/gateway-pools/"+i.Record.PoolID+"/nginx/draft", nil, &draft, nil); err != nil {
			return err
		}
		if draft.Revision != validation.DraftRevision {
			return &bootstrap.Rejected{Reason: "NGINX draft changed after the recorded validation"}
		}
		return nil
	})
}

func (i *Installer) validationDigest(revision int64) string {
	return bootstrap.Digest(map[string]any{"pool_id": i.Record.PoolID, "draft_revision": revision, "sandbox_id": i.Record.Nodes[i.Config.DataNode].SandboxID, "renderer_version": nginxx.RendererVersion})
}

func fresh(at time.Time) bool {
	age := time.Since(at)
	return !at.IsZero() && age >= -5*time.Second && age < 15*time.Second
}

func (i *Installer) verifyGateways(ctx context.Context) error {
	if err := wait(ctx, func() error {
		for _, n := range i.controllerNodes() {
			var gateway model.Gateway
			if err := i.api(ctx, "GET", "/gateways/"+i.Record.Nodes[n.Name].GatewayID, nil, &gateway, nil); err != nil {
				return err
			}
			if gateway.ID != i.Record.Nodes[n.Name].GatewayID || gateway.PoolID != i.Record.PoolID || gateway.Status != model.GatewayReady || gateway.DesiredRevision <= 0 || gateway.AppliedRevision < gateway.DesiredRevision || gateway.LastSeenAt == nil || !fresh(*gateway.LastSeenAt) {
				return fmt.Errorf("Gateway has no fresh positive loaded revision")
			}
			var evidence model.GatewayEvidence
			if err := i.api(ctx, "GET", "/gateways/"+gateway.ID+"/evidence", nil, &evidence, nil); err != nil {
				return err
			}
			if !evidence.LoadingConfirmed || evidence.ConfigurationState != "CONFIRMED" || evidence.StartupState != "CONFIRMED" || !fresh(evidence.ObservedAt) || !fresh(evidence.ReceivedAt) || evidence.ConfirmedRevision != gateway.AppliedRevision || evidence.ModelDigest == "" || evidence.ModelDigest != gateway.ModelDigest || evidence.RenderedDigest == "" || evidence.RenderedDigest != gateway.RenderedDigest {
				return fmt.Errorf("Gateway loading evidence is unconfirmed")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return wait(ctx, func() error {
		return i.withStore(ctx, true, func(store storage.Store, _ config.Config) error {
			gateway := i.Record.Nodes[i.Config.DataNode].SandboxID
			status, err := store.GetControlDocument(ctx, gateway+":sandbox-status")
			if err != nil {
				return err
			}
			if status.Kind != "sandbox-status" || status.ParentID != i.Record.PoolID || !fresh(status.UpdatedAt) {
				return fmt.Errorf("dedicated Sandbox heartbeat is not fresh")
			}
			return nil
		})
	})
}

// A rotated operator credential is transient input. It never changes the frozen
// installation configuration or the original offline recovery material.
func (i *Installer) apiCredential() ([]byte, error) {
	path := i.CredentialFile
	if path == "" {
		path = i.material("admin-password")
	}
	return bootstrap.ReadPrivate(path)
}
