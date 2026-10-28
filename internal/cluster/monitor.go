package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var ErrUnavailable = errors.New("cluster inventory unavailable")
var ErrReadOnly = errors.New("cluster labels are read-only")

type SourceStore interface {
	ListRuntimes(context.Context, storage.Page) ([]model.Runtime, error)
	GetRuntime(context.Context, string) (model.Runtime, error)
	ListServicesFiltered(context.Context, storage.ServiceFilter, storage.Page) ([]model.Service, error)
}
type Factory func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error)
type source struct {
	Host       *hoststats.Metrics
	Health     *hoststats.Health
	Info       system.Info
	Nodes      []swarm.Node
	Services   []swarm.Service
	Tasks      []swarm.Task
	Containers []container.Summary
	At         time.Time
	Error      string
}
type containerDetail struct {
	Health, Error string
	CreatedAt     *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	RestartCount  *int
	ExitCode      *int
	OOMKilled     *bool
	At            time.Time
}
type Monitor struct {
	details    map[string]containerDetail
	store      SourceStore
	config     config.Config
	factory    Factory
	now        func() time.Time
	refreshMu  sync.Mutex
	mu         sync.RWMutex
	sources    map[string]source
	runtimes   map[string]model.Runtime
	references map[string]map[string]string
	metrics    map[string]*series
}

func New(store SourceStore, cfg config.Config) *Monitor {
	return NewWithFactory(store, cfg, engine.NewMonitoringClient)
}
func NewWithFactory(store SourceStore, cfg config.Config, factory Factory) *Monitor {
	return &Monitor{store: store, config: cfg, factory: factory, now: func() time.Time { return time.Now().UTC() }, sources: map[string]source{}, runtimes: map[string]model.Runtime{}, references: map[string]map[string]string{}, metrics: map[string]*series{}, details: map[string]containerDetail{}}
}
func connection(rt model.Runtime) string {
	if strings.HasPrefix(rt.Endpoint, "node://") {
		return string(rt.Type) + "\x00" + rt.Endpoint
	}
	return rt.Endpoint + "\x00" + rt.CredentialRef
}
func (m *Monitor) client(rt model.Runtime) (engine.MonitoringClient, error) {
	var files engine.TLSFiles
	if rt.CredentialRef != "" {
		c, ok := m.config.Credentials[rt.CredentialRef]
		if !ok {
			return nil, fmt.Errorf("missing Docker credentials")
		}
		files = engine.TLSFiles{CA: c.CAFile, Certificate: c.CertFile, Key: c.KeyFile}
	}
	return m.factory(rt, files)
}
func (m *Monitor) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		_ = m.Refresh(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Refresh is shared by all sessions; collection never occurs in HTTP handlers.
func (m *Monitor) Refresh(ctx context.Context) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	runtimes := map[string]model.Runtime{}
	connections := map[string]model.Runtime{}
	refs := map[string]map[string]string{}
	for after := ""; ; {
		rows, err := m.store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return err
		}
		for _, rt := range rows {
			runtimes[rt.ID] = rt
			if rt.Enabled {
				connections[connection(rt)] = rt
			}
		}
		if len(rows) < 200 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	for id := range runtimes {
		refs[id] = map[string]string{}
		for after := ""; ; {
			rows, err := m.store.ListServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: id}, storage.Page{After: after, Limit: 200})
			if err != nil {
				break
			}
			for _, s := range rows {
				refs[id][s.ServiceKey] = s.ID
				if s.RuntimeObjectID != "" {
					refs[id][s.RuntimeObjectID] = s.ID
				}
			}
			if len(rows) < 200 {
				break
			}
			after = rows[len(rows)-1].ID
		}
	}
	m.mu.RLock()
	old := make(map[string]source, len(m.sources))
	for k, v := range m.sources {
		old[k] = v
	}
	m.mu.RUnlock()
	type collected struct {
		key    string
		value  source
		client engine.MonitoringClient
	}
	keys := make([]string, 0, len(connections))
	for k := range connections {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	results := make([]collected, len(keys))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			cli, err := m.client(connections[key])
			v := source{}
			if err == nil {
				callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				v.Info, err = cli.Info(callCtx)
				if err == nil {
					v.Containers, err = cli.ContainerList(callCtx)
				}
				if err == nil && v.Info.Swarm.ControlAvailable {
					v.Nodes, err = cli.NodeList(callCtx)
					if err == nil {
						v.Services, err = cli.ServiceList(callCtx)
					}
					if err == nil {
						v.Tasks, err = cli.TaskList(callCtx)
					}
				}
				cancel()
			}
			if err != nil {
				if cli != nil {
					_ = cli.Close()
					cli = nil
				}
				v = old[key]
				v.Error = "collection_failed"
			} else {
				v.At = m.now()
				if host, ok := cli.(interface {
					HostObservation() (*hoststats.Metrics, *hoststats.Health)
				}); ok {
					v.Host, v.Health = host.HostObservation()
				}
				if observed, ok := cli.(interface{ ObservedAt() time.Time }); ok {
					v.At = observed.ObservedAt()
				}
				if observed, ok := cli.(interface{ ObservationError() error }); ok && observed.ObservationError() != nil {
					v.Error = "collection_failed"
				}
			}
			results[i] = collected{key, v, cli}
		}(i, key)
	}
	wg.Wait()
	next := map[string]source{}
	// Different configured endpoints can connect to the same Engine. Sample it once.
	engines := map[string]collected{}
	for _, r := range results {
		if r.key == "" {
			continue
		}
		next[r.key] = r.value
		if r.client != nil {
			defer r.client.Close()
			if r.value.Info.ID != "" {
				if _, ok := engines[r.value.Info.ID]; !ok {
					engines[r.value.Info.ID] = r
				}
			}
		}
	}
	m.mu.Lock()
	// A label write can complete during a collection cycle. Retain newer specs.
	for key, v := range next {
		current := m.sources[key]
		for i, n := range v.Nodes {
			for _, fresh := range current.Nodes {
				if fresh.ID == n.ID && fresh.Version.Index > n.Version.Index {
					v.Nodes = append([]swarm.Node(nil), v.Nodes...)
					v.Nodes[i].Spec = fresh.Spec
					v.Nodes[i].Version = fresh.Version
				}
			}
		}
		next[key] = v
	}
	m.sources = next
	m.runtimes = runtimes
	m.references = refs
	m.prune(m.now())
	// An unavailable daemon cannot produce per-container errors. Record the gap
	// here so old CPU counters and last-good values are never considered current.
	for key, s := range m.metrics {
		engineID, _, _ := strings.Cut(key, "/")
		if _, ok := engines[engineID]; !ok {
			s.Previous = nil
			s.append(Metric{At: m.now(), Error: "collection_failed"}, m.now())
		}
	}
	m.mu.Unlock()
	type job struct {
		running bool
		engine  string
		id      string
		cli     engine.MonitoringClient
	}
	jobs := []job{}
	engineKeys := make([]string, 0, len(engines))
	for k := range engines {
		engineKeys = append(engineKeys, k)
	}
	sort.Strings(engineKeys)
	for _, engineID := range engineKeys {
		r := engines[engineID]
		for _, c := range r.value.Containers {
			jobs = append(jobs, job{string(c.State) == "running", engineID, c.ID, r.client})
		}
	}
	// Bound each cycle, including a daemon that never responds. Eight workers share
	// the deadline; skipped samples remain explicit gaps instead of fabricated zeroes.
	cycle, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	queue := make(chan job)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				call, done := context.WithTimeout(cycle, 3*time.Second)
				inspection, inspectErr := j.cli.ContainerInspect(call, j.id)
				detail := containerDetail{At: m.now()}
				if inspectErr == nil {
					detail.CreatedAt = parseDockerTime(inspection.Created)
					detail.RestartCount = pointer(inspection.RestartCount)
				}
				if inspectErr == nil && inspection.State != nil {
					detail.Error = inspection.State.Error
					detail.StartedAt = parseDockerTime(inspection.State.StartedAt)
					detail.FinishedAt = parseDockerTime(inspection.State.FinishedAt)
					detail.ExitCode = pointer(inspection.State.ExitCode)
					detail.OOMKilled = pointer(inspection.State.OOMKilled)
					detail.Health = "none"
					if inspection.State.Health != nil {
						h := inspection.State.Health
						detail.Health = string(h.Status)
						if detail.Error == "" && h.Status == container.Unhealthy && len(h.Log) > 0 && h.Log[len(h.Log)-1] != nil {
							detail.Error = h.Log[len(h.Log)-1].Output
							if len(detail.Error) > 4096 {
								detail.Error = detail.Error[:4096]
							}
						}
					}
				}
				var v container.StatsResponse
				var err error
				if j.running {
					v, err = j.cli.ContainerStats(call, j.id)
				}
				done()
				now := m.now()
				if observed, ok := j.cli.(interface{ ObservedAt() time.Time }); ok {
					now = observed.ObservedAt()
				}
				detail.At = now
				key := j.engine + "/" + j.id
				m.mu.Lock()
				if inspectErr == nil {
					if len(m.details) >= 4096 {
						for k := range m.details {
							delete(m.details, k)
							break
						}
					}
					m.details[key] = detail
				}
				if !j.running {
					m.mu.Unlock()
					continue
				}
				s := m.metrics[key]
				if s == nil {
					m.makeRoom()
					s = &series{}
					m.metrics[key] = s
				}
				p := Metric{At: now, Error: "collection_failed"}
				if !s.LastAttempt.Before(now) {
					m.mu.Unlock()
					continue
				}
				if err == nil {
					p = sample(v, s.Previous, now)
					s.Previous = &v
				} else {
					s.Previous = nil
				}
				s.append(p, now)
				m.mu.Unlock()
			}
		}()
	}
	// Rotate sampling order so an overloaded endpoint cannot starve later IDs.
	if len(jobs) > 0 {
		offset := int(m.now().Unix()/10) % len(jobs)
		jobs = append(jobs[offset:], jobs[:offset]...)
	}
loop:
	for _, j := range jobs {
		select {
		case queue <- j:
		case <-cycle.Done():
			break loop
		}
	}
	close(queue)
	wg.Wait()
	return nil
}
func (m *Monitor) prune(now time.Time) {
	for k, d := range m.details {
		if now.Sub(d.At) > retention {
			delete(m.details, k)
		}
	}
	for k, s := range m.metrics {
		if now.Sub(s.LastAttempt) > retention {
			delete(m.metrics, k)
		}
	}
}
func (m *Monitor) makeRoom() {
	if len(m.metrics) < maxSeries {
		return
	}
	oldest := ""
	var at time.Time
	for k, s := range m.metrics {
		if oldest == "" || s.LastAttempt.Before(at) {
			oldest = k
			at = s.LastAttempt
		}
	}
	delete(m.metrics, oldest)
}

// Labels are always inspected fresh. A racing docker node update must fail rather
// than overwrite either labels or unrelated role/availability changes.
func (m *Monitor) UpdateLabels(ctx context.Context, runtimeID, nodeID string, expected uint64, labels map[string]string) (Node, error) {
	rt, err := m.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return Node{}, err
	}
	if !m.config.Management.Enabled || !rt.ManagementEnabled || !rt.Enabled || rt.Type != model.RuntimeSwarm {
		return Node{}, ErrReadOnly
	}
	cli, err := m.client(rt)
	if err != nil {
		return Node{}, err
	}
	defer cli.Close()
	call, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	info, err := cli.Info(call)
	if err != nil {
		return Node{}, err
	}
	if !info.Swarm.ControlAvailable {
		return Node{}, ErrReadOnly
	}
	n, err := cli.NodeInspect(call, nodeID)
	if err != nil {
		return Node{}, dockerError(err)
	}
	if n.Version.Index != expected {
		return Node{}, storage.ErrPrecondition
	}
	n.Spec.Labels = labels
	if err = cli.NodeUpdate(call, nodeID, n.Version, n.Spec); err != nil {
		// Engines may return InvalidArgument for "update out of sequence". Re-inspect
		// only to classify the conflict; never retry the mutation.
		fresh, e := cli.NodeInspect(call, nodeID)
		if errdefs.IsConflict(err) || (e == nil && fresh.Version.Index != expected) {
			return Node{}, storage.ErrPrecondition
		}
		return Node{}, dockerError(err)
	}
	n, err = cli.NodeInspect(call, nodeID)
	if err != nil {
		return Node{}, dockerError(err)
	}
	result := swarmNode(n, m.now())
	result.CanEditLabels = true
	// Refresh cached labels immediately without overwriting newer versions.
	m.mu.Lock()
	for key, v := range m.sources {
		for i, old := range v.Nodes {
			if old.ID == nodeID && old.Version.Index <= n.Version.Index {
				nodes := append([]swarm.Node(nil), v.Nodes...)
				nodes[i] = n
				v.Nodes = nodes
				m.sources[key] = v
			}
		}
	}
	m.mu.Unlock()
	return result, nil
}
func dockerError(err error) error {
	if errdefs.IsNotFound(err) {
		return storage.ErrNotFound
	}
	return err
}
func nodeID(info system.Info) string {
	if info.Swarm.NodeID != "" {
		return info.Swarm.NodeID
	}
	return "engine:" + info.ID
}
func swarmNode(n swarm.Node, at time.Time) Node {
	v := Node{ID: n.ID, Name: n.Description.Hostname, Role: string(n.Spec.Role), State: string(n.Status.State), Availability: string(n.Spec.Availability), EngineVersion: n.Description.Engine.EngineVersion, Address: n.Status.Addr, CPUs: float64(n.Description.Resources.NanoCPUs) / 1e9, MemoryBytes: n.Description.Resources.MemoryBytes, Labels: n.Spec.Labels, EngineLabels: n.Description.Engine.Labels, Version: n.Version.Index, ObservedAt: at, Error: n.Status.Message}
	if v.Labels == nil {
		v.Labels = map[string]string{}
	}
	if v.EngineLabels == nil {
		v.EngineLabels = map[string]string{}
	}
	if n.ManagerStatus != nil {
		v.Leader = n.ManagerStatus.Leader
		v.Reachability = string(n.ManagerStatus.Reachability)
		if address, err := netip.ParseAddr(v.Address); v.Address == "" || (err == nil && address.IsUnspecified()) {
			if host, _, err := net.SplitHostPort(n.ManagerStatus.Addr); err == nil {
				v.Address = host
			}
		}
	}
	return v
}
func labels(values []string) map[string]string {
	m := map[string]string{}
	for _, v := range values {
		k, s, _ := strings.Cut(v, "=")
		m[k] = s
	}
	return m
}
