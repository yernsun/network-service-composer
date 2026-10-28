// Package volumemanager manages node-local Docker volumes using the existing
// authenticated node transport and durable deployment operations.
package volumemanager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var ErrIdentity = errors.New("volume node identity unavailable")
var ErrBlocked = errors.New("volume has protected or unknown consumers")

const labelOperation = "com.nsc.volume.operation"
const labelRuntime = "com.nsc.volume.runtime"
const labelEngine = "com.nsc.volume.engine"
const labelKind = "com.nsc.volume.kind"

type Request struct {
	RuntimeID           string `json:"runtime_id"`
	NodeID              string `json:"node_id"`
	EngineID            string `json:"engine_id,omitempty"`
	Action              string `json:"action"`
	Name                string `json:"name,omitempty"`
	Version             string `json:"version,omitempty"`
	OperationID         string `json:"operation_id,omitempty"`
	AcknowledgeDataLoss bool   `json:"acknowledge_data_loss,omitempty"`
}
type Reference struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
	State  string `json:"state,omitempty"`
}
type Item struct {
	nodecontrol.VolumeInfo
	NodeID         string      `json:"node_id"`
	EngineID       string      `json:"engine_id"`
	Owned          bool        `json:"owned"`
	UsedBy         []Reference `json:"used_by"`
	BlockedReasons []string    `json:"blocked_reasons"`
}
type Result struct {
	NodeID     string          `json:"node_id"`
	EngineID   string          `json:"engine_id"`
	NodeName   string          `json:"node_name"`
	ObservedAt time.Time       `json:"observed_at"`
	Complete   bool            `json:"complete"`
	Items      []Item          `json:"items"`
	Name       string          `json:"name,omitempty"`
	Version    string          `json:"version,omitempty"`
	Services   []swarm.Service `json:"services,omitempty"`
	Nodes      []swarm.Node    `json:"nodes,omitempty"`
}
type Manager struct {
	Store    storage.Store
	Config   config.Config
	Keys     *security.Keyring
	ReadNode func(context.Context, string) (nodecontrol.Report, error)
	Open     func(model.Runtime, engine.TLSFiles) (*client.Client, error)
}

func New(store storage.Store, cfg config.Config, keys *security.Keyring) Manager {
	m := Manager{Store: store, Config: cfg, Keys: keys}
	if sqlStore, ok := store.(*storage.SQLStore); ok && keys != nil {
		reports := nodecontrol.Reports{Store: store, Keys: keys, Now: (controlplane.SQLState{Store: sqlStore}).DatabaseTime}
		m.ReadNode = reports.Read
	}
	return m
}

// LockKey is shared with application deployment so a pending reference cannot
// race a verified unused-volume removal inside NSC.
func LockKey(rt model.Runtime) string {
	if rt.ClusterID != "" {
		return "volume-runtime:" + rt.ClusterID
	}
	return fmt.Sprintf("volume-endpoint:%x", sha256.Sum256([]byte(rt.Endpoint)))
}

func (m Manager) operation(ctx context.Context, req Request, peer string) error {
	if req.Action != "create" && req.Action != "delete" {
		return nil
	}
	if req.EngineID == "" || req.NodeID == "" || req.Action == "delete" && (!req.AcknowledgeDataLoss || req.Name == "" || req.Version == "") {
		return ErrIdentity
	}
	op, err := m.Store.GetDeploymentOperation(ctx, req.OperationID)
	if err != nil {
		return err
	}
	expected := "VOLUME_CREATE"
	if req.Action == "delete" {
		expected = "VOLUME_DELETE"
	}
	if op.Type != expected || op.RuntimeID != req.RuntimeID || op.Status != model.OperationRunning || op.WorkerID == "" || peer != "" && op.WorkerID != peer || m.Keys == nil {
		return ErrIdentity
	}
	raw, _, err := m.Keys.Decrypt(op.EncryptedInput, []byte("deployment-operation:"+op.ID))
	if err != nil {
		return ErrIdentity
	}
	defer clear(raw)
	var frozen Request
	if json.Unmarshal(raw, &frozen) != nil {
		return ErrIdentity
	}
	frozen.OperationID = req.OperationID
	if frozen != req {
		return ErrIdentity
	}
	return nil
}

func (m Manager) binding(rt model.Runtime, node string) (*config.ControllerNodeConfig, error) {
	if rt.Type == model.RuntimeSwarm && (rt.ClusterID == "" || node == "") {
		return nil, ErrIdentity
	}
	if rt.Type == model.RuntimeDocker && !strings.HasPrefix(rt.Endpoint, "node://") {
		return nil, nil
	}
	for i := range m.Config.Controller.Nodes {
		b := &m.Config.Controller.Nodes[i]
		match := rt.Type == model.RuntimeSwarm && b.NodeID == node && b.ClusterID == rt.ClusterID || rt.Type == model.RuntimeDocker && rt.Endpoint == "node://"+b.NodeID && (node == "" || node == b.NodeID)
		if match {
			return b, nil
		}
	}
	if rt.Type == model.RuntimeSwarm && !strings.HasPrefix(rt.Endpoint, "node://") {
		return nil, nil
	}
	return nil, ErrIdentity
}

func (m Manager) Do(ctx context.Context, req Request) (Result, error) {
	rt, err := m.Store.GetRuntime(ctx, req.RuntimeID)
	if err != nil {
		return Result{}, err
	}
	if !rt.Enabled || rt.Type != model.RuntimeDocker && rt.Type != model.RuntimeSwarm {
		return Result{}, ErrIdentity
	}
	if (req.Action == "create" || req.Action == "delete") && (!rt.ManagementEnabled || !m.Config.Management.Enabled) {
		return Result{}, ErrIdentity
	}
	if err = m.operation(ctx, req, ""); err != nil {
		return Result{}, err
	}
	if (req.Action == "create" || req.Action == "delete") && (req.EngineID == "" || req.NodeID == "") {
		return Result{}, ErrIdentity
	}
	if req.Action == "delete" && (!req.AcknowledgeDataLoss || req.Name == "" || req.Version == "") {
		return Result{}, storage.ErrPrecondition
	}
	b, err := m.binding(rt, req.NodeID)
	if err != nil {
		return Result{}, err
	}
	if b != nil && (req.Action == "create" || req.Action == "delete") {
		target, bound, e := execution.BoundTarget(ctx, req.OperationID, rt.ID)
		if e != nil {
			return Result{}, e
		}
		if !bound || target.NodeID != req.NodeID || target.EngineID != req.EngineID || target.InstanceID != m.Config.Controller.InstanceID {
			return Result{}, ErrIdentity
		}
	}
	var result Result
	if b != nil && b.InstanceID != m.Config.Controller.InstanceID {
		err = dockerresource.Forward(ctx, m.Config, *b, "/internal/v1/volumes", req, &result)
		var remote dockerresource.RemoteError
		if errors.As(err, &remote) {
			switch remote.Code {
			case "volume.identity":
				err = ErrIdentity
			case "volume.blocked":
				err = ErrBlocked
			case "volume.conflict":
				err = storage.ErrConflict
			case "volume.precondition":
				err = storage.ErrPrecondition
			case "volume.not_found":
				err = storage.ErrNotFound
			}
		}
	} else {
		result, err = m.local(ctx, rt, req, b)
	}
	if err != nil && !errors.Is(err, storage.ErrNotFound) && (req.Action == "list" || req.Action == "inspect") && b != nil && m.ReadNode != nil {
		// A retained observation is display-only and never authorizes mutations.
		report, readErr := m.ReadNode(ctx, b.NodeID)
		if (readErr == nil || errors.Is(readErr, nodecontrol.ErrStale) || errors.Is(readErr, nodecontrol.ErrIncomplete)) && report.NodeID == b.NodeID && report.EngineID == b.EngineID && report.Observation.VolumeDetails != nil {
			result = Result{NodeID: b.NodeID, EngineID: b.EngineID, ObservedAt: report.Observation.At, Items: []Item{}}
			for _, v := range report.Observation.VolumeDetails {
				if req.Action == "list" || req.Name == v.Name {
					result.Items = append(result.Items, Item{VolumeInfo: v, NodeID: b.NodeID, EngineID: b.EngineID, UsedBy: []Reference{}, BlockedReasons: []string{"inventory_unknown"}})
				}
			}
			if req.Action == "inspect" && len(result.Items) == 0 {
				return Result{}, storage.ErrNotFound
			}
			sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Name < result.Items[j].Name })
			return result, nil
		}
	}
	return result, err
}

func (m Manager) local(ctx context.Context, rt model.Runtime, req Request, b *config.ControllerNodeConfig) (Result, error) {
	endpoint := rt.Endpoint
	if b != nil {
		if err := (dockerresource.Manager{Store: m.Store}).CheckNodeOwner(ctx, *b); err != nil {
			return Result{}, err
		}
		endpoint = "unix:///var/run/docker.sock"
	}
	files := engine.TLSFiles{}
	if rt.CredentialRef != "" && b == nil {
		credential, ok := m.Config.Credentials[rt.CredentialRef]
		if !ok {
			return Result{}, ErrIdentity
		}
		files = engine.TLSFiles{CA: credential.CAFile, Certificate: credential.CertFile, Key: credential.KeyFile}
	}
	open := m.Open
	if open == nil {
		open = engine.NewManagementClient
	}
	cli, err := open(model.Runtime{Endpoint: endpoint}, files)
	if err != nil {
		return Result{}, err
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return Result{}, err
	}
	node := info.Info.Swarm.NodeID
	if node == "" {
		node = info.Info.ID
	}
	if b != nil {
		node = b.NodeID
	}
	if info.Info.ID == "" || req.EngineID != "" && req.EngineID != info.Info.ID || req.NodeID != "" && req.NodeID != node {
		return Result{}, ErrIdentity
	}
	if b != nil && (info.Info.ID != b.EngineID || rt.Type == model.RuntimeSwarm && info.Info.Swarm.NodeID != b.NodeID) {
		return Result{}, ErrIdentity
	}
	result := Result{NodeID: node, EngineID: info.Info.ID, NodeName: info.Info.Name, ObservedAt: time.Now().UTC(), Complete: true, Items: []Item{}}
	// Consumer evidence is fetched live from the provisioned manager. Reports
	// locate the manager but never authorize removal from a worker node.
	if req.Action == "consumers" {
		if rt.Type != model.RuntimeSwarm || !info.Info.Swarm.ControlAvailable || info.Info.Swarm.Cluster == nil || info.Info.Swarm.Cluster.ID != rt.ClusterID {
			return Result{}, ErrIdentity
		}
		services, e := cli.ServiceList(ctx, client.ServiceListOptions{})
		if e != nil {
			return Result{}, e
		}
		nodes, e := cli.NodeList(ctx, client.NodeListOptions{})
		if e != nil {
			return Result{}, e
		}
		result.Services = services.Items
		result.Nodes = nodes.Items
		return result, nil
	}
	var services []swarm.Service
	// A Docker alias must not bypass consumers on an actual Swarm engine.
	if rt.Type == model.RuntimeDocker && info.Info.Swarm.NodeID != "" {
		if !info.Info.Swarm.ControlAvailable || info.Info.Swarm.Cluster == nil {
			if req.Action == "target" || req.Action == "create" {
				return Result{}, ErrIdentity
			}
			result.Complete = false
		} else {
			rt.ClusterID = info.Info.Swarm.Cluster.ID
		}
	}
	if rt.Type == model.RuntimeSwarm || rt.Type == model.RuntimeDocker && info.Info.Swarm.ControlAvailable {
		var e error
		services, e = m.services(ctx, rt, cli, info.Info, node, req.Action == "target" || req.Action == "create")
		if e != nil {
			if req.Action != "list" && req.Action != "inspect" {
				return Result{}, e
			}
			result.Complete = false
		}
	}
	switch req.Action {
	case "target":
		return result, nil
	case "create":
		return m.create(ctx, cli, req, result)
	case "list", "inspect", "delete":
		return m.inventory(ctx, cli, rt, req, services, result)
	default:
		return Result{}, storage.ErrConflict
	}
}

func (m Manager) services(ctx context.Context, rt model.Runtime, cli *client.Client, info system.Info, node string, active bool) ([]swarm.Service, error) {
	var services []swarm.Service
	var nodes []swarm.Node
	if info.Swarm.ControlAvailable && info.Swarm.Cluster != nil && info.Swarm.Cluster.ID == rt.ClusterID {
		listed, err := cli.ServiceList(ctx, client.ServiceListOptions{})
		if err != nil {
			return nil, err
		}
		services = listed.Items
		listedNodes, err := cli.NodeList(ctx, client.NodeListOptions{})
		if err != nil {
			return nil, err
		}
		nodes = listedNodes.Items
	} else {
		if m.ReadNode == nil {
			return nil, ErrIdentity
		}
		found := false
		for _, b := range m.Config.Controller.Nodes {
			if b.ClusterID != rt.ClusterID {
				continue
			}
			r, err := m.ReadNode(ctx, b.NodeID)
			if err == nil && r.NodeID == b.NodeID && r.EngineID == b.EngineID && r.Observation.Info.Swarm.ControlAvailable && r.Observation.Info.Swarm.Cluster != nil && r.Observation.Info.Swarm.Cluster.ID == rt.ClusterID {
				var current Result
				if err := dockerresource.Forward(ctx, m.Config, b, "/internal/v1/volumes", Request{RuntimeID: rt.ID, NodeID: b.NodeID, EngineID: b.EngineID, Action: "consumers"}, &current); err != nil {
					return nil, err
				}
				if current.NodeID != b.NodeID || current.EngineID != b.EngineID || !current.Complete {
					return nil, ErrIdentity
				}
				services = current.Services
				nodes = current.Nodes
				found = true
				break
			}
		}
		if !found {
			return nil, ErrIdentity
		}
	}
	for _, n := range nodes {
		if n.ID == node && n.Status.State == swarm.NodeStateReady && (!active || n.Spec.Availability == swarm.NodeAvailabilityActive) {
			return services, nil
		}
	}
	return nil, ErrIdentity
}

func (m Manager) create(ctx context.Context, cli *client.Client, req Request, result Result) (Result, error) {
	if req.OperationID == "" || req.EngineID == "" {
		return Result{}, ErrIdentity
	}
	listed, err := cli.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return Result{}, err
	}
	var recovered *volume.Volume
	kind := "named"
	if req.Name == "" {
		kind = "anonymous"
	}
	for _, v := range listed.Items {
		same := v.Labels[labelOperation] == req.OperationID && v.Labels[labelRuntime] == req.RuntimeID && v.Labels[labelEngine] == req.EngineID && v.Labels[labelKind] == kind
		if same {
			if recovered != nil || req.Name != "" && v.Name != req.Name {
				return Result{}, ErrIdentity
			}
			copy := v
			recovered = &copy
		} else if req.Name != "" && v.Name == req.Name {
			return Result{}, storage.ErrConflict
		}
	}
	if recovered != nil {
		result.Name = recovered.Name
		result.Version = nodecontrol.VolumeMetadata(*recovered).Version
		return result, nil
	}
	if err := execution.BeforeMutation(ctx, req.OperationID, req.RuntimeID); err != nil {
		return Result{}, err
	}
	created, err := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: req.Name, Driver: "local", Labels: map[string]string{labelOperation: req.OperationID, labelRuntime: req.RuntimeID, labelEngine: req.EngineID, labelKind: kind}})
	if err != nil {
		return Result{}, err
	}
	if created.Volume.Name == "" || req.Name != "" && created.Volume.Name != req.Name || created.Volume.Driver != "local" || created.Volume.Scope != "local" || len(created.Volume.Options) != 0 {
		return Result{}, ErrIdentity
	}
	if created.Volume.Labels[labelOperation] != req.OperationID || created.Volume.Labels[labelRuntime] != req.RuntimeID || created.Volume.Labels[labelEngine] != req.EngineID || created.Volume.Labels[labelKind] != kind {
		return Result{}, storage.ErrConflict
	}
	result.Name = created.Volume.Name
	result.Version = nodecontrol.VolumeMetadata(created.Volume).Version
	return result, nil
}

func (m Manager) inventory(ctx context.Context, cli *client.Client, rt model.Runtime, req Request, services []swarm.Service, result Result) (Result, error) {
	var volumes []volume.Volume
	if req.Action == "list" {
		listed, err := cli.VolumeList(ctx, client.VolumeListOptions{})
		if err != nil {
			return Result{}, err
		}
		volumes = listed.Items
	} else {
		if req.Name == "" {
			return Result{}, storage.ErrConflict
		}
		inspected, err := cli.VolumeInspect(ctx, req.Name, client.VolumeInspectOptions{})
		if errdefs.IsNotFound(err) {
			if req.Action == "delete" {
				result.Name = req.Name
				return result, nil
			}
			return Result{}, storage.ErrNotFound
		}
		if err != nil {
			return Result{}, err
		}
		volumes = []volume.Volume{inspected.Volume}
	}
	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return Result{}, err
	}
	inspections := []container.InspectResponse{}
	for _, c := range containers.Items {
		inspected, err := cli.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
		if err != nil {
			result.Complete = false
			continue
		}
		inspections = append(inspections, inspected.Container)
	}
	refs, err := m.applicationReferences(ctx, rt, req, result.NodeID)
	if err != nil {
		return Result{}, err
	}
	for _, v := range volumes {
		owned, err := m.owned(ctx, v, rt.ID, result.NodeID, result.EngineID)
		if err != nil {
			return Result{}, err
		}
		item := Item{VolumeInfo: nodecontrol.VolumeMetadata(v), NodeID: result.NodeID, EngineID: result.EngineID, Owned: owned, UsedBy: []Reference{}, BlockedReasons: []string{}}
		if v.Driver != "local" || v.Scope != "local" {
			item.BlockedReasons = append(item.BlockedReasons, "unsupported_driver")
		}
		if len(v.Options) > 0 {
			item.BlockedReasons = append(item.BlockedReasons, "driver_options")
		}
		if _, e := time.Parse(time.RFC3339Nano, v.CreatedAt); e != nil {
			item.BlockedReasons = append(item.BlockedReasons, "identity_unknown")
		}
		if !result.Complete {
			item.BlockedReasons = append(item.BlockedReasons, "inventory_unknown")
		}
		if !rt.ManagementEnabled || !m.Config.Management.Enabled {
			item.BlockedReasons = append(item.BlockedReasons, "management_disabled")
		}
		for _, c := range inspections {
			for _, mount := range c.Mounts {
				if string(mount.Type) != "volume" || mount.Name != v.Name {
					continue
				}
				state := "unknown"
				if c.State != nil {
					state = string(c.State.Status)
				}
				item.UsedBy = append(item.UsedBy, Reference{Kind: "container", ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Target: mount.Destination, State: state})
				if item.Kind == "unknown" {
					item.Kind = mountKind(c, v.Name, mount.Destination)
				}
			}
		}
		for _, s := range services {
			if s.Spec.TaskTemplate.ContainerSpec == nil || !serviceCanUseNode(s, result.NodeID) {
				continue
			}
			for _, mount := range s.Spec.TaskTemplate.ContainerSpec.Mounts {
				if string(mount.Type) == "volume" && mount.Source != "" && (mount.Source == v.Name || strings.Contains(mount.Source, "{{")) {
					item.UsedBy = append(item.UsedBy, Reference{Kind: "service", ID: s.ID, Name: s.Spec.Name, Target: mount.Target})
				}
			}
		}
		for _, r := range refs {
			if r.volume == "*" || r.volume == v.Name || r.project != "" && (v.Labels["com.docker.compose.project"] == r.project || v.Labels["com.docker.stack.namespace"] == r.project) {
				item.UsedBy = append(item.UsedBy, r.reference)
			}
		}
		if len(item.UsedBy) > 0 {
			item.BlockedReasons = append(item.BlockedReasons, "referenced")
		}
		sort.Slice(item.UsedBy, func(i, j int) bool {
			return item.UsedBy[i].Kind+item.UsedBy[i].ID+item.UsedBy[i].Target < item.UsedBy[j].Kind+item.UsedBy[j].ID+item.UsedBy[j].Target
		})
		item.UsedBy = slices.Compact(item.UsedBy)
		result.Items = append(result.Items, item)
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Name < result.Items[j].Name })
	if req.Action == "delete" {
		if !req.AcknowledgeDataLoss || req.Version == "" {
			return Result{}, storage.ErrPrecondition
		}
		item := result.Items[0]
		if item.Version != req.Version {
			return Result{}, storage.ErrPrecondition
		}
		if len(item.BlockedReasons) > 0 {
			return Result{}, ErrBlocked
		}
		// Re-inspect after checking consumers; never request force removal.
		inspected, err := cli.VolumeInspect(ctx, req.Name, client.VolumeInspectOptions{})
		if err != nil {
			return Result{}, err
		}
		if nodecontrol.VolumeMetadata(inspected.Volume).Version != req.Version {
			return Result{}, storage.ErrPrecondition
		}
		if err := execution.BeforeMutation(ctx, req.OperationID, req.RuntimeID); err != nil {
			return Result{}, err
		}
		_, err = cli.VolumeRemove(ctx, req.Name, client.VolumeRemoveOptions{})
		if errdefs.IsConflict(err) {
			return Result{}, ErrBlocked
		}
		if err != nil && !errdefs.IsNotFound(err) {
			return Result{}, err
		}
		result.Name = req.Name
	}
	return result, nil
}

func serviceCanUseNode(s swarm.Service, node string) bool {
	if s.Spec.TaskTemplate.Placement != nil {
		for _, c := range s.Spec.TaskTemplate.Placement.Constraints {
			p := strings.Split(c, "==")
			if len(p) == 2 && strings.TrimSpace(p[0]) == "node.id" && strings.TrimSpace(p[1]) != node {
				return false
			}
			p = strings.Split(c, "!=")
			if len(p) == 2 && strings.TrimSpace(p[0]) == "node.id" && strings.TrimSpace(p[1]) == node {
				return false
			}
		}
	}
	return true
}
func (m Manager) owned(ctx context.Context, v volume.Volume, runtime, node, engineID string) (bool, error) {
	if v.Labels[labelRuntime] != runtime || v.Labels[labelEngine] != engineID || v.Labels[labelOperation] == "" {
		return false, nil
	}
	doc, err := m.Store.GetControlDocument(ctx, "volume-operation:"+v.Labels[labelOperation])
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var recorded struct {
		RuntimeID string `json:"runtime_id"`
		NodeID    string `json:"node_id"`
		EngineID  string `json:"engine_id"`
		Name      string `json:"name"`
		Version   string `json:"version"`
	}
	return doc.Kind == "volume-operation" && doc.ParentID == v.Labels[labelOperation] && json.Unmarshal(doc.Payload, &recorded) == nil && recorded.RuntimeID == runtime && recorded.NodeID == node && recorded.EngineID == engineID && recorded.Name == v.Name && recorded.Version == nodecontrol.VolumeMetadata(v).Version, nil
}
func mountKind(c container.InspectResponse, name, target string) string {
	if c.HostConfig == nil {
		return "unknown"
	}
	for _, m := range c.HostConfig.Mounts {
		if string(m.Type) == "volume" && m.Target == target {
			if m.Source == "" {
				return "anonymous"
			}
			if m.Source == name {
				return "named"
			}
		}
	}
	for _, b := range c.HostConfig.Binds {
		p := strings.Split(b, ":")
		if len(p) == 1 && p[0] == target {
			return "anonymous"
		}
		if len(p) >= 2 && p[0] == name && p[1] == target {
			return "named"
		}
	}
	return "unknown"
}

type applicationRef struct {
	volume, project string
	reference       Reference
}

func (m Manager) applicationReferences(ctx context.Context, rt model.Runtime, req Request, node string) ([]applicationRef, error) {
	refs := []applicationRef{}
	runtimes, err := m.runtimeScope(ctx, rt)
	if err != nil {
		return nil, err
	}
	for _, runtime := range runtimes {
		for after := ""; ; {
			apps, err := m.Store.ListApplications(ctx, runtime.ID, storage.Page{After: after, Limit: 200})
			if err != nil {
				return nil, err
			}
			for _, app := range apps {
				ref := Reference{Kind: "application", ID: app.ID, Name: app.Name, State: string(app.Status)}
				doc, err := m.Store.GetControlDocument(ctx, "application-retention:"+app.ID)
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					return nil, err
				}
				if err == nil {
					var retained struct {
						ApplicationID   string   `json:"application_id"`
						RuntimeID       string   `json:"runtime_id"`
						ExpectedNode    string   `json:"expected_node"`
						ExpectedVolumes []string `json:"expected_volumes"`
						Volumes         []struct {
							NodeID string `json:"node_id"`
							Name   string `json:"name"`
						}
					}
					if doc.Kind != "application-retention" || doc.ParentID != app.ID || json.Unmarshal(doc.Payload, &retained) != nil || retained.ApplicationID != app.ID || retained.RuntimeID != runtime.ID {
						return nil, ErrIdentity
					}
					for _, v := range retained.Volumes {
						if v.NodeID == node {
							refs = append(refs, applicationRef{volume: v.Name, reference: ref})
						}
					}
					if retained.ExpectedNode == node {
						for _, v := range retained.ExpectedVolumes {
							refs = append(refs, applicationRef{volume: v, reference: ref})
						}
					}
				}
				if app.ActiveRevisionID != "" {
					revision, err := m.Store.GetApplicationRevision(ctx, app.ActiveRevisionID)
					if err != nil {
						return nil, err
					}
					if revision.ApplicationID != app.ID || m.Keys == nil {
						return nil, ErrIdentity
					}
					if runtime.Type == model.RuntimeSwarm && revision.ConfigurationSource.DeploymentPlan.DataNodeID != "" && revision.ConfigurationSource.DeploymentPlan.DataNodeID != node {
						continue
					}
					raw, _, err := m.Keys.Decrypt(revision.EncryptedResolved, []byte("application-revision:"+revision.ID+":resolved"))
					if err != nil {
						return nil, ErrIdentity
					}
					names, err := applicationx.ManagedVolumeNames(string(raw), app.ProjectName)
					clear(raw)
					if err != nil {
						return nil, ErrIdentity
					}
					for _, name := range names {
						refs = append(refs, applicationRef{volume: name, reference: ref})
					}
				}
				refs = append(refs, applicationRef{project: app.ProjectName, reference: ref})
			}
			if len(apps) < 200 {
				break
			}
			after = apps[len(apps)-1].ID
		}
	}
	ids := []string{}
	for _, r := range runtimes {
		ids = append(ids, r.ID)
	}
	for after := ""; ; {
		ops, err := m.Store.ListDeploymentOperations(ctx, "", storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			if op.ID != req.OperationID && slices.Contains(ids, op.RuntimeID) && (op.Status == model.OperationQueued || op.Status == model.OperationRunning) && op.Type != "VOLUME_CREATE" && op.Type != "VOLUME_DELETE" && op.Type != "DOCKER_RESOURCE_CREATE" && op.Type != "DOCKER_RESOURCE_DELETE" {
				refs = append(refs, applicationRef{volume: "*", reference: Reference{Kind: "operation", ID: op.ID, Name: op.Type, State: string(op.Status)}})
			}
		}
		if len(ops) < 200 {
			break
		}
		after = ops[len(ops)-1].ID
	}
	return refs, nil
}
func (m Manager) runtimeScope(ctx context.Context, rt model.Runtime) ([]model.Runtime, error) {
	values := []model.Runtime{}
	for after := ""; ; {
		rows, err := m.Store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.ID == rt.ID || rt.ClusterID != "" && r.ClusterID == rt.ClusterID || rt.Type == model.RuntimeDocker && r.Type == model.RuntimeDocker && r.Endpoint == rt.Endpoint {
				values = append(values, r)
			}
		}
		if len(rows) < 200 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	return values, nil
}

func (m Manager) InternalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		peer, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		known := false
		for _, b := range m.Config.Controller.Nodes {
			if b.InstanceID == peer {
				known = true
			}
		}
		if !known {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var req Request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rt, err := m.Store.GetRuntime(r.Context(), req.RuntimeID)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		b, err := m.binding(rt, req.NodeID)
		if err != nil || b == nil || b.InstanceID != m.Config.Controller.InstanceID || !rt.Enabled || (req.Action == "create" || req.Action == "delete") && (!rt.ManagementEnabled || !m.Config.Management.Enabled) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if req.Action == "create" || req.Action == "delete" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if m.operation(r.Context(), req, peer) != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		result, err := m.local(r.Context(), rt, req, b)
		if err != nil {
			code := ""
			status := http.StatusServiceUnavailable
			switch {
			case errors.Is(err, ErrIdentity):
				code = "volume.identity"
			case errors.Is(err, ErrBlocked):
				code = "volume.blocked"
				status = http.StatusConflict
			case errors.Is(err, storage.ErrPrecondition):
				code = "volume.precondition"
				status = http.StatusPreconditionFailed
			case errors.Is(err, storage.ErrConflict):
				code = "volume.conflict"
				status = http.StatusConflict
			case errors.Is(err, storage.ErrNotFound):
				code = "volume.not_found"
				status = http.StatusNotFound
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
