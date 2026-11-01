package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/moby/moby/api/types/swarm"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"path/filepath"
)

type preparationObservation struct {
	Runtime        maintenance.Snapshot `json:"runtime"`
	Configurations map[string]string    `json:"configurations"`
	SecurityDigest string               `json:"security_digest"`
	TrustSHA256    string               `json:"trust_anchor_sha256"`
	NetworkID      string               `json:"network_id"`
}
type preparationInput struct {
	Observation preparationObservation
}

func certificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("certificate evidence is invalid")
	}
	return x509.ParseCertificate(block.Bytes)
}
func (i *Installer) preparationDirectory() string {
	return filepath.Join(i.offlineDir(), "preparation")
}
func (i *Installer) preparedNodeDirectory() string { return i.nodeDir() + "-maintenance" }

// Preparation supports registered protocol-2 PostgreSQL deployments and their
// original identities. It does not guess migration or old-image rollback rights.
func (i *Installer) preparationInput(ctx context.Context, store storage.Store, target *bootstrap.Release, caFile, keyFile string) (preparationInput, error) {
	input := preparationInput{}
	if i.Config.Adoption == nil || i.Record.MetadataLayout != 1 || target == nil {
		return input, &bootstrap.MaintenanceError{Code: "verified_adoption_required"}
	}
	if err := target.Validate(); err != nil {
		return input, err
	}
	if err := i.metadataBoundary(caFile, keyFile); err != nil {
		return input, err
	}
	owner, _, err := (maintenance.Repository{Store: store}).Installation(ctx)
	if err != nil {
		return input, err
	}
	if owner.ID != i.Record.ID || owner.MaintenanceAnchor != i.Record.MaintenanceAnchor || owner.ClusterID != i.Record.ClusterID || owner.ConfigDigest != i.Record.ConfigDigest {
		return input, storage.ErrConflict
	}
	if owner.Executor.ID != "" || owner.Control.ID != "" || owner.CredentialSecret.ID != "" || owner.MaintenanceExpiresAt != nil || i.Record.Resources["reserve:executor"].ID != "" {
		return input, &bootstrap.MaintenanceError{Code: "already_prepared_credentials_must_be_restored"}
	}
	if target.Images.PostgreSQL != owner.Database.Image || i.Record.Database != owner.Database {
		return input, &bootstrap.MaintenanceError{Code: "database_pin_unconfirmed"}
	}
	if caFile == "" || keyFile == "" {
		return input, &bootstrap.MaintenanceError{Code: "original_ca_references_required"}
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return input, err
	}
	keyPEM, err := bootstrap.ReadPrivate(keyFile)
	if err != nil {
		return input, err
	}
	defer clear(keyPEM)
	if _, err = tls.X509KeyPair(caPEM, keyPEM); err != nil {
		return input, &bootstrap.MaintenanceError{Code: "original_ca_pair_unconfirmed"}
	}
	ca, err := certificate(caPEM)
	if err != nil || !ca.IsCA || timeNow().Before(ca.NotBefore) || !timeNow().Before(ca.NotAfter) {
		return input, &bootstrap.MaintenanceError{Code: "original_ca_unconfirmed"}
	}
	fingerprint := sha256.Sum256(ca.Raw)
	input.Observation.TrustSHA256 = hex.EncodeToString(fingerprint[:])
	input.Observation.Configurations = map[string]string{}
	database, err := i.inspectService(ctx, owner.Database.ServiceID)
	if err != nil {
		return input, err
	}
	controller, err := i.inspectService(ctx, owner.Components["controller"].ID)
	if err != nil {
		return input, err
	}
	if controller.Spec.Mode.Global == nil && (controller.Spec.Mode.Replicated == nil || controller.Spec.Mode.Replicated.Replicas == nil || *controller.Spec.Mode.Replicated.Replicas != 1 || len(i.controllerNodes()) != 1) {
		return input, &bootstrap.MaintenanceError{Code: "controller_layout_requires_manual_preparation"}
	}
	user := strings.Split(controller.Spec.TaskTemplate.ContainerSpec.User, ":")
	if len(user) != 2 {
		return input, &bootstrap.MaintenanceError{Code: "nonroot_controller_identity_required"}
	}
	uid, uidErr := strconv.Atoi(user[0])
	gid, gidErr := strconv.Atoi(user[1])
	if uidErr != nil || gidErr != nil || uid < 1024 || gid < 0 {
		return input, &bootstrap.MaintenanceError{Code: "nonroot_controller_identity_required"}
	}
	targetPath := agentConfigPath(controller)
	if targetPath == "/etc/nsc/agent.yaml" {
		return input, &bootstrap.MaintenanceError{Code: "controller_configuration_reference_required"}
	}
	for _, value := range controller.Spec.TaskTemplate.ContainerSpec.Env {
		if strings.HasPrefix(value, "NSC_") {
			return input, &bootstrap.MaintenanceError{Code: "environment_overrides_require_manual_preparation"}
		}
	}
	input.Observation.SecurityDigest = ""
	dataHasController := false
	for _, node := range i.Config.Nodes {
		if !node.Manager {
			continue
		}
		if node.Name == i.Config.DataNode {
			dataHasController = true
		}
		if i.Record.Nodes[node.Name].SocketGID != user[1] {
			return input, &bootstrap.MaintenanceError{Code: "socket_identity_unconfirmed"}
		}
		raw, err := i.registeredFile(ctx, node.Name, controller, targetPath)
		if err != nil {
			return input, err
		}
		loaded, err := config.DecodeOffline(raw, func(path string) ([]byte, error) { return i.registeredFile(ctx, node.Name, controller, path, true) })
		if err != nil {
			return input, err
		}
		if loaded.Storage.Driver != "postgres" || loaded.Controller.LocalGateway || loaded.Controller.InstanceID != i.Record.Nodes[node.Name].InstanceID || len(loaded.Controller.SystemProject.Services) != 4 || loaded.Controller.SystemProject.RuntimeID != owner.RuntimeID || loaded.Controller.SystemProject.ClusterID != owner.ClusterID || loaded.Controller.Internal.Listen != ":9444" {
			return input, &bootstrap.MaintenanceError{Code: "controller_layout_requires_manual_preparation"}
		}
		connection, parseErr := url.Parse(loaded.Storage.DSN)
		if parseErr != nil || (connection.Scheme != "postgres" && connection.Scheme != "postgresql") || (connection.Hostname() != database.Spec.Name && connection.Hostname() != database.ID && connection.Hostname() != "tasks."+database.Spec.Name) || connection.Path != "/nsc" || connection.User == nil || connection.User.Username() != "nsc" {
			return input, &bootstrap.MaintenanceError{Code: "database_connection_identity_unconfirmed"}
		}
		digest := bootstrap.Digest(loaded.Security)
		if input.Observation.SecurityDigest != "" && input.Observation.SecurityDigest != digest {
			return input, &bootstrap.MaintenanceError{Code: "controller_security_inconsistent"}
		}
		input.Observation.SecurityDigest = digest
		originalCA, err := i.registeredFile(ctx, node.Name, controller, loaded.Server.GRPCClientCAFile)
		if err != nil {
			return input, err
		}
		original, err := certificate(originalCA)
		if err != nil || !bytes.Equal(original.Raw, ca.Raw) {
			return input, &bootstrap.MaintenanceError{Code: "original_ca_unconfirmed"}
		}
		input.Observation.Configurations[node.Name] = bootstrap.Digest(raw)
	}
	if !dataHasController {
		return input, &bootstrap.MaintenanceError{Code: "database_binding_requires_controller_on_data_node"}
	}
	rawCopy, err := bootstrap.ReadPrivate(i.Config.Adoption.ControllerConfigFile)
	if err != nil {
		return input, err
	}
	if bootstrap.Digest(rawCopy) != input.Observation.Configurations[i.Config.MaintenanceNode] {
		return input, &bootstrap.MaintenanceError{Code: "operator_configuration_copy_is_stale"}
	}
	for _, network := range controller.Spec.TaskTemplate.Networks {
		for _, attached := range database.Spec.TaskTemplate.Networks {
			if network.Target == attached.Target {
				if input.Observation.NetworkID != "" && input.Observation.NetworkID != network.Target {
					return input, &bootstrap.MaintenanceError{Code: "maintenance_network_is_ambiguous"}
				}
				input.Observation.NetworkID = network.Target
			}
		}
	}
	if input.Observation.NetworkID == "" {
		return input, &bootstrap.MaintenanceError{Code: "maintenance_network_unconfirmed"}
	}
	var members []swarm.Node
	if err = i.entry().Engine(ctx, "GET", "/nodes", nil, &members); err != nil {
		return input, err
	}
	for _, identity := range i.Record.Nodes {
		ready := false
		for _, member := range members {
			if member.ID == identity.NodeID && member.Status.State == swarm.NodeStateReady && member.Spec.Availability == swarm.NodeAvailabilityActive {
				ready = true
			}
		}
		if !ready {
			return input, &bootstrap.MaintenanceError{Code: "preparation_requires_ready_original_nodes"}
		}
	}
	runtime := &maintenance.DockerRuntime{Engine: i.entry(), Record: i.Record, Schema: maintenance.SchemaVerifier(store), Physical: func(ctx context.Context) error { return i.checkNodeBindings(ctx, "preparation") }}
	input.Observation.Runtime, err = runtime.Observe(ctx)
	if err != nil {
		return input, err
	}
	if !input.Observation.Runtime.Topology.QuorumAvailable {
		return input, &bootstrap.MaintenanceError{Code: "manager_unavailable"}
	}
	for key, expected := range owner.Components {
		actual := input.Observation.Runtime.Components[key]
		if expected.ID != actual.ID || expected.SpecDigest != actual.SpecDigest || actual.Version < expected.Version {
			return input, &bootstrap.MaintenanceError{Code: "external_drift"}
		}
	}
	return input, nil
}

func (i *Installer) PlanWebPreparation(ctx context.Context, action bootstrap.Action) (bootstrap.Plan, error) {
	if action != bootstrap.Upgrade {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "preparation_requires_upgrade"}
	}
	var result bootstrap.Plan
	err := i.withStore(ctx, true, func(store storage.Store, _ config.Config) error {
		owner, _, err := (maintenance.Repository{Store: store}).Installation(ctx)
		if err != nil {
			return err
		}
		if owner.OperationID != "" || i.Record.Preparation != nil {
			return &bootstrap.MaintenanceError{Code: "operation_active"}
		}
		adopted := false
		for _, operation := range i.Record.Operations {
			if operation.State == bootstrap.Pending || operation.State == bootstrap.Running || operation.State == bootstrap.Unknown || operation.State == bootstrap.Blocked {
				return &bootstrap.MaintenanceError{Code: "operation_active"}
			}
			if operation.Plan.Action == bootstrap.Adopt && operation.State == bootstrap.Succeeded {
				adopted = true
			}
		}
		if !adopted {
			return &bootstrap.MaintenanceError{Code: "verified_adoption_required"}
		}
		input, err := i.preparationInput(ctx, store, i.Target, i.OriginalCACertificateFile, i.OriginalCAPrivateKeyFile)
		if err != nil {
			return err
		}
		steps := []bootstrap.Step{{Key: "prepare:backup", Description: "Retain a current PostgreSQL archive and matching original configuration and identities"}, {Key: "prepare:images", Description: "Verify approved target images on every original node"}, {Key: "prepare:materials", Description: "Issue the first maintenance identity with the original CA and a separate IP console certificate"}, {Key: "prepare:secret", Description: "Create the protected maintenance Secret"}, {Key: "prepare:control", Description: "Create the non-secret installation checkpoint"}, {Key: "prepare:executor", Description: "Reserve the independent executor without starting it"}}
		for _, node := range i.controllerNodes() {
			steps = append(steps, bootstrap.Step{Key: "prepare:files:" + node.Name, Description: "Write new private Controller configuration without overwriting original files"})
		}
		before := input.Observation.Runtime.Components["controller"]
		steps = append(steps, bootstrap.Step{Key: "prepare:publish", Description: "Register the new console asset and maintenance identity without resetting users"}, bootstrap.Step{Key: "prepare:controller", Before: &before, Description: "Update the original Controller service with exact object-version preconditions"}, bootstrap.Step{Key: "prepare:controller-ready", Description: "Verify fresh Controller, Manager and existing system-application evidence"})
		for _, key := range []string{"nginx", "nginx-sandbox"} {
			before := input.Observation.Runtime.Components[key]
			steps = append(steps, bootstrap.Step{Key: "update:" + key, Before: &before, Description: "Update only the existing owned Gateway component"}, bootstrap.Step{Key: "verify:" + key, Description: "Verify target tasks, actual loading and access"})
		}
		steps = append(steps, bootstrap.Step{Key: "prepare:handoff", Description: "Verify the existing administrator, positive loaded configuration and independent HTTPS"}, bootstrap.Step{Key: "prepare:complete", Description: "Establish the first verified release baseline; retain original data and identities"})
		result, err = bootstrap.NewPlan(*i.Record, bootstrap.Upgrade, input.Observation, steps, timeNow())
		if err != nil {
			return err
		}
		scope := bootstrap.PlanScope{Preparation: &bootstrap.PreparationPlan{Proof: bootstrap.Digest(input.Observation), TrustSHA256: input.Observation.TrustSHA256}, Release: i.Target, Mode: i.Config.Mode, Targets: input.Observation.Runtime.Targets, ClusterID: owner.ClusterID, Images: i.Target.Images, DataNode: owner.Database.NodeID, DataVolume: owner.Database.Volume, DatabaseSinglePointOfFailure: true, Topology: input.Observation.Runtime.Topology}
		result.Impact = append(result.Impact, "CLI-only preparation changes Controller configuration and its independent HTTPS certificate; existing service IDs, database, administrators and business bindings are preserved.", "The unverified legacy image is retained for protected manual recovery, not automatic image rollback.")
		result = result.WithScope(scope)
		return nil
	})
	return result, err
}

func (i *Installer) ExecuteWebPreparation(ctx context.Context, plan bootstrap.Plan, approval, key string) (bootstrap.Operation, error) {
	var result bootstrap.Operation
	keyHash := bootstrap.Digest([]string{i.Record.ID, key})
	for _, operation := range i.Record.Operations {
		if operation.Key == keyHash {
			if operation.Plan.Hash != plan.Hash {
				return result, storage.ErrConflict
			}
			return operation, nil
		}
	}
	if os.Geteuid() != i.Record.MaintenanceUID {
		return result, &bootstrap.MaintenanceError{Code: "first_preparation_requires_original_maintenance_owner"}
	}
	err := i.withStore(ctx, false, func(store storage.Store, _ config.Config) error {
		input, err := i.preparationInput(ctx, store, plan.Scope.Release, i.OriginalCACertificateFile, i.OriginalCAPrivateKeyFile)
		if err != nil {
			return err
		}
		if plan.Scope.Preparation == nil || plan.Scope.Preparation.Proof != bootstrap.Digest(input.Observation) {
			return &bootstrap.MaintenanceError{Code: "stale_plan"}
		}
		if err = plan.Validate(*i.Record, approval, input.Observation, timeNow()); err != nil {
			return err
		}
		repository := maintenance.Repository{Store: store}
		job, err := repository.ClaimCLI(ctx, plan, key, "cli")
		if err != nil {
			return err
		}
		i.Record.Preparation = &bootstrap.PreparationReferences{CACertificateFile: i.OriginalCACertificateFile, CAPrivateKeyFile: i.OriginalCAPrivateKeyFile, PreviousCertificateID: i.Record.CertificateID, SourceConfigurationHashes: input.Observation.Configurations}
		i.Record.CertificateID = id.New(id.Certificate)
		i.Record.ConsoleFingerprint = ""
		i.Record.ConsoleCertificateExpiresAt = nil
		i.Record.TrustSHA256 = plan.Scope.Preparation.TrustSHA256
		i.Record.Resources["network:control"] = bootstrap.Resource{Kind: "registered-network", ID: input.Observation.NetworkID, SpecDigest: bootstrap.Digest(input.Observation.NetworkID)}
		i.Target = plan.Scope.Release
		checkpoint := &maintenance.Checkpointer{Journal: i.Journal, Repository: repository, Engine: i.entry(), Job: &job}
		driver := &webPreparationDriver{Installer: i, Store: store, Checkpoint: checkpoint, Input: &input, Plan: plan}
		result, err = (bootstrap.Executor{Journal: checkpoint, Driver: driver}).Execute(ctx, i.Record, plan, approval, job.Key)
		return err
	})
	return result, err
}
func (i *Installer) ResumeWebPreparation(ctx context.Context, operationID string) (bootstrap.Operation, error) {
	var result bootstrap.Operation
	if i.Record.Preparation == nil {
		return result, &bootstrap.MaintenanceError{Code: "preparation_references_unavailable"}
	}
	err := i.withStore(ctx, false, func(store storage.Store, _ config.Config) error {
		repository := maintenance.Repository{Store: store}
		installation, _, err := repository.Installation(ctx)
		if err != nil {
			return err
		}
		job, _, err := repository.Job(ctx, installation.OperationID)
		if err != nil {
			return err
		}
		for _, operation := range i.Record.Operations {
			if operation.ID != operationID {
				continue
			}
			if operation.Key != job.Key || operation.Plan.Hash != job.Plan.Hash || operation.Plan.Scope.Preparation == nil {
				return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
			}
			checkpoint := &maintenance.Checkpointer{Journal: i.Journal, Repository: repository, Engine: i.entry(), Job: &job}
			driver := &webPreparationDriver{Installer: i, Store: store, Checkpoint: checkpoint, Plan: operation.Plan}
			result, err = (bootstrap.Executor{Journal: checkpoint, Driver: driver}).Resume(ctx, i.Record, operationID)
			return err
		}
		return &bootstrap.MaintenanceError{Code: "operation_claim_missing"}
	})
	return result, err
}

type webPreparationDriver struct {
	Installer  *Installer
	Store      storage.Store
	Checkpoint *maintenance.Checkpointer
	Input      *preparationInput
	Plan       bootstrap.Plan
}

func (d *webPreparationDriver) Observe(context.Context) (any, error) {
	if d.Input == nil {
		return nil, fmt.Errorf("approved preparation evidence is unavailable")
	}
	return d.Input.Observation, nil
}
func (d *webPreparationDriver) runtime() *maintenance.DockerRuntime {
	i := d.Installer
	return &maintenance.DockerRuntime{Engine: i.entry(), Record: i.Record, Schema: maintenance.SchemaVerifier(d.Store), Physical: func(ctx context.Context) error { return i.checkNodeBindings(ctx, "preparation") }, Health: func(ctx context.Context) error { return i.preparedHealth(ctx, d.Store) }}
}
func (d *webPreparationDriver) Check(ctx context.Context, step bootstrap.Step) error {
	i := d.Installer
	if err := i.checkNodeBindings(ctx, "preparation"); err != nil {
		return err
	}
	if err := maintenance.SchemaVerifier(d.Store)(ctx); err != nil {
		return err
	}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		resource := i.Record.Resources["reserve:"+key]
		if key == "postgresql" {
			resource = i.Record.Resources["service:postgresql"]
		}
		latest := maintenance.Latest(i.Record, resource.ID)
		actual, err := i.inspectService(ctx, resource.ID)
		if err != nil {
			return err
		}
		if latest.ID == "" || actual.ID != latest.ID || !maintenance.MatchesRecordedSpec(i.Record, latest, maintenance.Receipt(actual)) || actual.Version.Index < latest.Version {
			return &bootstrap.MaintenanceError{Code: "external_drift"}
		}
	}
	for _, node := range i.controllerNodes() {
		if receipt := i.Record.Resources["prepare:files:"+node.Name]; receipt.ID != "" {
			if err := i.checkPreparedFiles(ctx, node.Name, receipt.SpecDigest); err != nil {
				return err
			}
		}
	}
	return nil
}
func (d *webPreparationDriver) Apply(ctx context.Context, step bootstrap.Step) (bootstrap.Resource, error) {
	i := d.Installer
	proof := func(kind string, value any) bootstrap.Resource {
		return bootstrap.Resource{Kind: kind, ID: i.Record.ID, SpecDigest: bootstrap.Digest(value)}
	}
	switch {
	case step.Key == "prepare:backup":
		return i.backupPreparation(ctx, d.Store)
	case step.Key == "prepare:images":
		view := *i
		view.Config = i.Config
		view.Config.Images = d.Plan.Scope.Release.Images
		record := *i.Record
		view.Record = &record
		return view.prepareImages(ctx)
	case step.Key == "prepare:materials":
		if err := i.makePreparationMaterials(); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: err.Error()}
		}
		digest, err := i.materialDigest()
		return bootstrap.Resource{Kind: "materials", ID: i.Record.ID, SpecDigest: digest}, err
	case step.Key == "prepare:secret":
		value, err := i.createMaintenanceCredential(ctx)
		if err == nil {
			i.Record.Resources["secret:maintenance"] = value
		}
		return value, err
	case step.Key == "prepare:control":
		value, err := i.createControl(ctx)
		if err == nil {
			i.Record.Resources["control"] = value
		}
		return value, err
	case step.Key == "prepare:executor":
		view := *i
		view.Config = i.Config
		view.Config.Images = d.Plan.Scope.Release.Images
		gid, err := i.socketGID(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		value, err := view.createService(ctx, view.serviceSpec("executor", gid, true))
		if err == nil {
			i.Record.Resources["reserve:executor"] = value
		}
		return value, err
	case strings.HasPrefix(step.Key, "prepare:files:"):
		return i.distributePreparedFiles(ctx, strings.TrimPrefix(step.Key, "prepare:files:"), d.Checkpoint.Save)
	case step.Key == "prepare:publish":
		cfg, err := i.localConfig(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		owner, _, err := (maintenance.Repository{Store: d.Store}).Installation(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		owner.CertificateID = i.Record.CertificateID
		if err = seedConsoleCertificate(ctx, d.Store, ring, owner, i.material("console.crt"), i.material("console.key")); err != nil {
			return bootstrap.Resource{}, err
		}
		return proof("preparation-registration", i.Record.MaintenanceID), nil
	case step.Key == "prepare:controller":
		return i.updatePreparedController(ctx, step, d.Plan.Scope.Release, d.Checkpoint.Save)
	case step.Key == "prepare:controller-ready":
		if err := d.runtime().Verify(ctx, "controller"); err != nil {
			return bootstrap.Resource{}, err
		}
		if err := i.verifyController(ctx); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "prepared Controller evidence is unavailable"}
		}
		return proof("preparation-verification", "controller"), nil
	case strings.HasPrefix(step.Key, "update:") || strings.HasPrefix(step.Key, "verify:"):
		driver := maintenance.Driver{Record: i.Record, Runtime: d.runtime(), Journal: d.Checkpoint, Target: d.Plan.Scope.Release}
		return driver.Apply(ctx, step)
	case step.Key == "prepare:handoff":
		if err := d.runtime().Verify(ctx, "installation"); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "prepared installation health or access is unconfirmed"}
		}
		if err := i.verifyPreparedLogin(ctx); err != nil {
			// Detailed transport/credential errors stay in restricted recovery
			// material, never in the public operation or ordinary process log.
			_ = bootstrap.AtomicPrivateFile(i.preparationDirectory(), "login-verification-error.txt", []byte(err.Error()))
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "existing administrator login is unconfirmed"}
		}
		return proof("preparation-verification", "handoff"), nil
	case step.Key == "prepare:complete":
		release := *d.Plan.Scope.Release
		i.Record.Release = &release
		i.Record.History = []bootstrap.ReleaseReceipt{}
		return proof("preparation-completion", release), nil
	default:
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "preparation step is outside the allowlist"}
	}
}
func (d *webPreparationDriver) Reconcile(ctx context.Context, step bootstrap.Step) (*bootstrap.Resource, error) {
	i := d.Installer
	if strings.HasPrefix(step.Key, "update:") || strings.HasPrefix(step.Key, "verify:") {
		return (&maintenance.Driver{Record: i.Record, Runtime: d.runtime(), Journal: d.Checkpoint, Target: d.Plan.Scope.Release}).Reconcile(ctx, step)
	}
	switch {
	case step.Key == "prepare:controller":
		intent := i.Record.Resources["intent:prepare:controller"]
		identifier := intent.ID
		if identifier == "" && step.Before != nil {
			identifier = step.Before.ID
		}
		actual, err := i.inspectService(ctx, identifier)
		if err != nil {
			return nil, err
		}
		if actual.ID == intent.ID && bootstrap.Digest(actual.Spec) == intent.SpecDigest && actual.Version.Index > intent.Version {
			value := serviceReceipt(actual)
			return &value, nil
		}
		if step.Before != nil && actual.ID == step.Before.ID && bootstrap.Digest(actual.Spec) == step.Before.SpecDigest && actual.Version.Index >= step.Before.Version {
			return nil, &bootstrap.RetrySafe{Reason: "the original CAS precondition still holds"}
		}
	case step.Key == "prepare:backup" || step.Key == "prepare:images" || step.Key == "prepare:controller-ready" || step.Key == "prepare:publish" || step.Key == "prepare:handoff" || step.Key == "prepare:complete":
		return nil, &bootstrap.RetrySafe{Reason: "repeat only the verified read-only or idempotent metadata continuation"}
	case step.Key == "prepare:materials":
		if _, err := os.Lstat(i.materialDir()); os.IsNotExist(err) {
			return nil, &bootstrap.RetrySafe{Reason: "the first identity directory was never created"}
		}
		if err := i.checkPreparationMaterials(); err != nil {
			return nil, err
		}
		digest, err := i.materialDigest()
		value := bootstrap.Resource{Kind: "materials", ID: i.Record.ID, SpecDigest: digest}
		return &value, err
	case strings.HasPrefix(step.Key, "prepare:files:"):
		name := strings.TrimPrefix(step.Key, "prepare:files:")
		intent := i.Record.Resources["intent:"+step.Key]
		if intent.ID == "" {
			return nil, &bootstrap.RetrySafe{Reason: "no node file mutation was issued before its durable intent"}
		}
		if intent.ID != "" && i.checkPreparedFiles(ctx, name, intent.SpecDigest) == nil {
			if err := i.retainPreparedConfiguration(name); err != nil {
				return nil, err
			}
			return &intent, nil
		}
	default:
		references := map[string]struct{ kind, key string }{
			"prepare:secret":   {"secret", "secret:maintenance"},
			"prepare:control":  {"config", "control"},
			"prepare:executor": {"service", "reserve:executor"},
		}
		if step.Receipt != nil {
			value := *step.Receipt
			reference := references[step.Key]
			if value.Kind != reference.kind {
				return nil, fmt.Errorf("preparation receipt kind differs from its approved resource")
			}
			if value.Kind == "service" {
				actual, err := i.inspectService(ctx, value.ID)
				if err == nil && bootstrap.Digest(actual.Spec) == value.SpecDigest {
					receipt := serviceReceipt(actual)
					i.Record.Resources[reference.key] = receipt
					return &receipt, nil
				}
			}
			if err := i.checkReceipt(ctx, step); err == nil {
				i.Record.Resources[reference.key] = value
				return &value, nil
			}
		}
	}
	return nil, fmt.Errorf("preparation outcome requires its original receipt; resource names cannot establish ownership")
}

func (i *Installer) preparedHealth(ctx context.Context, store storage.Store) error {
	owner, _, err := (maintenance.Repository{Store: store}).Installation(ctx)
	if err != nil {
		return err
	}
	credential, err := i.readMaintenanceCredential()
	if err != nil {
		return err
	}
	health, err := maintenance.HealthClient(owner, credential)
	if err != nil {
		return err
	}
	return health(ctx)
}
func (i *Installer) verifyPreparedInstallation(ctx context.Context, output io.Writer) error {
	err := i.withStore(ctx, true, func(store storage.Store, _ config.Config) error {
		runtime := (&webPreparationDriver{Installer: i, Store: store}).runtime()
		return runtime.Verify(ctx, "installation")
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{"installation_id": i.Record.ID, "health": "VERIFIED", "database_single_point_of_failure": true})
}
