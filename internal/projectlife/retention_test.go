package projectlife

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestNodeRetentionRejectsMissingReplacedStaleAndAnonymousStorage(t *testing.T) {
	now := time.Now().UTC()
	ref := "example.test/db@sha256:" + strings.Repeat("a", 64)
	spec := &swarm.ContainerSpec{Image: ref, Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "stack_data", Target: "/data"}}}
	baseline := NodeStorage{NodeID: "node-one", At: now, Complete: true, Volumes: map[string]time.Time{"stack_data": now}, VolumeOwners: map[string]string{"stack_data": "stack"}, Images: map[string][]string{ref: {"/data"}}}
	retained, err := retainedNodeSpec(spec, baseline)
	if err != nil || len(retained) != 1 {
		t.Fatal(retained, err)
	}
	for _, scenario := range []string{"valid", "offline", "stale", "future", "incomplete", "foreign", "missing", "replaced", "owner changed"} {
		t.Run(scenario, func(t *testing.T) {
			node := baseline
			node.Volumes = map[string]time.Time{"stack_data": now}
			node.VolumeOwners = map[string]string{"stack_data": "stack"}
			var fail error
			switch scenario {
			case "owner changed":
				node.VolumeOwners["stack_data"] = "foreign"
			case "offline":
				fail = errors.New("offline")
			case "stale":
				node.At = now.Add(-time.Minute)
			case "future":
				node.At = now.Add(time.Minute)
			case "incomplete":
				node.Complete = false
			case "foreign":
				node.NodeID = "other-node"
			case "missing":
				node.Volumes = map[string]time.Time{}
			case "replaced":
				node.Volumes["stack_data"] = now.Add(time.Second)
			}
			read := func(context.Context, string) (NodeStorage, error) { return node, fail }
			err := VerifyRetainedVolumes(context.Background(), read, retained)
			if (err == nil) != (scenario == "valid") {
				t.Fatal("unsafe retention accepted", err)
			}
		})
	}
	for _, scenario := range []string{"anonymous", "implicit image volume", "tag", "unknown image", "bind"} {
		t.Run(scenario, func(t *testing.T) {
			s := *spec
			s.Mounts = append([]mount.Mount{}, spec.Mounts...)
			node := baseline
			switch scenario {
			case "anonymous":
				s.Mounts[0].Source = ""
			case "implicit image volume":
				node.Images = map[string][]string{ref: {"/data/nested"}}
			case "tag":
				s.Image = "db:latest"
			case "unknown image":
				node.Images = nil
			case "bind":
				s.Mounts[0].Type = mount.TypeBind
			}
			if _, err := retainedNodeSpec(&s, node); err == nil {
				t.Fatal("unsafe task removal accepted")
			}
		})
	}
}

func TestRemotePinnedSwarmRemovalRetainsVolumesAndVerifiesContainerExit(t *testing.T) {
	for _, scenario := range []string{"valid", "incomplete after removal", "unknown after removal", "incomplete", "anonymous image volume", "foreign mount", "replaced volume", "lingering container", "missing actual container", "changed actual mount", "foreign volume owner"} {
		t.Run(scenario, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			f.remoteTask = true
			f.dataNode = "uninspected-worker"
			now := time.Now().UTC()
			evidence := NodeStorage{NodeID: f.dataNode, At: now, Complete: true, VolumeOwners: map[string]string{"fixture-data": "fixture"}, Volumes: map[string]time.Time{"fixture-data": now}, Images: map[string][]string{f.image: {"/data"}}, Containers: map[string]container.InspectResponse{}}
			var actual container.InspectResponse
			if err := json.Unmarshal([]byte(`{"Id":"remote-one","Image":"`+f.image+`","State":{"Running":true},"Config":{"Image":"`+f.image+`","Labels":{"com.docker.swarm.service.id":"service-one","com.docker.swarm.task.id":"task-one"}},"Mounts":[{"Type":"volume","Name":"fixture-data","Destination":"/data"}]}`), &actual); err != nil {
				t.Fatal(err)
			}
			evidence.Containers["remote-one"] = actual
			switch scenario {
			case "foreign volume owner":
				evidence.VolumeOwners["fixture-data"] = "foreign"
			case "missing actual container":
				evidence.Containers = nil
			case "changed actual mount":
				actual.Mounts[0].Name = "other"
				evidence.Containers["remote-one"] = actual
			case "incomplete":
				evidence.Complete = false
			case "anonymous image volume":
				evidence.Images[f.image] = []string{"/data", "/unmounted"}
			case "foreign mount":
				evidence.Volumes = nil
			}
			afterRemoval := 0
			d.ReadNode = func(context.Context, string) (NodeStorage, error) {
				if f.missing && (scenario == "incomplete after removal" || scenario == "unknown after removal") {
					afterRemoval++
					evidence.Complete = scenario == "incomplete after removal" && afterRemoval > 1
				}
				if c, ok := evidence.Containers["remote-one"]; ok {
					c.State.Running = !f.missing || scenario == "lingering container"
					evidence.Containers["remote-one"] = c
				}
				return evidence, nil
			}
			observed, err := d.Observe(context.Background(), rt, app, services)
			invalid := scenario == "foreign volume owner" || scenario == "incomplete" || scenario == "anonymous image volume" || scenario == "foreign mount" || scenario == "missing actual container" || scenario == "changed actual mount"
			if invalid {
				if err == nil || len(f.requests) != 0 {
					t.Fatal("unsafe observation accepted", err)
				}
				return
			}
			if err != nil || len(observed.Retained) != 1 {
				t.Fatal("remote retention not observed", err)
			}
			if scenario == "replaced volume" {
				evidence.Volumes["fixture-data"] = now.Add(time.Second)
			}
			if scenario == "lingering container" {
				var remote container.InspectResponse
				if err := json.Unmarshal([]byte(`{"Id":"remote-one","State":{"Running":true},"Config":{"Labels":{"com.docker.swarm.service.id":"service-one"}}}`), &remote); err != nil {
					t.Fatal(err)
				}
				evidence.Containers["remote-one"] = remote
			}
			timeout := 100 * time.Millisecond
			if scenario == "incomplete after removal" {
				timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err = d.Remove(ctx, rt, app, services, observed, true)
			if scenario == "valid" || scenario == "incomplete after removal" {
				if err != nil || !f.missing {
					t.Fatal("safe remote service removal failed", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unknown data or exit accepted")
			}
			if scenario == "replaced volume" && len(f.requests) != 0 {
				t.Fatal("removed workload after volume replacement")
			}
		})
	}
}

func TestExecutionRetentionWaitsForFreshCollection(t *testing.T) {
	now := time.Now()
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "deleted after admission"}[missing], func(t *testing.T) {
			calls := 0
			read := func(context.Context, string) (NodeStorage, error) {
				calls++
				v := NodeStorage{NodeID: "node", Generation: 1, Sequence: uint64(calls), At: now, Complete: true, Volumes: map[string]time.Time{"data": now}}
				if missing && calls >= 3 {
					v.Volumes = map[string]time.Time{}
				}
				return v, nil
			}
			err := VerifyRetainedVolumes(context.Background(), executionNodeReader(read), []RetainedVolume{{NodeID: "node", Name: "data", CreatedAt: now}})
			if (err != nil) != missing || calls < 3 {
				t.Fatalf("fresh evidence: calls=%d error=%v", calls, err)
			}
		})
	}
	t.Run("collector does not advance", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		read := func(context.Context, string) (NodeStorage, error) {
			return NodeStorage{NodeID: "node", Generation: 1, Sequence: 1, At: now, Complete: true}, nil
		}
		if _, err := executionNodeReader(read)(ctx, "node"); !errors.Is(err, ErrDataRetention) {
			t.Fatal(err)
		}
	})
}

func TestPinnedServiceReadinessRequiresHealthyActualContainer(t *testing.T) {
	for _, status := range []string{"", "starting", "healthy", "unhealthy"} {
		t.Run(status, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			f.remoteTask = true
			f.dataNode = "uninspected-worker"
			f.healthcheck = true
			now := time.Now()
			var actual container.InspectResponse
			raw := `{"Id":"remote-one","State":{"Running":true},"Config":{"Image":"` + f.image + `","Labels":{"com.docker.swarm.service.id":"service-one","com.docker.swarm.task.id":"task-one"}},"Mounts":[{"Type":"volume","Name":"fixture-data","Destination":"/data"}]}`
			if err := json.Unmarshal([]byte(raw), &actual); err != nil {
				t.Fatal(err)
			}
			if status != "" {
				actual.State.Health = &container.Health{Status: container.HealthStatus(status)}
			}
			d.ReadNode = func(context.Context, string) (NodeStorage, error) {
				return NodeStorage{NodeID: f.dataNode, At: now, Complete: true, VolumeOwners: map[string]string{"fixture-data": "fixture"}, Volumes: map[string]time.Time{"fixture-data": now}, Images: map[string][]string{f.image: {"/data"}}, Containers: map[string]container.InspectResponse{"remote-one": actual}}, nil
			}
			observed, err := d.Observe(context.Background(), rt, app, services)
			if err != nil || observed.Ready != (status == "healthy") || observed.Unhealthy != (status == "unhealthy") {
				t.Fatal(observed, err)
			}
		})
	}
}

func TestExecutionStorageWaitsForCompleteInitialEvidence(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		reads := 0
		read := func(context.Context, string) (NodeStorage, error) {
			reads++
			return NodeStorage{NodeID: "worker", Generation: 1, Sequence: uint64(reads), At: time.Now(), Complete: !permanent && reads > 1}, nil
		}
		timeout := 3 * time.Second
		if permanent {
			timeout = 30 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		value, err := executionNodeReader(read)(ctx, "worker")
		cancel()
		if permanent {
			if !errors.Is(err, ErrDataRetention) {
				t.Fatal("permanent unknown evidence accepted", err)
			}
		} else if err != nil || !value.Complete || value.Sequence < 4 {
			t.Fatal("did not wait for a complete initial scan and two fresh subsequent reports", value.Sequence, err)
		}
	}
}

func TestExecutionStorageWaitsForCompleteSubsequentEvidence(t *testing.T) {
	reads := 0
	read := func(context.Context, string) (NodeStorage, error) {
		reads++
		return NodeStorage{NodeID: "worker", Generation: 1, Sequence: uint64(reads), At: time.Now(), Complete: reads != 2}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := executionNodeReader(read)(ctx, "worker")
	if err != nil || !value.Complete || value.Sequence != 3 {
		t.Fatal("partial subsequent report accepted or terminated a bounded proof wait", value.Sequence, err)
	}
}

func TestFirstInstallationRejectsUnrecordedVolume(t *testing.T) {
	for _, exists := range []bool{false, true} {
		volumes := map[string]time.Time{}
		if exists {
			volumes["stack_data"] = time.Now()
		}
		d := Docker{ReadNode: func(context.Context, string) (NodeStorage, error) {
			return NodeStorage{NodeID: "worker", At: time.Now(), Complete: true, Volumes: volumes}, nil
		}}
		err := d.VerifyNewStorage(context.Background(), model.Runtime{}, model.Application{}, "worker", []string{"stack_data"})
		if (err != nil) != exists {
			t.Fatalf("existing=%v error=%v", exists, err)
		}
	}
}

func TestExecutionRejectsExternallyChangedRunningStorage(t *testing.T) {
	now := time.Now()
	ref := "image@sha256:" + strings.Repeat("a", 64)
	for _, scenario := range []string{"valid", "changed volume", "changed target", "anonymous volume", "unknown image", "additional writer", "foreign writer", "foreign read-only backup"} {
		t.Run(scenario, func(t *testing.T) {
			var actual container.InspectResponse
			raw := `{"Id":"container","State":{"Running":true},"Config":{"Image":"` + ref + `","Labels":{"com.docker.stack.namespace":"stack","com.docker.swarm.service.id":"service","com.docker.swarm.task.id":"task"}},"Mounts":[{"Type":"volume","Name":"data","Destination":"/data"}]}`
			if err := json.Unmarshal([]byte(raw), &actual); err != nil {
				t.Fatal(err)
			}
			evidence := NodeStorage{NodeID: "node", At: now, Complete: true, Volumes: map[string]time.Time{"data": now}, VolumeOwners: map[string]string{"data": "stack"}, Images: map[string][]string{ref: {"/data"}}, Containers: map[string]container.InspectResponse{}}
			switch scenario {
			case "changed volume":
				actual.Mounts[0].Name = "other"
			case "changed target":
				actual.Mounts[0].Destination = "/other"
			case "anonymous volume":
				actual.Mounts[0].Name = ""
			case "unknown image":
				evidence.Images = nil
			case "foreign writer", "foreign read-only backup":
				var other container.InspectResponse
				raw := `{"Id":"external","State":{"Running":true},"Config":{"Labels":{"com.docker.stack.namespace":"foreign"}},"Mounts":[{"Type":"volume","Name":"data","Destination":"/backup","RW":true}]}`
				if err := json.Unmarshal([]byte(raw), &other); err != nil {
					t.Fatal(err)
				}
				other.Mounts[0].RW = scenario == "foreign writer"
				evidence.Containers[other.ID] = other
			case "additional writer":
				other := actual
				other.ID = "another-container"
				evidence.Containers[other.ID] = other
			}
			evidence.Containers[actual.ID] = actual
			d := Docker{ReadNode: func(context.Context, string) (NodeStorage, error) { return evidence, nil }}
			err := d.VerifyRetention(context.Background(), model.Runtime{}, model.Application{ProjectName: "stack"}, []RetainedVolume{{NodeID: "node", Name: "data", Target: "/data", Owner: "stack", CreatedAt: now}})
			if (err == nil) != (scenario == "valid" || scenario == "foreign read-only backup") {
				t.Fatal("unsafe running storage accepted", err)
			}
		})
	}
}
