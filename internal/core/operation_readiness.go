package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

type ReadinessState string

const (
	ReadinessPending       ReadinessState = "PENDING"
	ReadinessConfirmed     ReadinessState = "CONFIRMED"
	ReadinessFailed        ReadinessState = "FAILED"
	ReadinessUnknown       ReadinessState = "UNKNOWN"
	ReadinessNotApplicable ReadinessState = "NOT_APPLICABLE"
)

type ReadinessStage struct {
	Name       string         `json:"name"`
	State      ReadinessState `json:"state"`
	ErrorCode  string         `json:"error_code,omitempty"`
	ObjectID   string         `json:"object_id,omitempty"`
	Revision   int64          `json:"revision,omitempty"`
	ObservedAt *time.Time     `json:"observed_at,omitempty"`
}

type ReadinessGatewayTarget struct {
	GatewayID       string         `json:"gateway_id"`
	PoolID          string         `json:"pool_id"`
	State           ReadinessState `json:"state"`
	ErrorCode       string         `json:"error_code,omitempty"`
	DesiredRevision int64          `json:"desired_revision"`
	AppliedRevision int64          `json:"applied_revision"`
	ModelDigest     string         `json:"model_digest,omitempty"`
	RenderedDigest  string         `json:"rendered_digest,omitempty"`
	ObservedAt      *time.Time     `json:"observed_at,omitempty"`
}

type ReadinessAccessTarget struct {
	DomainID               string `json:"domain_id"`
	RouteID                string `json:"route_id"`
	Host                   string `json:"host"`
	Scheme                 string `json:"scheme"`
	Port                   int    `json:"port"`
	Path                   string `json:"path"`
	CertificateFingerprint string `json:"certificate_fingerprint,omitempty"`
}

type OperationReadiness struct {
	Execution      *ExecutionEvidence       `json:"execution,omitempty"`
	OperationID    string                   `json:"operation_id"`
	RevisionID     string                   `json:"revision_id,omitempty"`
	Revision       int64                    `json:"revision"`
	ObservedAt     time.Time                `json:"observed_at"`
	BaselineDigest string                   `json:"baseline_digest"`
	Stages         []ReadinessStage         `json:"stages"`
	GatewayTargets []ReadinessGatewayTarget `json:"gateway_targets"`
	AccessTargets  []ReadinessAccessTarget  `json:"access_targets"`
}

// The baseline binds resource identities and configuration, never heartbeat
// timestamps or transient observation state. A client can detect a changed
// target between its read-only access check and the final Controller read.
type readinessBaseline struct {
	OperationID       string                  `json:"operation_id"`
	Type              string                  `json:"type"`
	RevisionID        string                  `json:"revision_id"`
	ApplicationID     string                  `json:"application_id"`
	ActiveRevisionID  string                  `json:"active_revision_id"`
	DesiredRevisionID string                  `json:"desired_revision_id"`
	RuntimeID         string                  `json:"runtime_id"`
	Objects           []string                `json:"objects"`
	Gateways          []string                `json:"gateways"`
	Pools             map[string]int64        `json:"pools"`
	Bindings          []string                `json:"bindings"`
	Targets           []ReadinessAccessTarget `json:"access_targets"`
	Recovery          readinessRecovery       `json:"recovery"`
}

type readinessRecovery struct {
	Held      bool       `json:"held"`
	Mode      bool       `json:"mode"`
	Available bool       `json:"-"`
	ReviewID  string     `json:"review_id,omitempty"`
	Cutover   *time.Time `json:"cutover,omitempty"`
}

func (c *Core) observeReadinessRecovery(ctx context.Context, operationID string) readinessRecovery {
	v := readinessRecovery{Mode: c.config.Controller.RecoveryMode}
	held, err := storage.RecoveryHeld(ctx, c.store, "deployment", operationID)
	if err != nil {
		return v
	}
	v.Held = held
	page := storage.Page{Limit: 200}
	for {
		reviews, err := c.store.ListControlDocuments(ctx, "recovery-review", "", page)
		if err != nil {
			return v
		}
		for _, review := range reviews {
			if review.CreatedAt.IsZero() {
				return v
			}
			if v.Cutover == nil || review.CreatedAt.After(*v.Cutover) {
				cutover := review.CreatedAt.UTC()
				v.Cutover, v.ReviewID = &cutover, review.ID
			}
		}
		if len(reviews) < page.Limit {
			v.Available = true
			return v
		}
		page.After = reviews[len(reviews)-1].ID
	}
}

// An observation at the cutover cannot establish that it followed release.
func readinessAfterCutover(cutover *time.Time, observations ...*time.Time) bool {
	if cutover == nil {
		return true
	}
	for _, observation := range observations {
		if observation == nil || !observation.After(*cutover) {
			return false
		}
	}
	return true
}

func (v *OperationReadiness) stage(name string) *ReadinessStage {
	for i := range v.Stages {
		if v.Stages[i].Name == name {
			return &v.Stages[i]
		}
	}
	panic("unknown readiness stage")
}

func (v *OperationReadiness) set(name string, state ReadinessState, code string) {
	stage := v.stage(name)
	stage.State, stage.ErrorCode = state, code
}

func (v *OperationReadiness) incomplete(code string) {
	for _, name := range []string{"workload", "certificate", "gateway", "client_access"} {
		v.set(name, ReadinessUnknown, code)
	}
}

func readinessFresh(now time.Time, at *time.Time, ttl time.Duration) bool {
	return at != nil && !at.IsZero() && !at.After(now.Add(5*time.Second)) && now.Sub(*at) < ttl
}

func readinessDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func readinessGatewayDigests(gateway model.Gateway, evidence GatewayEvidence) bool {
	for _, value := range []string{gateway.ModelDigest, gateway.RenderedDigest, evidence.ModelDigest, evidence.RenderedDigest, evidence.DiskDigest} {
		if !readinessDigest(value) {
			return false
		}
	}
	return true
}

func readinessRank(value ReadinessState) int {
	switch value {
	case ReadinessFailed:
		return 3
	case ReadinessUnknown:
		return 2
	case ReadinessPending:
		return 1
	default:
		return 0
	}
}

func readinessMerge(stage *ReadinessStage, state ReadinessState, code string) {
	if stage.State == ReadinessNotApplicable || readinessRank(state) > readinessRank(stage.State) {
		stage.State, stage.ErrorCode = state, code
	}
}

// DeploymentReadiness observes existing evidence only. It never retries an
// operation, requests a certificate, publishes configuration or probes a URL.
func (c *Core) DeploymentReadiness(ctx context.Context, operationID string) (OperationReadiness, error) {
	op, err := c.store.GetDeploymentOperation(ctx, operationID)
	if err != nil {
		return OperationReadiness{}, err
	}
	now := c.now()
	v := OperationReadiness{OperationID: op.ID, RevisionID: op.RevisionID, Revision: op.Revision, ObservedAt: now,
		GatewayTargets: []ReadinessGatewayTarget{}, AccessTargets: []ReadinessAccessTarget{}, Stages: []ReadinessStage{
			{Name: "accepted", State: ReadinessConfirmed, ObjectID: op.ID, Revision: op.Revision, ObservedAt: &op.CreatedAt},
			{Name: "workload", State: ReadinessPending},
			{Name: "certificate", State: ReadinessNotApplicable},
			{Name: "gateway", State: ReadinessNotApplicable},
			{Name: "client_access", State: ReadinessNotApplicable},
		}}
	recovery := c.observeReadinessRecovery(ctx, op.ID)
	baseline := readinessBaseline{OperationID: op.ID, Type: op.Type, RevisionID: op.RevisionID, ApplicationID: op.ApplicationID, RuntimeID: op.RuntimeID, Objects: []string{}, Gateways: []string{}, Pools: map[string]int64{}, Bindings: []string{}, Targets: []ReadinessAccessTarget{}, Recovery: recovery}
	v.Execution = c.operationExecutionEvidence(ctx, op)
	finish := func() (OperationReadiness, error) {
		if recovery.Held || recovery.Mode || !recovery.Available {
			code := "recovery_held"
			if !recovery.Available {
				code = "recovery_state_unavailable"
			}
			v.incomplete(code)
			for i := range v.GatewayTargets {
				v.GatewayTargets[i].State, v.GatewayTargets[i].ErrorCode = ReadinessUnknown, code
			}
		}
		sort.Strings(baseline.Objects)
		sort.Strings(baseline.Gateways)
		sort.Strings(baseline.Bindings)
		sort.Slice(v.AccessTargets, func(i, j int) bool {
			a, b := v.AccessTargets[i], v.AccessTargets[j]
			return a.DomainID+":"+a.RouteID+":"+a.Scheme < b.DomainID+":"+b.RouteID+":"+b.Scheme
		})
		baseline.Targets = v.AccessTargets
		raw, marshalErr := json.Marshal(baseline)
		if marshalErr != nil {
			return v, marshalErr
		}
		v.BaselineDigest = digestBytes(raw)
		return v, nil
	}
	if op.ApplicationID == "" {
		v.set("workload", ReadinessNotApplicable, "not_workload_operation")
		return finish()
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil || app.RuntimeID != op.RuntimeID {
		v.incomplete("workload_unavailable")
		return finish()
	}
	v.stage("workload").ObjectID = app.ID
	var revision model.ApplicationRevision
	if op.RevisionID != "" {
		revision, err = c.store.GetApplicationRevision(ctx, op.RevisionID)
		if err != nil || revision.ApplicationID != app.ID {
			v.incomplete("workload_unavailable")
			return finish()
		}
	}
	scope := readinessOperationScope(op, revision)
	scoped := op.ServiceID != "" || scope != nil
	if !scoped {
		baseline.ActiveRevisionID, baseline.DesiredRevisionID = app.ActiveRevisionID, app.DesiredRevisionID
		if op.Status == model.OperationSucceeded && op.RevisionID != "" && app.ActiveRevisionID != op.RevisionID {
			v.incomplete("workload_superseded")
			return finish()
		}
	}
	runtime, err := c.store.GetRuntime(ctx, op.RuntimeID)
	if err != nil {
		v.incomplete("workload_unavailable")
		return finish()
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil {
		v.incomplete("workload_unavailable")
		return finish()
	}
	selected := []model.Service{}
	for _, service := range services {
		if !scoped && revision.ID != "" && readinessServiceHead(revision, applicationServiceNameForPolicy(service, app)) == "" {
			continue
		}
		if op.ServiceID != "" && service.ID != op.ServiceID {
			continue
		}
		if op.ServiceID == "" && scope != nil {
			included := false
			for _, name := range scope.Names {
				included = included || service.Name == name || service.ServiceKey == app.ProjectName+"_"+name || service.ServiceKey == app.ProjectName+"/"+name
			}
			if !included {
				continue
			}
		}
		if service.RuntimeID != runtime.ID || service.ApplicationID != app.ID {
			v.incomplete("workload_unavailable")
			return finish()
		}
		selected = append(selected, service)
		baseline.Objects = append(baseline.Objects, service.ID+":"+service.RuntimeObjectID)
	}
	if scoped {
		if code := c.readinessServiceHeads(ctx, &baseline, op, app, revision, selected); code != "" {
			v.incomplete(code)
			return finish()
		}
	}
	c.readinessWorkload(ctx, &v, &baseline, op, app, revision, runtime, selected, now, recovery.Cutover)
	if op.Type == "STOP" || op.Type == "DOWN" || op.Type == "REMOVE_RUNTIME" || op.Type == "DOMAIN_DETACH" || readinessServiceDeletion(op, revision) {
		for _, name := range []string{"certificate", "gateway", "client_access"} {
			v.set(name, ReadinessNotApplicable, "access_not_requested")
		}
		return finish()
	}
	bindings := []model.DomainBinding{}
	for _, service := range selected {
		if op.Type == "DOMAIN_ATTACH" {
			binding, readErr := c.domainBindingFromOperation(ctx, service, op)
			if readErr != nil {
				v.set("certificate", ReadinessUnknown, "binding_unavailable")
				v.set("gateway", ReadinessUnknown, "binding_unavailable")
				v.set("client_access", ReadinessUnknown, "client_not_verified")
				return finish()
			}
			bindings = append(bindings, binding)
		} else {
			values, readErr := c.store.ListServiceDomainBindings(ctx, service.ID)
			if readErr != nil {
				v.set("certificate", ReadinessUnknown, "binding_unavailable")
				v.set("gateway", ReadinessUnknown, "binding_unavailable")
				v.set("client_access", ReadinessUnknown, "client_not_verified")
				return finish()
			}
			bindings = append(bindings, values...)
		}
	}
	entrypoint := readinessSelectedEntrypoint(revision, app, selected)
	if len(bindings) == 0 && entrypoint != nil {
		v.set("gateway", ReadinessPending, "binding_pending")
		v.set("client_access", ReadinessUnknown, "client_not_verified")
		status, statusErr := c.ServiceEntrypointStatus(ctx, op.ID)
		if statusErr != nil {
			v.set("gateway", ReadinessUnknown, "binding_unavailable")
		} else if status.Status == "FAILED" {
			v.set("gateway", ReadinessFailed, "binding_failed")
		}
		if entrypoint.CertificateID != "" {
			v.set("certificate", ReadinessPending, "certificate_pending")
		}
		return finish()
	}
	pools := map[string]bool{}
	for _, binding := range bindings {
		if !binding.Domain.Enabled || !binding.Route.Enabled {
			continue
		}
		if binding.Service.ID == "" || binding.Route.ServiceID != binding.Service.ID || binding.Domain.PoolID != runtime.PoolID {
			v.set("gateway", ReadinessUnknown, "binding_unavailable")
			v.set("client_access", ReadinessUnknown, "client_not_verified")
			return finish()
		}
		v.set("client_access", ReadinessUnknown, "client_not_verified")
		readinessMerge(v.stage("gateway"), ReadinessConfirmed, "")
		if binding.Route.Status != model.RouteActive {
			readinessMerge(v.stage("gateway"), ReadinessPending, "binding_pending")
		}
		pools[binding.Domain.PoolID] = true
		baseline.Bindings = append(baseline.Bindings, binding.Route.ID+":"+binding.Domain.ID+":"+strconv.FormatInt(binding.Route.Revision, 10)+":"+strconv.FormatInt(binding.Domain.Revision, 10))
		fingerprint := c.readinessCertificate(ctx, &v, binding.Domain, &baseline, now)
		scheme, port := "http", 80
		if binding.Domain.HTTPSEnabled {
			scheme, port = "https", 443
		}
		if binding.Domain.HTTPEnabled || binding.Domain.HTTPSEnabled {
			path := binding.Route.Path
			if !strings.HasPrefix(path, "/") {
				path = "/"
			}
			v.AccessTargets = append(v.AccessTargets, ReadinessAccessTarget{DomainID: binding.Domain.ID, RouteID: binding.Route.ID, Host: binding.Domain.Host, Scheme: scheme, Port: port, Path: path, CertificateFingerprint: fingerprint})
		}
	}
	for poolID := range pools {
		c.readinessGateways(ctx, &v, &baseline, poolID, entrypoint, recovery.Cutover)
	}
	sort.Slice(v.GatewayTargets, func(i, j int) bool { return v.GatewayTargets[i].GatewayID < v.GatewayTargets[j].GatewayID })
	return finish()
}

// A nil target means required immutable intent could not be established; an
// absent target leaves an unmanaged or ordinary Swarm workload's native desired
// count authoritative. Compose inventory counts containers that still exist,
// so it cannot establish the managed application's requested replica count.
func (c *Core) readinessReplicaTargets(op model.DeploymentOperation, app model.Application, revision model.ApplicationRevision, runtime model.Runtime, services []model.Service) map[string]*int {
	targets := map[string]*int{}
	if op.Type == "STOP" || op.Type == "DOWN" || op.Type == "REMOVE_RUNTIME" {
		return targets
	}
	for _, service := range services {
		if service.Managed && (runtime.Type == model.RuntimeDocker || op.Type == "SCALE") {
			targets[service.ID] = nil
		}
	}
	if len(targets) == 0 || c.keyring == nil || revision.ID == "" || revision.ApplicationID != app.ID {
		return targets
	}
	if op.Type == "SCALE" {
		raw, _, err := c.keyring.Decrypt(op.EncryptedInput, operationAAD(op.ID))
		var input struct {
			Replicas   *int   `json:"replicas"`
			ServiceKey string `json:"service_key"`
		}
		if err != nil || json.Unmarshal(raw, &input) != nil || input.Replicas == nil || *input.Replicas < 0 || *input.Replicas > 10000 {
			return targets
		}
		for _, service := range services {
			if service.Managed && op.ServiceID == service.ID && input.ServiceKey == service.ServiceKey {
				targets[service.ID] = input.Replicas
			}
		}
		return targets
	}
	raw, _, err := c.keyring.Decrypt(revision.EncryptedResolved, revisionAAD(revision.ID, "resolved"))
	var document struct {
		Services map[string]struct {
			Image  string `yaml:"image"`
			Deploy struct {
				Replicas *int   `yaml:"replicas"`
				Mode     string `yaml:"mode"`
			} `yaml:"deploy"`
		} `yaml:"services"`
	}
	if err != nil || digestString(string(raw)) != revision.ResolvedSHA256 || yaml.Unmarshal(raw, &document) != nil {
		return targets
	}
	for _, service := range services {
		if _, required := targets[service.ID]; !required {
			continue
		}
		name := applicationServiceNameForPolicy(service, app)
		definition, exists := document.Services[name]
		if !exists || strings.TrimSpace(definition.Image) == "" || definition.Deploy.Mode != "" && definition.Deploy.Mode != "replicated" {
			continue
		}
		count := 1
		if definition.Deploy.Replicas != nil {
			count = *definition.Deploy.Replicas
		}
		if override, exists := app.ReplicaOverrides[name]; exists {
			count = override
		}
		// Incremental Compose deploy explicitly supplies --scale, which takes
		// precedence over both the aggregate definition and saved overrides.
		if scope := revision.ConfigurationSource.ServiceScope; op.Type == "UPGRADE" && scope != nil && scope.RequestedReplicas != nil {
			if len(scope.Names) != 1 || scope.Names[0] != name {
				continue
			}
			count = *scope.RequestedReplicas
		}
		if count >= 0 {
			targets[service.ID] = &count
		}
	}
	return targets
}

func (c *Core) readinessWorkload(ctx context.Context, v *OperationReadiness, baseline *readinessBaseline, op model.DeploymentOperation, app model.Application, revision model.ApplicationRevision, runtime model.Runtime, services []model.Service, now time.Time, cutover *time.Time) {
	replicas := c.readinessReplicaTargets(op, app, revision, runtime, services)
	for _, service := range services {
		if count, required := replicas[service.ID]; required {
			intent := "unknown"
			if count != nil {
				intent = strconv.Itoa(*count)
			}
			baseline.Objects = append(baseline.Objects, service.ID+":requested-replicas:"+intent)
		}
	}
	presence, _, presenceErr := readControl[storage.RuntimePresence](ctx, c.store, storage.RuntimePresenceID(runtime.ID), "runtime-presence")
	present := map[string]bool{}
	for _, id := range presence.ServiceIDs {
		present[id] = true
	}
	for _, service := range services {
		if service.Managed {
			// Membership changes target semantics; polling epochs, timestamps
			// and transient operation/runtime/service states do not change them.
			baseline.Objects = append(baseline.Objects, service.ID+":present:"+strconv.FormatBool(present[service.ID]))
		}
	}
	switch op.Status {
	case model.OperationFailed, model.OperationRolledBack, model.OperationRollbackFailed:
		v.set("workload", ReadinessFailed, "operation_failed")
		return
	case model.OperationQueued:
		return
	case model.OperationSucceeded:
		// Runtime evidence below confirms the current result independently.
	case model.OperationRunning:
		if !readinessFresh(now, op.HeartbeatAt, c.config.Management.StaleAfter.Std()) || !readinessAfterCutover(cutover, op.HeartbeatAt) {
			v.set("workload", ReadinessUnknown, "operation_stalled")
		}
		return
	default:
		v.set("workload", ReadinessUnknown, "operation_stalled")
		return
	}
	if !readinessAfterCutover(cutover, runtime.LastSeenAt) {
		v.set("workload", ReadinessUnknown, "recovery_observation_pending")
		return
	}
	if !runtime.Enabled || runtime.Status != model.RuntimeOnline || runtime.ObservationEpoch != runtime.AppliedObservation || !readinessFresh(now, runtime.LastSeenAt, c.config.Controller.Discovery.StaleEndpointTimeout.Std()) {
		v.set("workload", ReadinessUnknown, "workload_stale")
		return
	}
	if readinessServiceDeletion(op, revision) {
		c.readinessDeletedService(v, op, revision, runtime, services, presence, presenceErr)
		return
	}
	if len(services) == 0 {
		if op.Type == "STOP" || op.Type == "DOWN" || op.Type == "REMOVE_RUNTIME" {
			v.set("workload", ReadinessConfirmed, "")
		} else {
			v.set("workload", ReadinessUnknown, "workload_unavailable")
		}
		return
	}
	v.set("workload", ReadinessConfirmed, "")
	v.stage("workload").ObservedAt = runtime.LastSeenAt
	for _, service := range services {
		if !readinessAfterCutover(cutover, service.LastSeenAt) {
			readinessMerge(v.stage("workload"), ReadinessUnknown, "recovery_observation_pending")
			continue
		}
		if !readinessFresh(now, service.LastSeenAt, c.config.Controller.Discovery.StaleEndpointTimeout.Std()) {
			readinessMerge(v.stage("workload"), ReadinessUnknown, "workload_stale")
			continue
		}
		if service.ConfigurationError != "" {
			readinessMerge(v.stage("workload"), ReadinessFailed, "workload_unhealthy")
			continue
		}
		if op.Type == "STOP" || op.Type == "DOWN" || op.Type == "REMOVE_RUNTIME" {
			if service.RunningInstances > 0 {
				readinessMerge(v.stage("workload"), ReadinessPending, "workload_converging")
			}
		} else {
			if service.Managed {
				// Exact time equality inherits the authoritative Runtime's freshness
				// and post-recovery cutover checks already established above.
				if presenceErr != nil || presence.RuntimeID != runtime.ID || presence.Observation < 1 || presence.Observation != runtime.AppliedObservation || !presence.ObservedAt.Equal(*runtime.LastSeenAt) || !present[service.ID] || service.RemovedAt != nil || service.Status == model.ServiceRemoved {
					readinessMerge(v.stage("workload"), ReadinessUnknown, "workload_unavailable")
					continue
				}
			}
			desired := service.DesiredInstances
			if count, required := replicas[service.ID]; required {
				if count == nil {
					readinessMerge(v.stage("workload"), ReadinessUnknown, "workload_unavailable")
					continue
				}
				desired = *count
				if service.DesiredInstances != desired {
					readinessMerge(v.stage("workload"), ReadinessPending, "workload_converging")
				}
			}
			if service.RunningInstances < desired || service.RuntimeReadyInstances < desired {
				readinessMerge(v.stage("workload"), ReadinessPending, "workload_converging")
			}
		}
	}
}

func (c *Core) readinessCertificate(ctx context.Context, v *OperationReadiness, domain model.Domain, baseline *readinessBaseline, now time.Time) string {
	if !domain.HTTPSEnabled {
		return ""
	}
	if domain.CertificateID == "" {
		readinessMerge(v.stage("certificate"), ReadinessPending, "certificate_pending")
		return ""
	}
	certificate, err := c.store.GetCertificate(ctx, domain.CertificateID)
	if err != nil || certificate.PoolID != domain.PoolID {
		readinessMerge(v.stage("certificate"), ReadinessUnknown, "certificate_unavailable")
		return ""
	}
	baseline.Bindings = append(baseline.Bindings, certificate.ID+":"+strconv.FormatInt(certificate.Revision, 10)+":"+certificate.Fingerprint)
	stage := v.stage("certificate")
	switch certificate.Status {
	case model.CertificateError, model.CertificateExpired, model.CertificateRevoked:
		readinessMerge(stage, ReadinessFailed, "certificate_failed")
	case model.CertificateIssued, model.CertificateActive, model.CertificateRenewing:
		if certificate.NotBefore == nil || certificate.NotAfter == nil || !readinessDigest(certificate.Fingerprint) {
			readinessMerge(stage, ReadinessUnknown, "certificate_unavailable")
		} else if now.Before(*certificate.NotBefore) || !now.Before(*certificate.NotAfter) {
			readinessMerge(stage, ReadinessFailed, "certificate_failed")
		} else {
			readinessMerge(stage, ReadinessConfirmed, "")
		}
	default:
		readinessMerge(stage, ReadinessPending, "certificate_pending")
	}
	return certificate.Fingerprint
}

func (c *Core) readinessGateways(ctx context.Context, v *OperationReadiness, baseline *readinessBaseline, poolID string, entrypoint *model.ServiceEntrypoint, cutover *time.Time) {
	pool, err := c.store.GetPool(ctx, poolID)
	if err != nil || !pool.Enabled || pool.DesiredRevision < 1 {
		readinessMerge(v.stage("gateway"), ReadinessUnknown, "gateway_unavailable")
		return
	}
	baseline.Pools[poolID] = pool.DesiredRevision
	sandbox, err := c.GetSandbox(ctx, poolID)
	if err != nil {
		readinessMerge(v.stage("gateway"), ReadinessUnknown, "gateway_unavailable")
		return
	}
	gateways := []model.Gateway{}
	if entrypoint != nil {
		gateway, readErr := c.store.GetGateway(ctx, entrypoint.GatewayID)
		if readErr != nil || gateway.PoolID != poolID {
			readinessMerge(v.stage("gateway"), ReadinessUnknown, "gateway_unavailable")
			return
		}
		gateways = append(gateways, gateway)
	} else {
		page := storage.Page{Limit: 200}
		for {
			values, readErr := c.store.ListGateways(ctx, page)
			if readErr != nil {
				readinessMerge(v.stage("gateway"), ReadinessUnknown, "gateway_unavailable")
				return
			}
			for _, gateway := range values {
				if gateway.PoolID == poolID && gateway.Enabled && gateway.ID != sandbox.GatewayID {
					gateways = append(gateways, gateway)
				}
			}
			if len(values) < page.Limit {
				break
			}
			page.After = values[len(values)-1].ID
		}
	}
	if len(gateways) == 0 {
		readinessMerge(v.stage("gateway"), ReadinessUnknown, "gateway_unavailable")
		return
	}
	for _, gateway := range gateways {
		baseline.Gateways = append(baseline.Gateways, gateway.ID+":"+gateway.ModelDigest+":"+gateway.RenderedDigest)
		target := ReadinessGatewayTarget{GatewayID: gateway.ID, PoolID: poolID, DesiredRevision: pool.DesiredRevision, AppliedRevision: gateway.AppliedRevision, State: ReadinessUnknown, ErrorCode: "gateway_unavailable", ModelDigest: gateway.ModelDigest, RenderedDigest: gateway.RenderedDigest}
		evidence, readErr := c.GatewayEvidence(ctx, gateway.ID)
		if readErr == nil {
			target.ObservedAt = &evidence.ReceivedAt
			if !readinessAfterCutover(cutover, gateway.LastSeenAt, &evidence.ReceivedAt, &evidence.ObservedAt) {
				target.ErrorCode = "recovery_observation_pending"
			} else if !readinessFresh(c.now(), gateway.LastSeenAt, c.config.Controller.Gateway.OfflineTimeout.Std()) || !readinessFresh(c.now(), &evidence.ReceivedAt, 3*time.Duration(evidence.RefreshIntervalMillis)*time.Millisecond) {
				// Cached loading evidence never confirms an unavailable Gateway.
			} else if gateway.Status == model.GatewayApplyFailed || gateway.Status == model.GatewayIncompatible || evidence.ConfigurationState == "EXTERNAL_CHANGE" || evidence.StartupState == "RISK" {
				target.State, target.ErrorCode = ReadinessFailed, "gateway_failed"
			} else if gateway.Enabled && gateway.Status == model.GatewayReady && evidence.StartupState == "CONFIRMED" && evidence.ConfigurationState == "CONFIRMED" && evidence.LoadingConfirmed && gateway.DesiredRevision == pool.DesiredRevision && gateway.AppliedRevision == pool.DesiredRevision && evidence.ConfirmedRevision == pool.DesiredRevision && evidence.LastConfirmedRevision == pool.DesiredRevision && evidence.LastConfirmedAt != nil && readinessAfterCutover(cutover, evidence.LastConfirmedAt) && readinessDigest(gateway.ModelDigest) && readinessDigest(gateway.RenderedDigest) && gateway.ModelDigest == evidence.ModelDigest && gateway.RenderedDigest == evidence.RenderedDigest && evidence.DiskDigest == evidence.RenderedDigest {
				target.State, target.ErrorCode = ReadinessConfirmed, ""
			} else if gateway.Enabled && gateway.Status == model.GatewayReady && evidence.StartupState == "CONFIRMED" && readinessGatewayDigests(gateway, evidence) && (!evidence.LoadingConfirmed || gateway.AppliedRevision != pool.DesiredRevision || gateway.DesiredRevision != pool.DesiredRevision || evidence.ConfirmedRevision != pool.DesiredRevision) {
				target.State, target.ErrorCode = ReadinessPending, "gateway_pending"
			}
		}
		v.GatewayTargets = append(v.GatewayTargets, target)
		readinessMerge(v.stage("gateway"), target.State, target.ErrorCode)
	}
}
