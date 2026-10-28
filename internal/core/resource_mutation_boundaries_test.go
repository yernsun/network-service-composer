package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestPoolMutationRequiresSeparateDisableAndNetworkChange(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	if _, err := c.CreatePool(ctx, CreatePoolInput{Name: " "}); err == nil {
		t.Fatal("unnamed pool accepted")
	}
	for _, address := range []string{"%", "ftp://server", "http:///missing", "http://user@server", "http://server?query", "http://server#fragment"} {
		if _, err := c.CreatePool(ctx, CreatePoolInput{Name: "invalid", ChallengeUpstream: address}); err == nil {
			t.Fatalf("invalid challenge upstream accepted: %q", address)
		}
	}
	defaulted, err := c.CreatePool(ctx, CreatePoolInput{Name: " defaulted "})
	if err != nil || defaulted.Name != "defaulted" || defaulted.BackendNetwork != "nsc-ingress" || defaulted.ChallengeUpstream != c.config.Server.PublicURL {
		t.Fatalf("pool defaults=%#v %v", defaulted, err)
	}
	for _, mutate := range []func(*model.GatewayPool){func(p *model.GatewayPool) { p.Name = " " }, func(p *model.GatewayPool) { p.BackendNetwork = " " }, func(p *model.GatewayPool) { p.ChallengeUpstream = " " }, func(p *model.GatewayPool) { p.ChallengeUpstream = "file:///tmp" }} {
		bad := pool
		mutate(&bad)
		if _, err := c.UpdatePool(ctx, bad, pool.Revision); err == nil {
			t.Fatal("invalid pool patch accepted")
		}
	}
	moved := pool
	moved.BackendNetwork = "another"
	if _, err := c.UpdatePool(ctx, moved, pool.Revision); err == nil {
		t.Fatal("enabled pool network changed")
	}
	pool.Enabled = false
	pool, err = c.UpdatePool(ctx, pool, pool.Revision)
	if err != nil {
		t.Fatal(err)
	}
	moved = pool
	moved.BackendNetwork = "another"
	moved.Enabled = true
	if _, err := c.UpdatePool(ctx, moved, pool.Revision); err == nil {
		t.Fatal("network change and enable combined")
	}
	moved.Enabled = false
	moved.Name = " renamed "
	moved.BackendNetwork = " another "
	moved.ChallengeUpstream = " https://controller.example.test/ "
	changed, err := c.UpdatePool(ctx, moved, pool.Revision)
	if err != nil || changed.Name != "renamed" || changed.BackendNetwork != "another" || changed.ChallengeUpstream != "https://controller.example.test" {
		t.Fatalf("disabled network change failed: %#v %v", changed, err)
	}
	if _, err := c.UpdatePool(ctx, pool, pool.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale pool overwritten: %v", err)
	}
	missing := pool
	missing.ID = "missing"
	if _, err := c.UpdatePool(ctx, missing, 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing pool mutation=%v", err)
	}
}

func TestRuntimeMutationPreservesModeAndExplicitMigrationBoundary(t *testing.T) {
	c, runtime, pool := controlFixture(t)
	ctx := context.Background()
	other, err := c.CreatePool(ctx, CreatePoolInput{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []CreateRuntimeInput{
		{PoolID: "missing"}, {PoolID: pool.ID, RuntimeType: "kubernetes"},
		{PoolID: pool.ID, RuntimeType: model.RuntimeDocker, Endpoint: "unix:///fixture"},
		{PoolID: pool.ID, RuntimeType: model.RuntimeSwarm, Name: "name"},
		{PoolID: pool.ID, RuntimeType: model.RuntimeDocker, Name: "name", Endpoint: "https://server"},
		{PoolID: pool.ID, RuntimeType: model.RuntimeDocker, Name: "name", Endpoint: "tcp://server:2376", CredentialRef: "missing"},
	} {
		if _, err := c.CreateRuntime(ctx, in); err == nil {
			t.Fatalf("invalid runtime accepted: %#v", in)
		}
	}
	c.config.Credentials["remote"] = config.DockerCredential{CAFile: "ca", CertFile: "cert", KeyFile: "key"}
	if _, err := c.CreateRuntime(ctx, CreateRuntimeInput{PoolID: other.ID, Name: "swarm", RuntimeType: model.RuntimeSwarm, Endpoint: "tcp://isolated:2376", CredentialRef: "remote"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("SQLite accepted unsupported Swarm runtime: %v", err)
	}
	created, err := c.CreateRuntime(ctx, CreateRuntimeInput{PoolID: other.ID, Name: " remote ", RuntimeType: model.RuntimeDocker, Endpoint: "tcp://isolated:2376", CredentialRef: "remote"})
	if err != nil || created.Status != model.RuntimeInitializing || created.ManagementEnabled {
		t.Fatalf("new connection granted unchecked management: %#v %v", created, err)
	}
	for _, mutate := range []func(*model.Runtime){
		func(r *model.Runtime) { r.ID = "missing" }, func(r *model.Runtime) { r.PoolID = "missing" }, func(r *model.Runtime) { r.Type = model.RuntimeSwarm },
		func(r *model.Runtime) { r.PoolID = other.ID }, func(r *model.Runtime) { r.Name = " " }, func(r *model.Runtime) { r.Endpoint = " " },
		func(r *model.Runtime) { r.CredentialRef = "missing" }, func(r *model.Runtime) { r.Endpoint = "file:///tmp" },
	} {
		bad := runtime
		mutate(&bad)
		if _, err := c.UpdateRuntime(ctx, bad, runtime.Revision); err == nil {
			t.Fatalf("unsafe runtime update accepted: %#v", bad)
		}
	}
	runtime.Enabled = false
	runtime, err = c.UpdateRuntime(ctx, runtime, runtime.Revision)
	if err != nil || runtime.Status != model.RuntimeDisabled {
		t.Fatalf("runtime disable=%#v %v", runtime, err)
	}
	moved := runtime
	moved.PoolID = other.ID
	moved.Enabled = true
	if _, err := c.UpdateRuntime(ctx, moved, runtime.Revision); err == nil {
		t.Fatal("runtime moved and enabled in one operation")
	}
	moved.Enabled = false
	moved.CredentialRef = " remote "
	moved.Endpoint = " tcp://isolated:2376 "
	moved, err = c.UpdateRuntime(ctx, moved, runtime.Revision)
	if err != nil || moved.PoolID != other.ID || moved.CredentialRef != "remote" {
		t.Fatalf("disabled migration failed: %#v %v", moved, err)
	}
	moved.Enabled = true
	moved.LastError = "previous failure"
	enabled, err := c.UpdateRuntime(ctx, moved, moved.Revision)
	if err != nil || enabled.Status != model.RuntimeInitializing || enabled.LastError != "" {
		t.Fatalf("reconnection claimed health or retained stale error: %#v %v", enabled, err)
	}
	if _, err := c.UpdateRuntime(ctx, moved, moved.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale runtime overwritten: %v", err)
	}
}

func TestReadinessValidationRejectsEachUnsafeValue(t *testing.T) {
	valid := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "https", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1}
	for _, mutate := range []func(*model.ReadinessPolicy){
		func(p *model.ReadinessPolicy) { p.Mode = "exec" }, func(p *model.ReadinessPolicy) { p.Path = "" }, func(p *model.ReadinessPolicy) { p.Path = "relative" },
		func(p *model.ReadinessPolicy) { p.Path = "/ready?query" }, func(p *model.ReadinessPolicy) { p.Scheme = "file" }, func(p *model.ReadinessPolicy) { p.Interval = 0 },
		func(p *model.ReadinessPolicy) { p.Timeout = 0 }, func(p *model.ReadinessPolicy) { p.SuccessThreshold = 0 }, func(p *model.ReadinessPolicy) { p.FailureThreshold = 0 }, func(p *model.ReadinessPolicy) { p.Timeout = 2 * time.Second },
	} {
		bad := valid
		mutate(&bad)
		if err := validateReadiness(bad); err == nil {
			t.Fatalf("unsafe readiness accepted: %#v", bad)
		}
	}
	for _, mode := range []model.ReadinessMode{model.ReadinessAuto, model.ReadinessDocker, model.ReadinessTCP, model.ReadinessGrace, model.ReadinessHTTP} {
		for _, scheme := range []string{"", "http", "https"} {
			p := valid
			p.Mode = mode
			p.Scheme = scheme
			if mode != model.ReadinessHTTP {
				p.Path = ""
			}
			if err := validateReadiness(p); err != nil {
				t.Fatalf("supported readiness rejected: %v", err)
			}
		}
	}
	for _, endpoint := range []string{"%", "unix:", "tcp:", "npipe:"} {
		if err := validateDockerEndpoint(endpoint); err == nil {
			t.Fatalf("empty or malformed endpoint accepted: %q", endpoint)
		}
	}
	for _, endpoint := range []string{"npipe://host", "npipe:pipe", "npipe:///pipe"} {
		if err := validateDockerEndpoint(endpoint); err != nil {
			t.Fatalf("named pipe endpoint rejected: %q %v", endpoint, err)
		}
	}
	var patch ServicePatch
	if err := json.Unmarshal([]byte(`{"target_port_override":"not-number"}`), &patch); err == nil {
		t.Fatal("malformed nullable override accepted")
	}
	if err := json.Unmarshal([]byte(`{"target_port_override":null}`), &patch); err != nil || !patch.TargetPortOverride.Set || patch.TargetPortOverride.Value != nil {
		t.Fatal("explicit null lost clearing semantics")
	}
	if validProtocol(strings.ToUpper("http")) {
		t.Fatal("unnormalized unsupported protocol accepted")
	}
}
