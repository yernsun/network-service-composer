package core

import (
	"context"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestNodeRuntimeBindingCannotSelectForeignEngineOrRemoteDocker(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	in := CreateRuntimeInput{PoolID: pool.ID, Name: "node runtime", RuntimeType: model.RuntimeDocker, Endpoint: "node://node-one"}
	if _, err := c.CreateRuntime(ctx, in); err == nil {
		t.Fatal("unprovisioned node runtime created")
	}
	c.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "controller-one", NodeID: "node-one", EngineID: "engine-one", ClusterID: "cluster-one", URL: "https://controller-one:9444"}}
	runtime, err := c.CreateRuntime(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"node://foreign", "node://node-one/", "node://user@node-one", "node://node-one?extra=1", "node://node-one#fragment", "node://node-one:9444", "tcp://another:2376", "unix:///var/run/docker.sock"} {
		bad := in
		bad.Endpoint = endpoint
		if _, err := c.CreateRuntime(ctx, bad); err == nil {
			t.Fatal("unbound endpoint created", endpoint)
		}
		changed := runtime
		changed.Endpoint = endpoint
		if _, err := c.UpdateRuntime(ctx, changed, runtime.Revision); err == nil {
			t.Fatal("bound runtime changed ownership", endpoint)
		}
	}
	in.CredentialRef = "legacy-remote-credentials"
	if _, err := c.CreateRuntime(ctx, in); err == nil {
		t.Fatal("remote credential accepted for a bound node")
	}
	runtime.Name = "renamed"
	updated, err := c.UpdateRuntime(ctx, runtime, runtime.Revision)
	if err != nil || updated.Endpoint != "node://node-one" || updated.Name != "renamed" {
		t.Fatal("metadata update changed node binding", err)
	}
	read, err := c.store.GetRuntime(ctx, runtime.ID)
	if err != nil || read.Endpoint != updated.Endpoint {
		t.Fatal("node binding did not persist", err)
	}
}
