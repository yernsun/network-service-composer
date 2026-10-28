package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestObservedReadinessSeparatesRuntimeFromProbeEvidence(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mode   model.ReadinessMode
		mutate func(*model.DiscoveredEndpoint, *model.ReadinessPolicy)
		want   model.ReadyState
		source model.ReadinessMode
	}{
		{"stopping", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.RuntimeState = model.EndpointStopping }, model.Draining, model.ReadinessAuto},
		{"dead", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.RuntimeState = model.EndpointDead }, model.Draining, model.ReadinessAuto},
		{"not running", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.RuntimeState = "starting" }, model.ReadyStarting, model.ReadinessAuto},
		{"invalid address", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.Address = "service-name" }, model.NotReady, model.ReadinessAuto},
		{"missing port", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.Port = 0 }, model.NotReady, model.ReadinessAuto},
		{"overflow port", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.Port = 65536 }, model.NotReady, model.ReadinessAuto},
		{"docker healthy", model.ReadinessDocker, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.HealthState = model.HealthHealthy }, model.Ready, model.ReadinessDocker},
		{"docker unknown", model.ReadinessDocker, nil, model.NotReady, model.ReadinessDocker},
		{"auto health", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.HealthState = model.HealthHealthy }, model.Ready, model.ReadinessDocker},
		{"auto unhealthy", model.ReadinessAuto, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) { e.HealthState = model.HealthUnhealthy }, model.NotReady, model.ReadinessDocker},
		{"auto HTTP", model.ReadinessAuto, func(_ *model.DiscoveredEndpoint, p *model.ReadinessPolicy) { p.Path = "/ready" }, model.Ready, model.ReadinessHTTP},
		{"auto TCP", model.ReadinessAuto, func(_ *model.DiscoveredEndpoint, p *model.ReadinessPolicy) { p.TCPFallback = true }, model.Ready, model.ReadinessTCP},
		{"auto grace", model.ReadinessAuto, nil, model.ReadyStarting, model.ReadinessGrace},
		{"grace zero", model.ReadinessGrace, func(_ *model.DiscoveredEndpoint, p *model.ReadinessPolicy) { p.StartupGracePeriod = 0 }, model.Ready, model.ReadinessGrace},
		{"grace elapsed", model.ReadinessGrace, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) {
			started := now.Add(-time.Second)
			e.StartedAt = &started
		}, model.Ready, model.ReadinessGrace},
		{"grace future", model.ReadinessGrace, func(e *model.DiscoveredEndpoint, _ *model.ReadinessPolicy) {
			started := now.Add(time.Second)
			e.StartedAt = &started
		}, model.ReadyStarting, model.ReadinessGrace},
		{"explicit HTTP", model.ReadinessHTTP, nil, model.Ready, model.ReadinessHTTP}, {"explicit TCP", model.ReadinessTCP, nil, model.Ready, model.ReadinessTCP},
		{"unknown policy", "unknown", nil, model.NotReady, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := model.DiscoveredEndpoint{RuntimeState: model.EndpointRunning, Address: "127.0.0.1", Port: 8080, HealthState: model.HealthUnknown}
			policy := model.ReadinessPolicy{Mode: tc.mode, StartupGracePeriod: time.Second}
			if tc.mutate != nil {
				tc.mutate(&endpoint, &policy)
			}
			state, source := initialReadiness(policy, endpoint, now)
			if state != tc.want || source != tc.source {
				t.Fatalf("state=%s source=%s; want %s/%s", state, source, tc.want, tc.source)
			}
		})
	}
}

func TestProbeTokensInvalidateOnChangedTargetOrPolicy(t *testing.T) {
	base := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1}
	original := agentProbeToken(base, "127.0.0.1", 8080, model.EndpointRunning, model.HealthUnknown)
	if len(original) != 64 || agentProbeToken(base, "127.0.0.1", 8080, model.EndpointRunning, model.HealthUnknown) != original {
		t.Fatal("unstable probe identity")
	}
	for _, mutate := range []func(*model.ReadinessPolicy){
		func(p *model.ReadinessPolicy) { p.Path = "/changed" }, func(p *model.ReadinessPolicy) { p.Scheme = "https" }, func(p *model.ReadinessPolicy) { p.Interval++ }, func(p *model.ReadinessPolicy) { p.Timeout++ },
		func(p *model.ReadinessPolicy) { p.SuccessThreshold++ }, func(p *model.ReadinessPolicy) { p.FailureThreshold++ }, func(p *model.ReadinessPolicy) { p.StartupGracePeriod++ }, func(p *model.ReadinessPolicy) { p.StartupTimeout++ }, func(p *model.ReadinessPolicy) { p.TCPFallback = true },
	} {
		policy := base
		mutate(&policy)
		if agentProbeToken(policy, "127.0.0.1", 8080, model.EndpointRunning, model.HealthUnknown) == original {
			t.Fatal("changed policy retained old probe identity")
		}
	}
	for _, tc := range []struct {
		address string
		port    int
		state   model.RuntimeState
	}{
		{"127.0.0.1", 8080, model.EndpointDead}, {"invalid", 8080, model.EndpointRunning}, {"127.0.0.1", 0, model.EndpointRunning}, {"127.0.0.1", 65536, model.EndpointRunning},
	} {
		if agentProbeToken(base, tc.address, tc.port, tc.state, model.HealthUnknown) != "" {
			t.Fatal("probe issued for ineligible endpoint")
		}
	}
	for _, tc := range []struct {
		policy   model.ReadinessPolicy
		health   model.HealthState
		eligible bool
	}{
		{model.ReadinessPolicy{Mode: model.ReadinessAuto, Path: "/ready"}, model.HealthUnknown, true},
		{model.ReadinessPolicy{Mode: model.ReadinessAuto}, model.HealthHealthy, false},
		{model.ReadinessPolicy{Mode: model.ReadinessAuto, TCPFallback: true}, model.HealthUnknown, true},
		{model.ReadinessPolicy{Mode: model.ReadinessAuto}, model.HealthUnknown, false},
		{model.ReadinessPolicy{Mode: model.ReadinessDocker}, model.HealthHealthy, false},
	} {
		if (agentProbeToken(tc.policy, "::1", 65535, model.EndpointRunning, tc.health) != "") != tc.eligible {
			t.Fatalf("incorrect automatic probe eligibility: %#v", tc)
		}
	}
	for _, name := range []string{"", strings.Repeat("x", 254), "127.0.0.1", "::1", "invalid/name"} {
		if serviceDNSProbeToken(base, name, 8080) != "" {
			t.Fatalf("invalid DNS target accepted: %q", name)
		}
	}
	for _, port := range []int{0, 65536} {
		if serviceDNSProbeToken(base, "service", port) != "" {
			t.Fatal("invalid DNS port accepted")
		}
	}
	for _, name := range []string{"service", "SERVICE_1.example-test", strings.Repeat("a", 253)} {
		if len(serviceDNSProbeToken(base, name, 8080)) != 64 {
			t.Fatalf("valid DNS target rejected: %q", name)
		}
	}
	if serviceDNSProbeToken(model.ReadinessPolicy{Mode: model.ReadinessDocker}, "service", 8080) != "" {
		t.Fatal("Docker healthcheck became DNS probe")
	}
}

func TestSavedTargetPortRestoresOnlyAuthorizedMissingMetadata(t *testing.T) {
	port := 8443
	zero := 0
	overflow := 65536
	for _, tc := range []struct {
		name       string
		service    model.Service
		discovered model.DiscoveredService
		want       int
		eligible   bool
	}{
		{"observed port wins", model.Service{Managed: true, DiscoveredPort: 8080}, model.DiscoveredService{TargetPort: 80}, 80, false},
		{"external without override", model.Service{DiscoveredPort: 8080}, model.DiscoveredService{}, 0, false},
		{"managed saved port", model.Service{Managed: true, DiscoveredPort: 8080}, model.DiscoveredService{}, 8080, true},
		{"explicit override", model.Service{TargetPortOverride: &port}, model.DiscoveredService{}, 8443, true},
		{"invalid zero", model.Service{Managed: true, DiscoveredPort: 8080, TargetPortOverride: &zero}, model.DiscoveredService{}, 0, false},
		{"invalid overflow", model.Service{TargetPortOverride: &overflow}, model.DiscoveredService{}, 0, false},
		{"configuration error retained", model.Service{TargetPortOverride: &port}, model.DiscoveredService{ConfigurationError: "unsafe configuration"}, 8443, false},
		{"other issue retained", model.Service{TargetPortOverride: &port}, model.DiscoveredService{RoutingIssues: []string{"DNS conflict"}}, 8443, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.discovered
			d.Endpoints = []model.DiscoveredEndpoint{{Port: 0}, {Port: 1234}}
			applySavedTargetPort(tc.service, &d)
			if d.TargetPort != tc.want || d.RoutingEligible != tc.eligible || d.ConfigurationError != tc.discovered.ConfigurationError {
				t.Fatalf("unsafe port restoration: %#v", d)
			}
			if d.Endpoints[1].Port != 1234 {
				t.Fatal("observed endpoint port overwritten")
			}
			if tc.discovered.TargetPort == 0 && tc.want > 0 && d.Endpoints[0].Port != tc.want {
				t.Fatal("missing endpoint port not restored")
			}
		})
	}
	for _, issue := range []string{"exactly one port required", "service does not expose port; required", "target_port required"} {
		d := model.DiscoveredService{RoutingIssues: []string{issue, "permission denied"}}
		applySavedTargetPort(model.Service{TargetPortOverride: &port}, &d)
		if len(d.RoutingIssues) != 1 || d.RoutingIssues[0] != "permission denied" || d.Enabled {
			t.Fatalf("unrelated problem lost: %#v", d)
		}
	}
	for _, issue := range []string{"required", "port absent", "port required for unrelated policy"} {
		if missingTargetPortIssue(issue) {
			t.Fatalf("unrelated issue classified as missing target: %s", issue)
		}
	}
}
