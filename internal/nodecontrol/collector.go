package nodecontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

// Collector runs on every Controller, independently of global leadership.
type Collector struct {
	Binding       Binding
	Locker        lease.Locker
	TLS           *tls.Config
	Open          func() (engine.MonitoringClient, error)
	Interval      time.Duration
	Host          *hoststats.Collector
	Health        func(context.Context) hoststats.Health
	EventsEnabled bool
	EventReport   func(string)
}

func (c *Collector) Run(ctx context.Context) error {
	if c.Interval == 0 {
		c.Interval = 5 * time.Second
	}
	if c.Interval <= 0 || c.Locker == nil || c.TLS == nil || c.Open == nil {
		return errors.New("invalid node collector configuration")
	}
	if err := controlplane.ValidateInternalURL(c.Binding.URL); err != nil {
		return err
	}
	for ctx.Err() == nil {
		held, err := c.Locker.TryLock(ctx, LockKey(c.Binding.NodeID))
		if err == nil {
			err = c.collectSession(ctx, held)
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = held.Release(cleanup)
			cancel()
			if errors.Is(err, ErrIdentity) {
				return err
			}
		}
		if !pause(ctx, c.Interval) {
			break
		}
	}
	return ctx.Err()
}

func pause(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Collector) collectSession(ctx context.Context, held lease.Lease) error {
	sessionLease, ok := held.(lease.SessionLease)
	if !ok {
		return errors.New("node reporting requires a shared database session lock")
	}
	owner, err := sessionLease.SessionID(ctx)
	if err != nil {
		return err
	}
	boot := make([]byte, 24)
	if _, err = rand.Read(boot); err != nil {
		return err
	}
	tlsConfig := c.TLS.Clone()
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if verify != nil {
			if err := verify(state); err != nil {
				return err
			}
		}
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return ErrIdentity
		}
		id, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil || id != c.Binding.InstanceID {
			return ErrIdentity
		}
		return nil
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := BeginRequest{NodeID: c.Binding.NodeID, EngineID: c.Binding.EngineID, BootID: hex.EncodeToString(boot), Owner: owner}
	var session Session
	if err := post(ctx, client, c.Binding.URL+"/internal/v1/node-sessions", request, &session); err != nil {
		return err
	}
	if c.EventsEnabled {
		eventCtx, stopEvents := context.WithCancel(ctx)
		eventDone := make(chan struct{})
		go func() { defer close(eventDone); c.collectEvents(eventCtx, client, session) }()
		defer func() { stopEvents(); <-eventDone }()
	}
	var previous Observation
	for ctx.Err() == nil {
		check, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = held.Check(check)
		cancel()
		if err != nil {
			return err
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		observation, collectErr := c.Collect(call)
		cancel()
		if errors.Is(collectErr, ErrIdentity) {
			return collectErr
		}
		if collectErr != nil {
			observation = previous
			observation.Info.ID = c.Binding.EngineID
			observation.Info.Swarm.NodeID = c.Binding.NodeID
			observation.Complete = false
			observation.Errors = map[string]string{"inventory": "collection_failed"}
		} else {
			previous = observation
		}
		// At is the completed attempt time. Incomplete attempts are never fresh inventory.
		if c.Host != nil {
			host := c.Host.Collect()
			observation.Host = &host
		}
		if c.Health != nil {
			check, done := context.WithTimeout(ctx, 3*time.Second)
			health := c.Health(check)
			done()
			health.InstanceID, health.At = c.Binding.InstanceID, time.Now().UTC()
			health.Collector = "healthy"
			if collectErr != nil || len(observation.Errors) > 0 {
				health.Collector = "degraded"
			}
			if observation.Host != nil {
				for _, reason := range observation.Host.Errors {
					if reason != "warming_up" {
						health.Collector = "degraded"
					}
				}
			}
			observation.Health = &health
		}
		observation.At = time.Now().UTC()
		session.Sequence++
		value := Report{NodeID: c.Binding.NodeID, EngineID: c.Binding.EngineID, Generation: session.Generation, Sequence: session.Sequence, Observation: observation}
		if err = post(ctx, client, c.Binding.URL+"/internal/v1/node-observations", value, nil); err != nil {
			return err
		}
		if !pause(ctx, c.Interval) {
			break
		}
	}
	return ctx.Err()
}

// collectEvents has its own Docker connection so inventory polling cannot
// interrupt the stream. A batch is removed only after an acknowledged write.
func (c *Collector) collectEvents(ctx context.Context, client *http.Client, session Session) {
	base := EventBatch{NodeID: c.Binding.NodeID, EngineID: c.Binding.EngineID, Generation: session.Generation}
	queue := make([]model.DiscoverySignal, 0, 200)
	queueBytes := 0
	report := func(result string) {
		if c.EventReport != nil {
			c.EventReport(result)
		}
	}
	// A replacement collector cannot attest to the previous process's
	// unacknowledged in-memory events, even if its last heartbeat was recent.
	pendingGap := "stream_disconnected"
	sendGap := func() bool {
		if pendingGap == "" {
			return true
		}
		batch := base
		batch.At = time.Now().UTC()
		batch.State = pendingGap
		if err := post(ctx, client, c.Binding.URL+"/internal/v1/node-events", batch, nil); err != nil {
			report("gap_" + reportFailureClass(err))
			return false
		}
		pendingGap = ""
		return true
	}
	flush := func() bool {
		if !sendGap() {
			return false
		}
		batch := base
		batch.At = time.Now().UTC()
		batch.Events = queue
		if err := post(ctx, client, c.Binding.URL+"/internal/v1/node-events", batch, nil); err != nil {
			if pendingGap == "" {
				pendingGap = "upload_failed"
			}
			report("batch_" + reportFailureClass(err))
			return false
		}
		queue = queue[:0]
		queueBytes = 0
		report("batch_accepted")
		return true
	}
	_ = sendGap()
	for ctx.Err() == nil {
		cli, err := c.Open()
		if err == nil {
			var info struct{ engine, node, cluster string }
			var actualInfoErr error
			value, infoErr := cli.Info(ctx)
			actualInfoErr = infoErr
			if actualInfoErr == nil {
				info.engine, info.node = value.ID, value.Swarm.NodeID
				if value.Swarm.Cluster != nil {
					info.cluster = value.Swarm.Cluster.ID
				}
			}
			if actualInfoErr != nil || info.engine != c.Binding.EngineID || info.node != c.Binding.NodeID ||
				(c.Binding.ClusterID != "" && info.cluster != c.Binding.ClusterID) {
				err = ErrIdentity
			}
		}
		if err != nil {
			if cli != nil {
				_ = cli.Close()
			}
			if pendingGap == "" {
				pendingGap = "stream_disconnected"
			}
			_ = sendGap()
			if !pause(ctx, c.Interval) {
				return
			}
			continue
		}
		messages, failures := cli.Events(ctx, []string{"container", "task", "service"})
		ticker := time.NewTicker(c.Interval)
		connected := true
		for connected && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				connected = false
			case value, ok := <-messages:
				if !ok {
					connected = false
					break
				}
				if !wantedDockerEvent(string(value.Type), string(value.Action)) {
					continue
				}
				signal := engine.NormalizeDockerEvent(value, "")
				encoded, marshalErr := json.Marshal(signal)
				if marshalErr != nil || len(encoded) > 64<<10 {
					pendingGap = "overflow"
					report("overflow")
					continue
				}
				if len(queue) == cap(queue) || queueBytes+len(encoded) > 256<<10 {
					if !flush() {
						pendingGap = "overflow"
						report("overflow")
						continue
					}
				}
				queue = append(queue, signal)
				queueBytes += len(encoded)
			case _, ok := <-failures:
				if !ok {
					connected = false
					break
				}
				connected = false
			case <-ticker.C:
				_ = flush()
			}
		}
		ticker.Stop()
		_ = cli.Close()
		if ctx.Err() != nil {
			return
		}
		if pendingGap == "" {
			pendingGap = "stream_disconnected"
		}
		_ = sendGap()
		if !pause(ctx, c.Interval) {
			return
		}
	}
}

func wantedDockerEvent(actor, action string) bool {
	switch actor {
	case "container":
		switch action {
		case "die", "oom", "kill", "restart", "start", "stop", "destroy", "create", "health_status: healthy", "health_status: unhealthy":
			return true
		}
	case "task", "service":
		return action == "create" || action == "update" || action == "remove"
	}
	return false
}

type reportStatusError int

func (e reportStatusError) Error() string {
	return fmt.Sprintf("node report rejected with HTTP %d", int(e))
}

func reportFailureClass(err error) string {
	var status reportStatusError
	if errors.As(err, &status) {
		switch int(status) {
		case http.StatusForbidden:
			return "identity_rejected"
		case http.StatusConflict:
			return "ownership_lost"
		case http.StatusServiceUnavailable:
			return "storage_unavailable"
		default:
			return "rejected"
		}
	}
	return "transport_failed"
}

func post(ctx context.Context, client *http.Client, url string, value, result any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return reportStatusError(response.StatusCode)
	}
	if result != nil {
		return json.NewDecoder(http.MaxBytesReader(nil, response.Body, 4096)).Decode(result)
	}
	return nil
}

func (c *Collector) Collect(ctx context.Context) (Observation, error) {
	cli, err := c.Open()
	if err != nil {
		return Observation{}, err
	}
	defer cli.Close()
	o := Observation{Inspections: map[string]container.InspectResponse{}, Stats: map[string]container.StatsResponse{}, Errors: map[string]string{}}
	o.Info, err = cli.Info(ctx)
	if err != nil {
		return o, err
	}
	if o.Info.ID == "" || o.Info.Swarm.NodeID == "" || o.Info.Swarm.Cluster != nil && c.Binding.ClusterID != "" && o.Info.Swarm.Cluster.ID == "" {
		// Docker can omit cluster metadata while quorum is unavailable. Missing
		// evidence must fence management without terminating the Controller.
		return o, ErrIncomplete
	}
	if o.Info.ID != c.Binding.EngineID || o.Info.Swarm.NodeID != c.Binding.NodeID {
		return o, ErrIdentity
	}
	if o.Info.Swarm.Cluster != nil && c.Binding.ClusterID != "" && o.Info.Swarm.Cluster.ID != c.Binding.ClusterID {
		return o, ErrIdentity
	}
	if o.Containers, err = cli.ContainerList(ctx); err != nil {
		return o, err
	}
	if o.Networks, err = cli.NetworkList(ctx); err != nil {
		return o, err
	}
	if o.Info.Swarm.ControlAvailable {
		if o.Nodes, err = cli.NodeList(ctx); err != nil {
			return o, err
		}
		if o.Services, err = cli.ServiceList(ctx); err != nil {
			return o, err
		}
		if o.Tasks, err = cli.TaskList(ctx); err != nil {
			return o, err
		}
	}
	var mu sync.Mutex
	var workers sync.WaitGroup
	queue := make(chan container.Summary)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range queue {
				call, done := context.WithTimeout(ctx, 2*time.Second)
				inspection, inspectErr := cli.ContainerInspect(call, item.ID)
				var stats container.StatsResponse
				var statsErr error
				if string(item.State) == "running" {
					stats, statsErr = cli.ContainerStats(call, item.ID)
				}
				done()
				mu.Lock()
				if inspectErr == nil {
					o.Inspections[item.ID] = inspection
				} else {
					o.Errors["inspect:"+item.ID] = "unavailable"
				}
				if string(item.State) == "running" {
					if statsErr == nil {
						o.Stats[item.ID] = stats
					} else {
						o.Errors["stats:"+item.ID] = "unavailable"
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, item := range o.Containers {
		queue <- item
	}
	close(queue)
	workers.Wait()
	if storageClient, ok := cli.(engine.StorageClient); ok {
		o.Volumes = map[string]time.Time{}
		o.VolumeOwners = map[string]string{}
		o.ImageVolumes = map[string][]string{}
		volumes, e := storageClient.VolumeList(ctx)
		o.VolumeInventoryComplete = e == nil && len(o.Inspections) == len(o.Containers)
		for _, v := range volumes {
			o.VolumeDetails = append(o.VolumeDetails, VolumeMetadata(v))
		}
		o.StorageComplete = e == nil && len(o.Inspections) == len(o.Containers)
		for _, v := range volumes {
			if v.Driver != "local" {
				continue
			}
			created, e := time.Parse(time.RFC3339Nano, v.CreatedAt)
			if e != nil {
				o.StorageComplete = false
			}
			o.Volumes[v.Name] = created
			o.VolumeOwners[v.Name] = v.Labels["com.docker.stack.namespace"]
		}
		refs := map[string]bool{}
		images, imageErr := storageClient.ImageList(ctx)
		if imageErr != nil {
			o.StorageComplete = false
		}
		for _, image := range images {
			refs[image.ID] = true
			for _, ref := range image.RepoDigests {
				refs[ref] = true
			}
		}
		for _, v := range o.Inspections {
			if v.Config != nil {
				refs[v.Config.Image] = true
			}
			refs[v.Image] = true
		}
		for ref := range refs {
			if ref == "" {
				continue
			}
			image, e := storageClient.ImageInspect(ctx, ref)
			if e != nil || image.Config == nil {
				o.StorageComplete = false
				continue
			}
			targets := []string{}
			for target := range image.Config.Volumes {
				targets = append(targets, target)
			}
			o.ImageVolumes[ref] = targets
			o.ImageVolumes[image.ID] = targets
			for _, digest := range image.RepoDigests {
				o.ImageVolumes[digest] = targets
			}
		}
	}
	o.At, o.Complete = time.Now().UTC(), true
	return o, nil
}
