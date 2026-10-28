package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type RuntimeIdentity struct {
	EngineID  string `json:"engine_id"`
	NodeID    string `json:"node_id"`
	ClusterID string `json:"cluster_id"`
}

type RuntimeEvidence struct {
	FrozenTarget        model.RuntimeExecutionIdentity `json:"frozen_target"`
	OperationID         string                         `json:"operation_id"`
	OperationRevision   int64                          `json:"operation_revision"`
	InputDigest         string                         `json:"input_digest"`
	RuntimeID           string                         `json:"runtime_id"`
	RuntimeRevision     int64                          `json:"runtime_revision"`
	ApplicationID       string                         `json:"application_id"`
	ApplicationRevision int64                          `json:"application_revision"`
	Identity            RuntimeIdentity                `json:"identity"`
	ObservedAt          time.Time                      `json:"observed_at"`
	StateDigest         string                         `json:"state_digest"`
	Objects             []projectlife.Object           `json:"objects"`
}

type Reconciliation struct {
	Evidence RuntimeEvidence `json:"evidence"`
	Receipt  []byte          `json:"receipt"`
}

// LiveReconciler has no runtime write interface. Optional readers are used by
// isolated behavior tests; production opens the configured Docker transport.
type LiveReconciler struct {
	Store    storage.Store
	Keys     *security.Keyring
	Config   config.Config
	ReadInfo func(context.Context, model.Runtime) (system.Info, error)
	Open     func(model.Runtime, engine.TLSFiles) (*client.Client, error)
	Observe  func(context.Context, model.Runtime, model.Application, []model.Service) (projectlife.Observation, error)
	Now      func() time.Time
}

func (r LiveReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func receiptAAD(item Item) []byte {
	return []byte("recovery-reconcile:" + item.ID + ":" + strconv.FormatInt(item.Revision, 10) + ":" + item.InputDigest)
}

func (r LiveReconciler) Capture(ctx context.Context, item Item, expected RuntimeIdentity) (*Reconciliation, error) {
	if item.Kind != "deployment" || item.State != "RUNNING" || expected.EngineID == "" {
		return nil, errors.New("reconciliation requires a held running Application operation and the original engine identity")
	}
	op, err := r.Store.GetDeploymentOperation(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if op.Status != model.OperationRunning || op.Revision != item.Revision || inputDigest(op.EncryptedInput) != item.InputDigest || op.ApplicationID == "" {
		return nil, storage.ErrPrecondition
	}
	if held, err := storage.RecoveryHeld(ctx, r.Store, item.Kind, item.ID); err != nil || !held {
		if err != nil {
			return nil, err
		}
		return nil, storage.ErrPrecondition
	}
	runtime, err := r.Store.GetRuntime(ctx, op.RuntimeID)
	if err != nil {
		return nil, err
	}
	configuredRuntime := runtime
	if frozen := op.ExecutionIdentity; frozen.Endpoint != "" {
		original := runtime
		original.PoolID, original.Type, original.Endpoint, original.CredentialRef, original.ClusterID = frozen.PoolID, frozen.Type, frozen.Endpoint, frozen.CredentialRef, frozen.ClusterID
		if execution.RuntimeFingerprint(original) != execution.RuntimeFingerprint(runtime) {
			return nil, projectlife.ErrOwnership
		}
	}
	if runtime.Type == model.RuntimeSwarm && execution.NodeID(runtime.Endpoint) != "" {
		matched := false
		for _, binding := range r.Config.Controller.Nodes {
			if binding.InstanceID == r.Config.Controller.InstanceID && binding.ClusterID == runtime.ClusterID && binding.NodeID == expected.NodeID && binding.EngineID == expected.EngineID {
				runtime.Endpoint = "node://" + binding.NodeID
				matched = true
				break
			}
		}
		if !matched {
			return nil, projectlife.ErrOwnership
		}
	}
	app, err := r.Store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		return nil, err
	}
	if !runtime.Enabled || !runtime.ManagementEnabled || !app.Managed || app.RuntimeID != runtime.ID || app.ProjectName == "" {
		return nil, projectlife.ErrOwnership
	}
	services, err := pages(ctx, r.Store.ListServices, func(v model.Service) string { return v.ID })
	if err != nil {
		return nil, err
	}
	owned := []model.Service{}
	for _, service := range services {
		if service.ApplicationID == app.ID {
			prefix := app.ProjectName + "/"
			if runtime.Type == model.RuntimeSwarm {
				prefix = app.ProjectName + "_"
			}
			if !service.Managed || service.RuntimeID != runtime.ID || !strings.HasPrefix(service.ServiceKey, prefix) {
				return nil, projectlife.ErrOwnership
			}
			owned = append(owned, service)
		}
	}
	if len(owned) == 0 {
		return nil, errors.New("restored Application has no service ownership evidence")
	}
	read := r.ReadInfo
	if read == nil {
		read = r.readInfo
	}
	before, err := read(ctx, runtime)
	if err != nil {
		return nil, fmt.Errorf("runtime observation unavailable: %w", err)
	}
	if err = verifyRuntimeIdentity(runtime, before, expected); err != nil {
		return nil, err
	}
	observe := r.Observe
	if observe == nil {
		observe = projectlife.NewDocker(r.Config, r.Store, r.Keys).Observe
	}
	observation, err := observe(ctx, runtime, app, owned)
	if err != nil {
		return nil, fmt.Errorf("owned runtime evidence unavailable: %w", err)
	}
	if observation.ObservedAt.IsZero() || r.now().Sub(observation.ObservedAt) > time.Minute || observation.ObservedAt.After(r.now().Add(5*time.Second)) {
		return nil, errors.New("runtime observation is stale or incomplete")
	}
	if len(observation.Objects) == 0 {
		return nil, errors.New("restored Application has no current owned runtime object evidence")
	}
	after, err := read(ctx, runtime)
	if err != nil {
		return nil, err
	}
	if err = verifyRuntimeIdentity(runtime, after, expected); err != nil {
		return nil, err
	}
	sort.Slice(observation.Objects, func(i, j int) bool { return observation.Objects[i].ID < observation.Objects[j].ID })
	sort.Strings(observation.Preserved)
	sort.Strings(observation.Nodes)
	sort.Slice(observation.Retained, func(i, j int) bool {
		a, b := observation.Retained[i], observation.Retained[j]
		return a.NodeID+"\x00"+a.Name+"\x00"+a.Target < b.NodeID+"\x00"+b.Name+"\x00"+b.Target
	})
	for _, object := range observation.Objects {
		if object.ID == "" || object.Kind == "" || runtime.Type == model.RuntimeSwarm && object.Kind == "service" && object.Version == 0 {
			return nil, errors.New("runtime object has ambiguous identity or version")
		}
	}
	observation.ObservedAt = time.Time{}
	raw, err := json.Marshal(observation)
	if err != nil {
		return nil, err
	}
	frozen := op.ExecutionIdentity
	if frozen.Endpoint == "" {
		frozen = model.RuntimeExecutionIdentity{PoolID: configuredRuntime.PoolID, Type: configuredRuntime.Type, Endpoint: configuredRuntime.Endpoint, CredentialRef: configuredRuntime.CredentialRef, ClusterID: configuredRuntime.ClusterID}
		if node := strings.TrimPrefix(configuredRuntime.Endpoint, "node://"); node != configuredRuntime.Endpoint {
			frozen.NodeID = node
			for _, binding := range r.Config.Controller.Nodes {
				if binding.NodeID == node {
					frozen.EngineID = binding.EngineID
				}
			}
			if frozen.EngineID == "" || runtime.Type == model.RuntimeDocker && (expected.NodeID != node || expected.EngineID != frozen.EngineID) {
				return nil, errors.New("legacy task requires the provisioned original Engine identity")
			}
		}
	}
	evidence := RuntimeEvidence{FrozenTarget: frozen, OperationID: op.ID, OperationRevision: op.Revision, InputDigest: item.InputDigest, RuntimeID: runtime.ID, RuntimeRevision: runtime.Revision, ApplicationID: app.ID, ApplicationRevision: app.Revision, Identity: expected, ObservedAt: r.now(), StateDigest: inputDigest(raw), Objects: observation.Objects}
	raw, err = json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	receipt, err := r.Keys.Encrypt(raw, receiptAAD(item))
	if err != nil {
		return nil, err
	}
	return &Reconciliation{Evidence: evidence, Receipt: receipt}, nil
}

func (r LiveReconciler) Validate(ctx context.Context, item Item, proof *Reconciliation) error {
	if proof == nil || len(proof.Receipt) == 0 {
		return errors.New("fresh runtime reconciliation receipt is required")
	}
	raw, _, err := r.Keys.Decrypt(proof.Receipt, receiptAAD(item))
	if err != nil {
		return err
	}
	expected, err := json.Marshal(proof.Evidence)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, expected) || proof.Evidence.ObservedAt.After(r.now().Add(5*time.Second)) || r.now().Sub(proof.Evidence.ObservedAt) > 2*time.Minute {
		return storage.ErrPrecondition
	}
	current, err := r.Capture(ctx, item, proof.Evidence.Identity)
	if err != nil {
		return err
	}
	fresh := current.Evidence
	fresh.ObservedAt = proof.Evidence.ObservedAt
	freshRaw, err := json.Marshal(fresh)
	if err != nil {
		return err
	}
	if !bytes.Equal(freshRaw, raw) {
		return storage.ErrPrecondition
	}
	return nil
}

func verifyRuntimeIdentity(runtime model.Runtime, info system.Info, expected RuntimeIdentity) error {
	actual := RuntimeIdentity{EngineID: info.ID, NodeID: info.Swarm.NodeID}
	if info.Swarm.Cluster != nil {
		actual.ClusterID = info.Swarm.Cluster.ID
	}
	if actual != expected || runtime.Type == model.RuntimeSwarm && (!info.Swarm.ControlAvailable || runtime.ClusterID == "" || actual.ClusterID != runtime.ClusterID) {
		return errors.New("runtime engine/node/cluster identity is not the reviewed target")
	}
	return nil
}

func (r LiveReconciler) readInfo(ctx context.Context, runtime model.Runtime) (system.Info, error) {
	files := engine.TLSFiles{}
	var bound *config.ControllerNodeConfig
	if runtime.CredentialRef != "" {
		credentials, ok := r.Config.Credentials[runtime.CredentialRef]
		if !ok {
			return system.Info{}, errors.New("runtime credential reference unavailable")
		}
		files = engine.TLSFiles{CA: credentials.CAFile, Certificate: credentials.CertFile, Key: credentials.KeyFile}
	}
	if node := applicationx.RuntimeNodeID(runtime.Endpoint); node != "" {
		found := false
		for _, binding := range r.Config.Controller.Nodes {
			if binding.InstanceID == r.Config.Controller.InstanceID && binding.NodeID == node && binding.EngineID != "" && binding.ClusterID != "" && (runtime.ClusterID == "" || binding.ClusterID == runtime.ClusterID) && runtime.CredentialRef == "" {
				runtime.Endpoint = "unix:///var/run/docker.sock"
				bound = &binding
				found = true
				break
			}
		}
		if !found {
			return system.Info{}, projectlife.ErrOwnership
		}
	}
	open := r.Open
	if open == nil {
		open = engine.NewManagementClient
	}
	cli, err := open(runtime, files)
	if err != nil {
		return system.Info{}, err
	}
	defer cli.Close()
	value, err := cli.Info(ctx, client.InfoOptions{})
	if err == nil && bound != nil && (value.Info.ID != bound.EngineID || value.Info.Swarm.NodeID != bound.NodeID || value.Info.Swarm.Cluster == nil && runtime.Type != model.RuntimeDocker || value.Info.Swarm.Cluster != nil && value.Info.Swarm.Cluster.ID != bound.ClusterID) {
		return system.Info{}, projectlife.ErrOwnership
	}
	return value.Info, err
}
