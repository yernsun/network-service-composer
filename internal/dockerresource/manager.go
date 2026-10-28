package dockerresource

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const maxContent = 500 << 10

var (
	ErrIdentity = errors.New("Swarm manager identity unavailable")
	ErrInUse    = errors.New("Docker resource has current or pending users")
	ErrUnowned  = errors.New("Docker resource is not owned by this Controller")
)

type Request struct {
	RuntimeID              string `json:"runtime_id"`
	Action                 string `json:"action"`
	Kind                   string `json:"kind,omitempty"`
	ID                     string `json:"id,omitempty"`
	Name                   string `json:"name,omitempty"`
	Content                []byte `json:"content,omitempty"`
	OperationID            string `json:"operation_id,omitempty"`
	AcknowledgeRestoreRisk bool   `json:"acknowledge_restore_risk,omitempty"`
}

type Item struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Owned     bool      `json:"owned"`
	CreatedAt time.Time `json:"created_at"`
	UsedBy    []string  `json:"used_by"`
}

type Result struct {
	ID    string `json:"id,omitempty"`
	Items []Item `json:"items,omitempty"`
}

type Manager struct {
	Store   storage.Store
	Config  config.Config
	Reports *nodecontrol.Reports
	Open    func(model.Runtime, engine.TLSFiles) (*client.Client, error)
}

func (m Manager) Do(ctx context.Context, request Request) (Result, error) {
	runtime, err := m.Store.GetRuntime(ctx, request.RuntimeID)
	if err != nil {
		return Result{}, err
	}
	if runtime.Type != model.RuntimeSwarm || !runtime.Enabled || runtime.ClusterID == "" {
		return Result{}, ErrIdentity
	}
	if request.Kind != "" && request.Kind != "config" && request.Kind != "secret" {
		return Result{}, storage.ErrConflict
	}
	if request.Action == "create" || request.Action == "delete" {
		operation, err := m.Store.GetDeploymentOperation(ctx, request.OperationID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return Result{}, err // A database outage must keep the encrypted operation retryable.
		}
		want := "DOCKER_RESOURCE_CREATE"
		if request.Action == "delete" {
			want = "DOCKER_RESOURCE_DELETE"
		}
		if err != nil || operation.Type != want || operation.RuntimeID != request.RuntimeID || operation.Status != model.OperationRunning || operation.WorkerID == "" {
			return Result{}, ErrIdentity
		}
	}
	if strings.HasPrefix(runtime.Endpoint, "node://") && (request.Action == "create" || request.Action == "delete") {
		target, bound, err := execution.BoundTarget(ctx, request.OperationID, runtime.ID)
		if err != nil {
			return Result{}, err
		}
		if !bound {
			return Result{}, ErrIdentity
		}
		runtime.Endpoint = "node://" + target.NodeID
	}
	if strings.HasPrefix(runtime.Endpoint, "node://") && request.Action == "list" && m.Reports != nil {
		binding, err := m.Reports.ManagerBinding(ctx, runtime)
		if err != nil {
			return Result{}, err
		}
		runtime.Endpoint = "node://" + binding.NodeID
	}
	if strings.HasPrefix(runtime.Endpoint, "node://") {
		var binding *config.ControllerNodeConfig
		for index := range m.Config.Controller.Nodes {
			value := &m.Config.Controller.Nodes[index]
			if runtime.Endpoint == "node://"+value.NodeID && runtime.ClusterID == value.ClusterID {
				binding = value
				break
			}
		}
		if binding == nil {
			return Result{}, ErrIdentity
		}
		if binding.InstanceID != m.Config.Controller.InstanceID {
			return m.proxy(ctx, *binding, request)
		}
		return m.local(ctx, runtime, request, binding)
	}
	return m.local(ctx, runtime, request, nil)
}

func (m Manager) local(ctx context.Context, runtime model.Runtime, request Request, binding *config.ControllerNodeConfig) (Result, error) {
	endpoint := runtime.Endpoint
	if binding != nil {
		if err := m.checkOwner(ctx, *binding); err != nil {
			return Result{}, err
		}
		endpoint = "unix:///var/run/docker.sock"
	}
	var files engine.TLSFiles
	if runtime.CredentialRef != "" {
		credential, found := m.Config.Credentials[runtime.CredentialRef]
		if !found {
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
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		// An unavailable engine cannot establish an identity mismatch, especially
		// when reconciling a request that may already have been accepted.
		return Result{}, err
	}
	if !info.Info.Swarm.ControlAvailable || info.Info.Swarm.Cluster == nil || info.Info.Swarm.Cluster.ID != runtime.ClusterID {
		return Result{}, ErrIdentity
	}
	if binding != nil && (info.Info.ID != binding.EngineID || info.Info.Swarm.NodeID != binding.NodeID) {
		return Result{}, ErrIdentity
	}
	if binding != nil && (request.Action == "create" || request.Action == "delete") {
		nodes, err := cli.NodeList(ctx, client.NodeListOptions{})
		if err != nil || !execution.Quorum(info.Info, nodes.Items) {
			return Result{}, execution.ErrUnavailable
		}
	}
	switch request.Action {
	case "list":
		return m.list(ctx, cli, runtime.ClusterID, request.Kind)
	case "create":
		return m.create(ctx, cli, runtime.ClusterID, request)
	case "delete":
		return m.remove(ctx, cli, runtime.ClusterID, request)
	default:
		return Result{}, storage.ErrConflict
	}
}

func (m Manager) checkOwner(ctx context.Context, binding config.ControllerNodeConfig) error {
	s, ok := m.Store.(*storage.SQLStore)
	if !ok {
		return ErrIdentity
	}
	doc, err := m.Store.GetControlDocument(ctx, "node-session:"+binding.NodeID)
	if err != nil {
		return err
	}
	var session nodecontrol.Session
	if doc.Kind != "node-session" || doc.ParentID != binding.NodeID || json.Unmarshal(doc.Payload, &session) != nil || session.Owner <= 0 {
		return ErrIdentity
	}
	return lease.CheckOwner(ctx, s.DB(), s.Dialect(), nodecontrol.LockKey(binding.NodeID), session.Owner)
}

const labelOwner = "com.nsc.docker-resource.owner"
const labelCluster = "com.nsc.docker-resource.cluster"
const labelOperation = "com.nsc.docker-resource.operation"

func (m Manager) list(ctx context.Context, cli *client.Client, clusterID, kind string) (Result, error) {
	result := Result{Items: []Item{}}
	services, err := cli.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return Result{}, err
	}
	add := func(id, name, resourceKind string, labels map[string]string, at time.Time) error {
		if kind != "" && resourceKind != kind {
			return nil
		}
		item := Item{ID: id, Name: name, Kind: resourceKind, CreatedAt: at, UsedBy: []string{}}
		if labels[labelOwner] == "nsc" && labels[labelCluster] == clusterID && labels[labelOperation] != "" {
			record, err := m.Store.GetDockerResource(ctx, clusterID, resourceKind, id)
			item.Owned = err == nil && record.Name == name && record.CreateOperationID == labels[labelOperation]
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
		}
		for _, service := range services.Items {
			if service.Spec.TaskTemplate.ContainerSpec == nil {
				continue
			}
			if resourceKind == "config" {
				for _, reference := range service.Spec.TaskTemplate.ContainerSpec.Configs {
					if reference.ConfigID == id {
						item.UsedBy = append(item.UsedBy, service.Spec.Name)
						break
					}
				}
			} else {
				for _, reference := range service.Spec.TaskTemplate.ContainerSpec.Secrets {
					if reference.SecretID == id {
						item.UsedBy = append(item.UsedBy, service.Spec.Name)
						break
					}
				}
			}
		}
		result.Items = append(result.Items, item)
		return nil
	}
	if kind == "" || kind == "config" {
		configs, err := cli.ConfigList(ctx, client.ConfigListOptions{})
		if err != nil {
			return Result{}, err
		}
		for _, value := range configs.Items {
			if err := add(value.ID, value.Spec.Name, "config", value.Spec.Labels, value.Meta.CreatedAt); err != nil {
				return Result{}, err
			}
		}
	}
	if kind == "" || kind == "secret" {
		secrets, err := cli.SecretList(ctx, client.SecretListOptions{})
		if err != nil {
			return Result{}, err
		}
		for _, value := range secrets.Items {
			if err := add(value.ID, value.Spec.Name, "secret", value.Spec.Labels, value.Meta.CreatedAt); err != nil {
				return Result{}, err
			}
		}
	}
	return result, nil
}

func (m Manager) create(ctx context.Context, cli *client.Client, clusterID string, request Request) (Result, error) {
	if request.OperationID == "" || request.Name == "" || len(request.Content) == 0 || len(request.Content) > maxContent {
		return Result{}, storage.ErrConflict
	}
	listed, err := m.list(ctx, cli, clusterID, request.Kind)
	if err != nil {
		return Result{}, err
	}
	for _, item := range listed.Items {
		if item.Kind != request.Kind || item.Name != request.Name {
			continue
		}
		record, err := m.Store.GetDockerResourceByOperation(ctx, request.OperationID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return Result{}, err
		}
		if err == nil && item.ID == record.DockerID && item.Owned {
			return Result{ID: item.ID}, nil
		}
		// A lost response leaves a Docker object before its DB record. Inspect
		// the operation label directly to reconcile without creating a second.
		same, err := m.sameOperation(ctx, cli, request.Kind, item.ID, clusterID, request.OperationID)
		if err != nil {
			return Result{}, err
		}
		if same {
			return Result{ID: item.ID}, nil
		}
		return Result{}, storage.ErrConflict
	}
	labels := map[string]string{labelOwner: "nsc", labelCluster: clusterID, labelOperation: request.OperationID}
	if request.Kind == "config" {
		if err := execution.BeforeMutation(ctx, request.OperationID, request.RuntimeID); err != nil {
			return Result{}, err
		}
		created, err := cli.ConfigCreate(ctx, client.ConfigCreateOptions{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: request.Name, Labels: labels}, Data: request.Content}})
		return Result{ID: created.ID}, err
	}
	if request.Kind == "secret" {
		if err := execution.BeforeMutation(ctx, request.OperationID, request.RuntimeID); err != nil {
			return Result{}, err
		}
		created, err := cli.SecretCreate(ctx, client.SecretCreateOptions{Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: request.Name, Labels: labels}, Data: request.Content}})
		return Result{ID: created.ID}, err
	}
	return Result{}, storage.ErrConflict
}

func (m Manager) sameOperation(ctx context.Context, cli *client.Client, kind, id, cluster, operation string) (bool, error) {
	if kind == "config" {
		values, err := cli.ConfigList(ctx, client.ConfigListOptions{})
		if err != nil {
			return false, err
		}
		for _, value := range values.Items {
			if value.ID == id {
				return value.Spec.Labels[labelOwner] == "nsc" && value.Spec.Labels[labelCluster] == cluster && value.Spec.Labels[labelOperation] == operation, nil
			}
		}
	} else {
		values, err := cli.SecretList(ctx, client.SecretListOptions{})
		if err != nil {
			return false, err
		}
		for _, value := range values.Items {
			if value.ID == id {
				return value.Spec.Labels[labelOwner] == "nsc" && value.Spec.Labels[labelCluster] == cluster && value.Spec.Labels[labelOperation] == operation, nil
			}
		}
	}
	return false, nil
}

func (m Manager) remove(ctx context.Context, cli *client.Client, clusterID string, request Request) (Result, error) {
	if request.ID == "" || !request.AcknowledgeRestoreRisk {
		return Result{}, storage.ErrConflict
	}
	listed, err := m.list(ctx, cli, clusterID, request.Kind)
	if err != nil {
		return Result{}, err
	}
	record, recordErr := m.Store.GetDockerResource(ctx, clusterID, request.Kind, request.ID)
	if recordErr != nil && !errors.Is(recordErr, storage.ErrNotFound) {
		return Result{}, recordErr
	}
	found := false
	for _, item := range listed.Items {
		if item.ID == request.ID && item.Kind == request.Kind {
			found = true
			if !item.Owned {
				return Result{}, ErrUnowned
			}
			if len(item.UsedBy) > 0 {
				return Result{}, ErrInUse
			}
		}
	}
	if !found {
		return Result{ID: request.ID}, nil
	} // prior remove ACK was lost
	if recordErr != nil {
		return Result{}, ErrUnowned
	}
	same, err := m.sameOperation(ctx, cli, request.Kind, request.ID, clusterID, record.CreateOperationID)
	if err != nil {
		return Result{}, err
	}
	if !same {
		return Result{}, ErrUnowned
	}
	busy, err := m.Store.HasPendingDockerResourceConsumer(ctx, clusterID, request.OperationID)
	if err != nil {
		return Result{}, err
	}
	if busy {
		return Result{}, ErrInUse
	}
	if err := execution.BeforeMutation(ctx, request.OperationID, request.RuntimeID); err != nil {
		return Result{}, err
	}
	if request.Kind == "config" {
		_, err = cli.ConfigRemove(ctx, request.ID, client.ConfigRemoveOptions{})
	} else if request.Kind == "secret" {
		_, err = cli.SecretRemove(ctx, request.ID, client.SecretRemoveOptions{})
	} else {
		return Result{}, storage.ErrConflict
	}
	return Result{ID: request.ID}, err
}

func (m Manager) proxy(ctx context.Context, binding config.ControllerNodeConfig, request Request) (Result, error) {
	var result Result
	err := Forward(ctx, m.Config, binding, "/internal/v1/docker-resources", request, &result)
	return result, err
}

// RemoteError carries an allowlisted machine code, never a remote diagnostic.
type RemoteError struct {
	Code   string
	Status int
}

func (e RemoteError) Error() string {
	return fmt.Sprintf("manager resource request rejected: HTTP %d (%s)", e.Status, e.Code)
}

// Forward uses the existing node identity and mTLS transport for resource commands.
func Forward(ctx context.Context, cfg config.Config, binding config.ControllerNodeConfig, path string, input, output any) error {
	if err := controlplane.ValidateInternalURL(binding.URL); err != nil {
		return err
	}
	tlsConfig, err := controlplane.InternalTLS(cfg.Controller.Internal.CAFile, cfg.Controller.Internal.CertFile, cfg.Controller.Internal.KeyFile, cfg.Controller.InstanceID)
	if err != nil {
		return err
	}
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if err := verify(state); err != nil {
			return err
		}
		if len(state.PeerCertificates) == 0 {
			return ErrIdentity
		}
		identity, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil || identity != binding.InstanceID {
			return ErrIdentity
		}
		return nil
	}
	u, err := url.Parse(binding.URL)
	if err != nil {
		return err
	}
	u.Path = path
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&problem) == nil && slices.Contains([]string{"volume.identity", "volume.blocked", "volume.conflict", "volume.precondition", "volume.not_found"}, problem.Code) {
			return RemoteError{Code: problem.Code, Status: response.StatusCode}
		}
		return fmt.Errorf("manager resource request rejected: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil {
		return err
	}
	return nil
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
		for _, binding := range m.Config.Controller.Nodes {
			if binding.InstanceID == peer {
				known = true
				break
			}
		}
		if !known {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request Request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		runtime, err := m.Store.GetRuntime(r.Context(), request.RuntimeID)
		if err != nil || runtime.Type != model.RuntimeSwarm || runtime.ClusterID == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var self *config.ControllerNodeConfig
		for index := range m.Config.Controller.Nodes {
			binding := &m.Config.Controller.Nodes[index]
			if binding.InstanceID == m.Config.Controller.InstanceID && runtime.ClusterID == binding.ClusterID && (runtime.Endpoint == "node://"+binding.NodeID || m.Reports != nil) {
				self = binding
				break
			}
		}
		if self == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if request.Action != "list" || request.Kind != "" && request.Kind != "config" && request.Kind != "secret" {
			// Mutations are accepted only through the frozen operation endpoint.
			// The legacy resource transport remains available for inventory.
			w.WriteHeader(http.StatusForbidden)
			return
		}

		if runtime.Endpoint != "node://"+self.NodeID {
			if _, err := m.Reports.ManagerBinding(r.Context(), runtime); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			report, err := m.Reports.ReadLive(r.Context(), self.NodeID)
			if err != nil || report.Observation.Info.ID != self.EngineID || report.Observation.Info.Swarm.NodeID != self.NodeID || !report.Observation.Info.Swarm.ControlAvailable || report.Observation.Info.Swarm.Cluster == nil || report.Observation.Info.Swarm.Cluster.ID != runtime.ClusterID {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		result, err := m.local(r.Context(), runtime, request, self)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// CheckNodeOwner requires the provisioned node collector to retain its database lease.
func (m Manager) CheckNodeOwner(ctx context.Context, binding config.ControllerNodeConfig) error {
	return m.checkOwner(ctx, binding)
}
