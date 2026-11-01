package install

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (i *Installer) maintenanceDriver(ctx context.Context, store storage.Store) (*maintenance.Driver, bootstrap.Installation, error) {
	repository := maintenance.Repository{Store: store}
	installation, _, err := repository.Installation(ctx)
	if err != nil {
		return nil, installation, err
	}
	if installation.ID != i.Record.ID || installation.MaintenanceAnchor != i.Record.MaintenanceAnchor || installation.ClusterID != i.Record.ClusterID {
		return nil, installation, storage.ErrConflict
	}
	if i.Record.Database.ServiceID != "" && i.Record.Database != installation.Database {
		return nil, installation, storage.ErrConflict
	}
	i.Record.Database = installation.Database
	if i.Record.Release == nil {
		i.Record.Release, i.Record.History = installation.Release, installation.History
	}
	if i.Record.Release == nil {
		i.Record.Release, err = i.adoptedBaseline(ctx, installation)
		if err != nil {
			return nil, installation, err
		}
	}
	health := func(ctx context.Context) error { return i.verifyHandoff(ctx) }
	if installation.MaintenanceID != "" && installation.TrustSHA256 != "" {
		credential, err := i.readMaintenanceCredential()
		if err != nil {
			return nil, installation, err
		}
		health, err = maintenance.HealthClient(installation, credential)
		if err != nil {
			return nil, installation, err
		}
	}

	auth, err := i.registryAuthorizations()
	if err != nil {
		return nil, installation, err
	}
	runtime := &maintenance.DockerRuntime{RegistryAuth: auth, Prepare: func(ctx context.Context, target bootstrap.Release) (bootstrap.Resource, error) {
		view := *i
		view.Config = i.Config
		view.Config.Images = target.Images
		return view.prepareImages(ctx)
	}, Engine: i.entry(), Record: i.Record, Schema: maintenance.SchemaVerifier(store), Physical: func(ctx context.Context) error { return i.checkNodeBindings(ctx, "maintenance") }, Health: health}
	var driverRuntime maintenance.Runtime = runtime
	if i.MaintenanceRuntime != nil {
		driverRuntime = i.MaintenanceRuntime(i.Record, store)
	}
	return &maintenance.Driver{Record: i.Record, Runtime: driverRuntime, Journal: i.Journal}, installation, nil
}
func (i *Installer) PlanMaintenance(ctx context.Context, action bootstrap.Action) (bootstrap.Plan, error) {
	if i.Record.MetadataLayout == 1 {
		if err := i.metadataBoundary(); err != nil {
			return bootstrap.Plan{}, err
		}
	}
	var plan bootstrap.Plan
	err := i.withStore(ctx, true, func(store storage.Store, _ config.Config) error {
		driver, _, err := i.maintenanceDriver(ctx, store)
		if err != nil {
			return err
		}
		request := bootstrap.PlanRequest{Action: action, Revision: i.Record.Revision}
		if action == bootstrap.Upgrade {
			request.Release = i.Target
		}
		if action == bootstrap.Repair {
			request.Components = i.Components
		}
		plan, err = driver.Plan(ctx, request)
		return err
	})
	return plan, err
}
func (i *Installer) ExecuteMaintenance(ctx context.Context, plan bootstrap.Plan, approval, key string) (bootstrap.Operation, error) {
	if plan.Scope.Preparation != nil {
		return i.ExecuteWebPreparation(ctx, plan, approval, key)
	}
	var result bootstrap.Operation
	err := i.withStore(ctx, false, func(store storage.Store, _ config.Config) error {
		driver, _, err := i.maintenanceDriver(ctx, store)
		if err != nil {
			return err
		}
		observed, err := driver.Observe(ctx)
		if err != nil {
			return err
		}
		// Duplicate approvals return their original operation even after the record
		// revision changes. A different plan can never reuse the same key.
		keyHash := bootstrap.Digest([]string{i.Record.ID, key})
		for _, operation := range i.Record.Operations {
			if operation.Key == keyHash {
				if operation.Plan.Hash != plan.Hash {
					return &bootstrap.MaintenanceError{Code: "idempotency_conflict"}
				}
				result = operation
				return nil
			}
		}
		if err = plan.Validate(*i.Record, approval, observed, timeNow()); err != nil {
			return err
		}
		repository := maintenance.Repository{Store: store}
		job, err := repository.ClaimCLI(ctx, plan, key, "cli")
		if err != nil {
			return err
		}
		checkpoint := &maintenance.Checkpointer{Journal: i.Journal, Repository: repository, Engine: i.entry(), Job: &job}
		driver.Journal, driver.Target, driver.Components = checkpoint, plan.Scope.Release, plan.Scope.Components
		executor := bootstrap.Executor{Journal: checkpoint, Driver: driver}
		result, err = executor.Execute(ctx, i.Record, plan, approval, job.Key)
		return err
	})
	return result, err
}
func (i *Installer) ResumeMaintenance(ctx context.Context, id string) (bootstrap.Operation, error) {
	for _, operation := range i.Record.Operations {
		if operation.ID == id && operation.Plan.Scope.Preparation != nil {
			return i.ResumeWebPreparation(ctx, id)
		}
	}
	var result bootstrap.Operation
	err := i.withStore(ctx, false, func(store storage.Store, _ config.Config) error {
		driver, installation, err := i.maintenanceDriver(ctx, store)
		if err != nil {
			return err
		}
		if installation.OperationID == "" {
			return &bootstrap.MaintenanceError{Code: "operation_claim_missing"}
		}
		repository := maintenance.Repository{Store: store}
		job, _, err := repository.Job(ctx, installation.OperationID)
		if err != nil {
			return err
		}
		found := false
		for _, operation := range i.Record.Operations {
			if operation.ID == id && operation.Key == job.Key && operation.Plan.Hash == job.Plan.Hash {
				found = true
			}
		}
		if !found {
			return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
		}
		checkpoint := &maintenance.Checkpointer{Journal: i.Journal, Repository: repository, Engine: i.entry(), Job: &job}
		driver.Journal, driver.Target, driver.Components = checkpoint, job.Plan.Scope.Release, job.Plan.Scope.Components
		result, err = (bootstrap.Executor{Journal: checkpoint, Driver: driver}).Resume(ctx, i.Record, id)
		return err
	})
	return result, err
}

// DecodeMaintenanceRelease is strict and bounded like the CLI configuration.
func DecodeMaintenanceRelease(path string) (*bootstrap.Release, error) {
	data, err := bootstrap.ReadPrivate(path)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage = data
	value, err := bootstrap.DecodeRelease(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// Legacy adoption remains CLI-only. A baseline can be established only from
// exact currently registered immutable services with the known compatibility
// contracts; an unknown schema or mutable old image never gains rollback rights.
func (i *Installer) adoptedBaseline(ctx context.Context, installation bootstrap.Installation) (*bootstrap.Release, error) {
	if i.Config.Adoption == nil {
		return nil, &bootstrap.MaintenanceError{Code: "baseline_missing"}
	}
	result := bootstrap.Release{Version: "adopted", Images: bootstrap.Images{Controller: installation.Components["controller"].Image, Gateway: installation.Components["nginx"].Image, PostgreSQL: installation.Database.Image}, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	for key, image := range map[string]string{"controller": result.Images.Controller, "gateway": result.Images.Gateway} {
		var actual struct {
			Config struct{ Labels map[string]string }
		}
		if err := i.entry().Engine(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, &actual); err != nil {
			return nil, err
		}
		labels := actual.Config.Labels
		if labels["io.nsc.bootstrap.contract"] != "1" || labels["io.nsc.bootstrap.protocol"] != "2" || key == "controller" && labels["io.nsc.bootstrap.schema"] != "18" {
			return nil, &bootstrap.MaintenanceError{Code: "baseline_missing"}
		}
		if key == "controller" && labels["org.opencontainers.image.version"] != "" {
			result.Version = labels["org.opencontainers.image.version"]
		}
	}
	return &result, nil
}
