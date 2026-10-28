// Package nodecontrol authenticates node-local observations and execution.
package nodecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var ErrIdentity = errors.New("node identity mismatch")
var ErrStale = errors.New("node observation is stale")
var ErrIncomplete = errors.New("node observation is incomplete")

// Bindings are operator-provisioned from each node's local engine inspection.
// A report cannot enroll itself or change its node/engine ownership.
type Binding struct {
	InstanceID string `json:"instance_id" yaml:"instance_id"`
	NodeID     string `json:"node_id" yaml:"node_id"`
	EngineID   string `json:"engine_id" yaml:"engine_id"`
	ClusterID  string `json:"cluster_id" yaml:"cluster_id"`
	URL        string `json:"url" yaml:"url"`
}

type Observation struct {
	VolumeDetails           []VolumeInfo                         `json:"volume_details,omitempty"`
	VolumeInventoryComplete bool                                 `json:"volume_inventory_complete"`
	StorageComplete         bool                                 `json:"storage_complete"`
	VolumeOwners            map[string]string                    `json:"volume_owners,omitempty"`
	Volumes                 map[string]time.Time                 `json:"volumes,omitempty"`
	ImageVolumes            map[string][]string                  `json:"image_volumes,omitempty"`
	Host                    *hoststats.Metrics                   `json:"host,omitempty"`
	Health                  *hoststats.Health                    `json:"health,omitempty"`
	Info                    system.Info                          `json:"info"`
	Containers              []container.Summary                  `json:"containers"`
	Nodes                   []swarm.Node                         `json:"nodes"`
	Services                []swarm.Service                      `json:"services"`
	Tasks                   []swarm.Task                         `json:"tasks"`
	Networks                []network.Summary                    `json:"networks"`
	Inspections             map[string]container.InspectResponse `json:"inspections"`
	Stats                   map[string]container.StatsResponse   `json:"stats"`
	Errors                  map[string]string                    `json:"errors"`
	At                      time.Time                            `json:"observed_at"`
	Complete                bool                                 `json:"complete"`
}

type Report struct {
	NodeID      string      `json:"node_id"`
	EngineID    string      `json:"engine_id"`
	Generation  int64       `json:"generation"`
	Sequence    uint64      `json:"sequence"`
	Observation Observation `json:"observation"`
}

type Session struct {
	Generation int64  `json:"generation"`
	Sequence   uint64 `json:"sequence"`
	Owner      int64  `json:"owner"`
	BootID     string `json:"boot_id"`
}

// EventBatch is an acknowledged, bounded allowlist of Docker event evidence.
// Generation and current database lock ownership are checked on every write.
type EventBatch struct {
	NodeID     string                  `json:"node_id"`
	EngineID   string                  `json:"engine_id"`
	Generation int64                   `json:"generation"`
	Events     []model.DiscoverySignal `json:"events"`
	State      string                  `json:"state,omitempty"`
	At         time.Time               `json:"at"`
}

type documentStore interface {
	GetControlDocument(context.Context, string) (storage.ControlDocument, error)
	WriteControlDocuments(context.Context, []storage.ControlWrite) error
	RecordRuntimeEvents(context.Context, model.RuntimeEventSource, []model.DiscoverySignal, time.Time) error
	MarkRuntimeEventSource(context.Context, model.RuntimeEventSource, string, time.Time) error
}

type Reports struct {
	Store         documentStore
	EventsEnabled bool
	Keys          *security.Keyring
	Bindings      map[string]Binding
	Now           func(context.Context) (time.Time, error)
	// CheckOwner verifies the sender still holds its per-node database session lock.
	CheckOwner func(context.Context, string, int64) error
}

func LockKey(node string) string    { return "controller:node:" + node }
func sessionKey(node string) string { return "node-session:" + node }
func reportKey(node string) string  { return "node-report:" + node }

func (s Reports) binding(instance, node, engine string) (Binding, error) {
	b, ok := s.Bindings[instance]
	if !ok || b.InstanceID != instance || b.NodeID == "" || b.EngineID == "" || b.NodeID != node || b.EngineID != engine {
		return Binding{}, ErrIdentity
	}
	return b, nil
}

func (s Reports) session(ctx context.Context, node string) (Session, storage.ControlDocument, error) {
	d, err := s.Store.GetControlDocument(ctx, sessionKey(node))
	if err != nil {
		return Session{}, d, err
	}
	if d.Kind != "node-session" || d.ParentID != node {
		return Session{}, d, ErrIdentity
	}
	var v Session
	err = json.Unmarshal(d.Payload, &v)
	return v, d, err
}

func write(key, kind, node string, value any, expected int64, now time.Time) (storage.ControlWrite, error) {
	raw, err := json.Marshal(value)
	return storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: node, Payload: raw, CreatedAt: now, UpdatedAt: now}, Expected: expected}, err
}

// Begin is idempotent for a process boot. A delayed boot cannot reclaim a
// generation after its database session lock has been lost.
func (s Reports) Begin(ctx context.Context, instance, node, engine, boot string, owner int64) (Session, error) {
	if _, err := s.binding(instance, node, engine); err != nil {
		return Session{}, err
	}
	if boot == "" || len(boot) > 128 || owner <= 0 || s.CheckOwner == nil {
		return Session{}, ErrIdentity
	}
	if err := s.CheckOwner(ctx, LockKey(node), owner); err != nil {
		return Session{}, err
	}
	now, err := s.Now(ctx)
	if err != nil {
		return Session{}, err
	}
	v, d, err := s.session(ctx, node)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Session{}, err
	}
	if err == nil && v.BootID == boot && v.Owner == owner {
		return v, nil
	}
	if v.Generation == math.MaxInt64 {
		return Session{}, ErrStale
	}
	v = Session{Generation: v.Generation + 1, Owner: owner, BootID: boot}
	w, err := write(sessionKey(node), "node-session", node, v, d.Revision, now)
	if err != nil {
		return Session{}, err
	}
	return v, s.Store.WriteControlDocuments(ctx, []storage.ControlWrite{w})
}

func (s Reports) Accept(ctx context.Context, instance string, value Report) error {
	binding, err := s.binding(instance, value.NodeID, value.EngineID)
	if err != nil {
		return err
	}
	v, sessionDoc, err := s.session(ctx, value.NodeID)
	if err != nil {
		return err
	}
	if value.Generation != v.Generation || value.Sequence == 0 || value.Sequence <= v.Sequence {
		return ErrStale
	}
	if s.CheckOwner == nil {
		return ErrIdentity
	}
	if err := s.CheckOwner(ctx, LockKey(value.NodeID), v.Owner); err != nil {
		return err
	}
	now, err := s.Now(ctx)
	if err != nil {
		return err
	}
	o := value.Observation
	if o.Health != nil && o.Health.InstanceID != instance {
		return ErrIdentity
	}
	if o.At.Before(now.Add(-30*time.Second)) || o.At.After(now.Add(5*time.Second)) {
		return ErrStale
	}
	if o.Info.ID != value.EngineID || o.Info.Swarm.NodeID != value.NodeID {
		return ErrIdentity
	}
	if o.Info.Swarm.Cluster != nil && binding.ClusterID != "" && o.Info.Swarm.Cluster.ID != binding.ClusterID {
		return ErrIdentity
	}
	// Collectors include their last inventory in incomplete reports. Readers
	// must still reject the completeness flag rather than treating it as current.
	d, err := s.Store.GetControlDocument(ctx, reportKey(value.NodeID))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err == nil && (d.Kind != "node-report" || d.ParentID != value.NodeID) {
		return ErrIdentity
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ciphertext, err := s.Keys.Encrypt(raw, []byte("control:"+reportKey(value.NodeID)))
	if err != nil {
		return err
	}
	envelope := struct {
		KeyID      string `json:"key_id"`
		Ciphertext []byte `json:"ciphertext"`
	}{s.Keys.PrimaryID(), ciphertext}
	w, err := write(reportKey(value.NodeID), "node-report", value.NodeID, envelope, d.Revision, now)
	if err != nil {
		return err
	}
	v.Sequence = value.Sequence
	guard, err := write(sessionKey(value.NodeID), "node-session", value.NodeID, v, sessionDoc.Revision, now)
	if err != nil {
		return err
	}
	return s.Store.WriteControlDocuments(ctx, []storage.ControlWrite{guard, w})
}

func (s Reports) AcceptEvents(ctx context.Context, instance string, batch EventBatch) error {
	if !s.EventsEnabled {
		return ErrIdentity
	}
	binding, err := s.binding(instance, batch.NodeID, batch.EngineID)
	if err != nil {
		return err
	}
	if len(batch.Events) > 200 || (batch.State != "" && batch.State != "stream_disconnected" && batch.State != "overflow" && batch.State != "upload_failed") ||
		(batch.State != "" && len(batch.Events) != 0) {
		return ErrIdentity
	}
	session, _, err := s.session(ctx, batch.NodeID)
	if err != nil {
		return err
	}
	if batch.Generation != session.Generation || s.CheckOwner == nil {
		return ErrStale
	}
	if err := s.CheckOwner(ctx, LockKey(batch.NodeID), session.Owner); err != nil {
		return err
	}
	now, err := s.Now(ctx)
	if err != nil {
		return err
	}
	if batch.At.Before(now.Add(-30*time.Second)) || batch.At.After(now.Add(5*time.Second)) {
		return ErrStale
	}
	for _, event := range batch.Events {
		if event.At.Before(now.Add(-24*time.Hour)) || event.At.After(now.Add(5*time.Second)) || len(event.ExternalID) > 255 ||
			len(event.ServiceKey) > 255 || len(event.ServiceObjectID) > 255 || len(event.Kind) > 80 ||
			(event.ActorType == "container" && event.NodeID != "" && event.NodeID != batch.NodeID) || event.RuntimeID != "" ||
			len(event.NodeID) > 255 || len(event.TaskID) > 255 || !allowedEventStatus(event.Status) {
			return ErrIdentity
		}
		if event.ActorType != "container" && event.ActorType != "task" && event.ActorType != "service" {
			return ErrIdentity
		}
	}
	source := model.RuntimeEventSource{ID: "node:" + binding.NodeID, ClusterID: binding.ClusterID, NodeID: binding.NodeID, EngineID: binding.EngineID}
	if batch.State != "" {
		return s.Store.MarkRuntimeEventSource(ctx, source, batch.State, now)
	}
	return s.Store.RecordRuntimeEvents(ctx, source, batch.Events, now)
}

func allowedEventStatus(value string) bool {
	switch value {
	case "", "running", "shutdown", "failed", "complete", "rejected", "pending", "preparing", "starting", "assigned", "accepted", "ready", "healthy", "unhealthy":
		return true
	default:
		return false
	}
}

func (s Reports) Read(ctx context.Context, node string) (Report, error) {
	return s.read(ctx, node, 2)
}

func (s Reports) read(ctx context.Context, node string, retries int) (Report, error) {
	d, err := s.Store.GetControlDocument(ctx, reportKey(node))
	if err != nil {
		return Report{}, err
	}
	if d.Kind != "node-report" || d.ParentID != node {
		return Report{}, ErrIdentity
	}
	var envelope struct {
		Ciphertext []byte `json:"ciphertext"`
	}
	if err = json.Unmarshal(d.Payload, &envelope); err != nil {
		return Report{}, err
	}
	raw, _, err := s.Keys.Decrypt(envelope.Ciphertext, []byte("control:"+d.ID))
	if err != nil {
		return Report{}, err
	}
	var v Report
	if err = json.Unmarshal(raw, &v); err != nil {
		return v, err
	}
	now, err := s.Now(ctx)
	if err != nil {
		return v, err
	}
	if v.NodeID != node {
		return Report{}, ErrIdentity
	}
	current, _, err := s.session(ctx, node)
	if err != nil {
		return v, err
	}
	// A collector publishes the report and sequence atomically. A reader can
	// still straddle that commit across its two reads; retry normal forward
	// progress instead of falsely fencing an otherwise healthy executor.
	if current.Generation == v.Generation && current.Sequence > v.Sequence && retries > 0 {
		return s.read(ctx, node, retries-1)
	}
	if current.Generation != v.Generation || current.Sequence != v.Sequence || now.Sub(v.Observation.At) > 30*time.Second || v.Observation.At.After(now.Add(5*time.Second)) {
		return v, ErrStale
	}
	if !v.Observation.Complete {
		return v, ErrIncomplete
	}
	return v, nil
}
