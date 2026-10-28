package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

type validationCheck struct {
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	RouteID        string            `json:"route_id"`
	Host           string            `json:"host"`
	Path           string            `json:"path"`
	Method         string            `json:"method"`
	Scheme         string            `json:"scheme"`
	ExpectedStatus int               `json:"expected_status"`
	Headers        map[string]string `json:"headers"`
}
type checkResult struct {
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

func (m *GenerationManager) Validate(ctx context.Context, request *gatewayv2.ValidationRequest, sandbox bool) *gatewayv2.ValidationResult {
	result := &gatewayv2.ValidationResult{TaskId: request.TaskId}
	if request.Snapshot == nil || request.ActivateSandbox && !sandbox {
		result.Error = "validation role mismatch"
		return result
	}
	lifetime := ctx
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if e := m.waitForStartup(ctx); e != nil {
		result.Error = e.Error()
		return result
	}
	if e := m.restoreHeld(ctx); e != nil {
		result.Error = e.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	directory, e := os.MkdirTemp(m.root, ".validation-")
	if e != nil {
		result.Error = "cannot create validation directory"
		return result
	}
	held := false
	defer func() {
		if !held {
			_ = os.RemoveAll(directory)
		}
	}()
	if fixture, _ := ctx.Value(sandboxFixtureContext{}).(bool); fixture && sandbox {
		e = m.renderer.RenderSandboxFixture(request.Snapshot, directory)
	} else {
		e = m.renderer.Render(request.Snapshot, directory)
	}
	if e != nil {
		result.Error = e.Error()
		return result
	}
	if e = m.linkGenerationMainConfig(directory); e != nil {
		result.Error = e.Error()
		return result
	}
	command := exec.CommandContext(ctx, m.config.Binary, "-t", "-c", generationMainConfigName, "-p", directory)
	output, e := command.CombinedOutput()
	if e != nil {
		result.Error = "nginx -t: " + truncateOutput(output)
		return result
	}
	result.RenderedDigest, e = digestGeneration(directory)
	if e != nil {
		result.Error = e.Error()
		return result
	}
	if !request.ActivateSandbox {
		result.Success = true
		return result
	}
	previous, e := os.Readlink(filepath.Join(m.root, "current"))
	if e != nil {
		result.Error = e.Error()
		return result
	}
	if e = replaceSymlink(m.root, filepath.Base(directory)); e != nil {
		result.Error = e.Error()
		return result
	}
	// Even cancellation uses a fresh deadline to restore the previous config.
	defer func() {
		if held {
			return
		}
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		restoreErr := replaceSymlink(m.root, previous)
		if restoreErr == nil {
			restoreErr = m.reload(restoreCtx, m.state.ConfigRevision)
		}
		if restoreErr != nil {
			result.Success = false
			result.Error = "sandbox restore failed: " + restoreErr.Error()
		}
	}()
	if e = m.reload(ctx, request.Snapshot.ConfigRevision); e != nil {
		result.Error = "sandbox reload: " + e.Error()
		return result
	}
	var checks []validationCheck
	if request.ChecksJson != "" {
		if e = json.Unmarshal([]byte(request.ChecksJson), &checks); e != nil {
			result.Error = "invalid checks"
			return result
		}
	}
	results := []checkResult{}
	result.Success = true
	roots := x509.NewCertPool()
	for _, cert := range request.Snapshot.Certificates {
		roots.AppendCertsFromPEM([]byte(cert.CertificatePem))
	}
	for _, check := range checks {
		r := checkResult{Host: check.Host, Path: check.Path}
		if check.Method != "GET" && check.Method != "HEAD" || check.Scheme != "http" && check.Scheme != "https" && check.Scheme != "http3" || !strings.HasPrefix(check.Path, "/") || strings.HasPrefix(check.Path, "//") {
			r.Error = "invalid check"
			results = append(results, r)
			result.Success = false
			continue
		}
		port := "80"
		if check.Scheme == "https" {
			port = "443"
		}
		transport := &http.Transport{Proxy: nil, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: check.Host, RootCAs: roots}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		}}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		scheme := check.Scheme
		closeTransport := transport.CloseIdleConnections
		if scheme == "http3" {
			scheme = "https"
			h3 := http3Transport(transport.TLSClientConfig, "127.0.0.1:443")
			client.Transport = h3
			closeTransport = func() { _ = h3.Close() }
		}
		req, err := http.NewRequestWithContext(ctx, check.Method, scheme+"://"+check.Host+check.Path, strings.NewReader(check.Body))
		if err == nil {
			for key, value := range check.RequestHeaders {
				req.Header.Set(key, value)
			}
			var response *http.Response
			response, err = client.Do(req)
			if err == nil {
				r.Status = response.StatusCode
				r.Protocol = response.Proto
				r.Success = r.Status == check.ExpectedStatus && (check.Scheme != "http3" || response.ProtoMajor == 3)
				for key, value := range check.Headers {
					if response.Header.Get(key) != value {
						r.Success = false
					}
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
				response.Body.Close()
			}
		}
		closeTransport()
		if err != nil {
			r.Error = "request failed"
		}
		if !r.Success {
			result.Success = false
		}
		results = append(results, r)
	}
	encoded, _ := json.Marshal(results)
	result.ChecksJson = string(encoded)
	if !result.Success {
		result.Error = fmt.Sprintf("sandbox checks failed (%d checks)", len(checks))
	}
	if result.Success && request.HoldUntilUnix > time.Now().Unix() {
		until := time.Unix(request.HoldUntilUnix, 0)
		if until.After(time.Now().Add(5 * time.Minute)) {
			until = time.Now().Add(5 * time.Minute)
		}
		held = true
		m.held = &heldValidation{directory: directory, previous: previous, task: request.TaskId}
		hold := m.held
		go func() {
			timer := time.NewTimer(time.Until(until))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-lifetime.Done():
			}
			m.operationMu.Lock()
			defer m.operationMu.Unlock()
			if m.held == hold {
				restoreCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = m.restoreHeld(restoreCtx)
			}
		}()
	}
	return result
}

type heldValidation struct{ directory, previous, task string }

func (m *GenerationManager) restoreHeld(ctx context.Context) error {
	if m.held == nil {
		return nil
	}
	h := m.held
	if e := replaceSymlink(m.root, h.previous); e != nil {
		return e
	}
	if e := m.reload(ctx, m.state.ConfigRevision); e != nil {
		return e
	}
	m.held = nil
	return os.RemoveAll(h.directory)
}
func (m *GenerationManager) CancelValidation(ctx context.Context, task string) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if m.held != nil && (task == "" || m.held.task == task) {
		return m.restoreHeld(ctx)
	}
	return nil
}
