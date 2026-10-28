package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"google.golang.org/protobuf/proto"
)

func TestSandboxOrchestrationKeepsFixtureAndCandidateResultsSeparate(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	for _, mode := range []string{"fixture failed", "invalid accepted", "restore failed", "candidate failed", "complete", "HTTP3 complete"} {
		t.Run(mode, func(t *testing.T) {
			command := "exit 0"
			if mode == "HTTP3 complete" {
				command = "echo 'nginx build --with-http_v3_module'"
			}
			m := validationManagerFixture(t, command)
			request := &gatewayv2.ValidationRequest{TaskId: "real-task", Snapshot: validationSnapshot(), ChecksJson: `[{"path":"/actual"}]`}
			original := proto.Clone(request)
			calls := 0
			var fixture *gatewayv2.Snapshot
			fixtureURL := ""
			result := m.validateSandbox(context.Background(), request, func(ctx context.Context, r *gatewayv2.ValidationRequest, sandbox bool) *gatewayv2.ValidationResult {
				calls++
				if !sandbox || r.TaskId != request.TaskId {
					t.Fatal("fixture lost sandbox authority or task identity")
				}
				answer := &gatewayv2.ValidationResult{TaskId: r.TaskId, Success: true, RenderedDigest: strings.Repeat("a", 64), ChecksJson: `[{"success":true}]`}
				switch calls {
				case 1:
					fixture = proto.Clone(r.Snapshot).(*gatewayv2.Snapshot)
					if !r.ActivateSandbox || r.Snapshot.PoolId != request.Snapshot.PoolId || len(r.Snapshot.Routes) != 3 || len(r.Snapshot.Certificates) != 1 || r.Snapshot.ConfigRevision == request.Snapshot.ConfigRevision {
						t.Fatal("fixture identity leaked into requested candidate")
					}
					h3 := mode == "HTTP3 complete"
					if r.Snapshot.Domains[0].Http3Enabled != h3 {
						t.Fatal("fixture misstated HTTP3 capability")
					}
					var checks []validationCheck
					if e := json.Unmarshal([]byte(r.ChecksJson), &checks); e != nil {
						t.Fatal(e)
					}
					want := 6
					if h3 {
						want++
					}
					if len(checks) != want {
						t.Fatal("self-test checks omitted", len(checks))
					}
					certificate := r.Snapshot.Certificates[0]
					pair, e := tls.X509KeyPair([]byte(certificate.CertificatePem), []byte(certificate.PrivateKeyPem))
					if e != nil {
						t.Fatal(e)
					}
					cert, e := x509.ParseCertificate(pair.Certificate[0])
					if e != nil {
						t.Fatal(e)
					}
					if e = cert.VerifyHostname("sandbox.nsc.test"); e != nil {
						t.Fatal(e)
					}
					if !cert.NotAfter.After(time.Now()) || cert.NotAfter.After(time.Now().Add(2*time.Hour)) {
						t.Fatal("fixture certificate lifetime is not bounded")
					}
					fixtureURL = fmt.Sprintf("http://127.0.0.1:%d", r.Snapshot.Routes[0].TargetPort)
					client := &http.Client{Timeout: 3 * time.Second}
					response, e := client.Get(fixtureURL + "/")
					if e != nil {
						t.Fatal(e)
					}
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					if e != nil || response.StatusCode != 200 || response.Header.Get("X-Sandbox-Fixture") != "ready" || !strings.Contains(string(body), "sandbox fixture response") {
						t.Fatal("self-test backend not usable", e)
					}
					if mode == "complete" {
						response, e = client.Get(fixtureURL + "/ws")
						if e != nil {
							t.Fatal(e)
						}
						response.Body.Close()
						if response.StatusCode != 426 {
							t.Fatal("websocket endpoint accepted no upgrade")
						}
						ws, e := http.NewRequestWithContext(ctx, "GET", fixtureURL+"/ws", nil)
						if e != nil {
							t.Fatal(e)
						}
						ws.Header.Set("Upgrade", "WebSocket")
						ws.Header.Set("Connection", "Upgrade")
						response, e = client.Do(ws)
						if e != nil {
							t.Fatal(e)
						}
						response.Body.Close()
						if response.StatusCode != 101 || response.Header.Get("Upgrade") != "websocket" {
							t.Fatal("fixture websocket handshake missing")
						}
						response, e = client.Get(fixtureURL + "/slow")
						if e != nil {
							t.Fatal(e)
						}
						response.Body.Close()
						if response.StatusCode != 200 {
							t.Fatal("slow fixture never completes")
						}
						canceled, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
						defer cancel()
						slow, e := http.NewRequestWithContext(canceled, "GET", fixtureURL+"/slow", nil)
						if e != nil {
							t.Fatal(e)
						}
						if response, e = client.Do(slow); e == nil {
							response.Body.Close()
							t.Fatal("slow fixture ignored canceled request")
						}
					}
					if mode == "fixture failed" {
						answer.Success = false
						answer.Error = "checks did not pass"
					}
				case 2:
					if r.ActivateSandbox || len(r.Snapshot.Snippets) != len(fixture.Snippets)+1 || !strings.Contains(r.Snapshot.Snippets[len(r.Snapshot.Snippets)-1].Content, "impossible") {
						t.Fatal("invalid candidate did not test rejection")
					}
					answer.Success = mode == "invalid accepted"
					answer.Error = "invalid configuration"
				case 3:
					if !r.ActivateSandbox || !proto.Equal(r.Snapshot, fixture) {
						t.Fatal("rejection mutated the fixture used for restoration")
					}
					if mode == "restore failed" {
						answer.Success = false
						answer.Error = "restore unavailable"
					}
				case 4:
					if r != request {
						t.Fatal("self-test substituted for actual candidate validation")
					}
					if mode == "candidate failed" {
						answer.Success = false
						answer.Error = "candidate unavailable"
					}
				default:
					t.Fatal("unexpected repeated validation")
				}
				return answer
			})
			wantCalls := 4
			fragment := ""
			success := mode == "complete" || mode == "HTTP3 complete"
			switch mode {
			case "fixture failed":
				wantCalls = 1
				fragment = "isolated fixture:"
			case "invalid accepted":
				wantCalls = 2
				fragment = "invalid candidate was not rejected"
			case "restore failed":
				wantCalls = 3
				fragment = "fixture restore:"
			case "candidate failed":
				fragment = "candidate unavailable"
			}
			if calls != wantCalls || result.Success != success || fragment != "" && !strings.Contains(result.Error, fragment) {
				t.Fatal("self-test result hid the failed stage", calls, result)
			}
			if calls == 4 {
				var report map[string]any
				if e := json.Unmarshal([]byte(result.ChecksJson), &report); e != nil {
					t.Fatal(e)
				}
				if report["fixture_passed"] != true || report["restore_passed"] != true || report["invalid_candidate_rejected"] != true || report["real_route_count"] != float64(1) || report["http3_passed"] != (mode == "HTTP3 complete") {
					t.Fatal("fixture and actual validation coverage conflated", report)
				}
			}
			if !proto.Equal(request, original) || m.State().ConfigRevision != 0 {
				t.Fatal("self-test modified the candidate or production revision")
			}
			client := &http.Client{Timeout: time.Second}
			if response, e := client.Get(fixtureURL); e == nil {
				response.Body.Close()
				t.Fatal("self-test backend remained exposed after completion")
			}
		})
	}
}
func TestSandboxPublicBoundaryRejectsMissingSnapshotAndFailedValidator(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	m := validationManagerFixture(t, "echo 'invalid candidate'; exit 1")
	for _, request := range []*gatewayv2.ValidationRequest{nil, {TaskId: "missing"}} {
		result := m.ValidateSandbox(context.Background(), request)
		if result.Success || result.Error != "sandbox validation requires a snapshot" || result.TaskId != request.GetTaskId() {
			t.Fatal("missing sandbox request accepted", result)
		}
	}
	request := &gatewayv2.ValidationRequest{TaskId: "rejected", Snapshot: validationSnapshot()}
	result := m.ValidateSandbox(context.Background(), request)
	if result.Success || !strings.Contains(result.Error, "isolated fixture:") || result.TaskId != request.TaskId {
		t.Fatal("public sandbox entry bypassed actual validation", result)
	}
	current, e := os.Readlink(filepath.Join(m.root, "current"))
	if e != nil {
		t.Fatal(e)
	}
	assertValidationCleaned(t, m, current)
}
