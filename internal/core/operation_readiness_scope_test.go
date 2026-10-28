package core

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func serviceReadinessScopeFixture(t *testing.T) (serviceReviewBoundaryFixture, map[string]model.Service) {
	t.Helper()
	f := serviceReviewBoundarySetup(t)
	now := f.core.now()
	f.core.now = func() time.Time { return now }
	services, err := f.core.store.ListAllServicesFiltered(t.Context(), storage.ServiceFilter{ApplicationID: f.op.ApplicationID})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]model.Service{}
	for _, service := range services {
		service.RuntimeObjectID = "docker-" + service.Name
		service.LastSeenAt = &now
		service.DesiredInstances, service.RunningInstances, service.RuntimeReadyInstances = 1, 1, 1
		byName[service.Name] = service
	}
	observeScopedServices(t, &f, byName)
	for name := range byName {
		byName[name], err = f.core.store.GetService(t.Context(), byName[name].ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f, byName
}

func observeScopedServices(t *testing.T, f *serviceReviewBoundaryFixture, services map[string]model.Service) {
	t.Helper()
	discovered := []model.DiscoveredService{}
	for _, service := range services {
		discovered = append(discovered, readinessDiscoveredService(service))
	}
	runtime, err := f.sql.GetRuntime(t.Context(), f.runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.sql.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: runtime.AppliedObservation + 1, ObservedAt: f.core.now(), Services: discovered}); err != nil {
		t.Fatal(err)
	}
	f.runtime, err = f.sql.GetRuntime(t.Context(), runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.Type = model.RuntimeSwarm
	f.core.store = networkRuntimeStore{Store: f.sql, runtime: f.runtime}
}

func finishScopedOperation(t *testing.T, f serviceReviewBoundaryFixture, op model.DeploymentOperation) {
	t.Helper()
	if _, err := f.sql.DB().ExecContext(t.Context(), "UPDATE deployment_operations SET status='SUCCEEDED',finished_at=? WHERE id=?", f.core.now(), op.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessServiceHeadSurvivesUnrelatedUpdate(t *testing.T) {
	f, services := serviceReadinessScopeFixture(t)
	ctx := t.Context()
	web := services["web"]
	op, err := f.core.QueueServiceOperation(ctx, web.ID, "SCALE", 2, "web-two", ServiceOperationOptions{Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateServiceRevision(ctx, f.runtime.ID, f.op.ApplicationID, op.RevisionID, op.RevisionID, []string{web.ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, op)
	web.DesiredInstances, web.RunningInstances, web.RuntimeReadyInstances = 2, 2, 2
	services["web"] = web
	observeScopedServices(t, &f, services)
	before, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || before.stage("workload").State != ReadinessConfirmed {
		t.Fatal(before, err)
	}
	old, err := f.core.store.GetDeploymentOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := f.core.GetEnvironment(ctx, f.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := f.core.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: "services:\n  api:\n    image: example.test/api@sha256:" + strings.Repeat("d", 64) + "\n    environment: {TOKEN: '${TOKEN}'}\n", RuntimeEnv: f.runtimeEnv, ServiceMode: "update"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.core.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "fixture", "api-change", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated pending revision must not change the client's target baseline.
	pending, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || pending.BaselineDigest != before.BaselineDigest {
		t.Fatal("unrelated desired revision invalidated target", pending, err)
	}
	if err = f.sql.ActivateServiceRevision(ctx, f.runtime.ID, f.op.ApplicationID, sibling.RevisionID, sibling.RevisionID, []string{services["api"].ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, sibling)
	observeScopedServices(t, &f, services)
	after, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || after.stage("workload").State != ReadinessConfirmed || after.BaselineDigest != before.BaselineDigest {
		t.Fatal("unchanged service became superseded", before, after, err)
	}
	stored, err := f.core.store.GetDeploymentOperation(ctx, op.ID)
	if err != nil || !reflect.DeepEqual(old, stored) {
		t.Fatal("readiness rewrote operation history", err)
	}
	web, err = f.core.store.GetService(ctx, web.ID)
	if err != nil {
		t.Fatal(err)
	}
	newHead, err := f.core.QueueServiceOperation(ctx, web.ID, "SCALE", 3, "web-three", ServiceOperationOptions{Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateServiceRevision(ctx, f.runtime.ID, f.op.ApplicationID, newHead.RevisionID, newHead.RevisionID, []string{web.ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, newHead)
	superseded, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || superseded.stage("workload").ErrorCode != "workload_superseded" || superseded.BaselineDigest == before.BaselineDigest {
		t.Fatal("changed target reused former evidence", superseded, err)
	}
}

func TestReadinessLifecycleUsesItsOwnTargetsAfterIncrementalSubmission(t *testing.T) {
	f, services := serviceReadinessScopeFixture(t)
	ctx := t.Context()
	web, api := services["web"], services["api"]
	op, err := f.core.QueueServiceOperation(ctx, api.ID, "SCALE", 1, "api-scope", ServiceOperationOptions{Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateServiceRevision(ctx, f.runtime.ID, f.op.ApplicationID, op.RevisionID, op.RevisionID, []string{api.ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, op)
	for _, action := range []string{"START", "REDEPLOY"} {
		lifecycle, err := f.core.QueueApplicationOperation(ctx, f.op.ApplicationID, action, "whole-"+action)
		if err != nil {
			t.Fatal(err)
		}
		finishScopedOperation(t, f, lifecycle)
		web.RuntimeReadyInstances = 0
		services["web"] = web
		observeScopedServices(t, &f, services)
		v, err := f.core.DeploymentReadiness(ctx, lifecycle.ID)
		if err != nil || v.stage("workload").State != ReadinessPending {
			t.Fatal("whole application ignored unhealthy sibling", action, v, err)
		}
	}
	for _, action := range []string{"RESTART", "SCALE"} {
		serviceOp, err := f.core.QueueServiceOperation(ctx, web.ID, action, 1, "target-"+action)
		if err != nil {
			t.Fatal(err)
		}
		finishScopedOperation(t, f, serviceOp)
		web.RuntimeReadyInstances = 1
		services["web"] = web
		observeScopedServices(t, &f, services)
		v, err := f.core.DeploymentReadiness(ctx, serviceOp.ID)
		if err != nil || v.stage("workload").State != ReadinessConfirmed {
			t.Fatal("service lifecycle inherited sibling scope", action, v, err)
		}
	}
}

func TestDeleteReviewedServiceClearsOnlyRemovedMetadata(t *testing.T) {
	f, services := serviceReadinessScopeFixture(t)
	ctx := t.Context()
	base, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	base.ConfigurationSource.TaggedImages = map[string]string{"web": "example.test/web:stable", "api": "example.test/api:stable"}
	base.ConfigurationSource.Entrypoint = &model.ServiceEntrypoint{Service: "web", Host: "removed.example.test", Port: 8080}
	source, err := json.Marshal(base.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(source), base.ID); err != nil {
		t.Fatal(err)
	}
	web := services["web"]
	op, err := f.core.QueueDeleteService(ctx, web.ID, "fixture", "delete-web", web.Revision, true)
	if err != nil {
		t.Fatal("reviewed image locks blocked deletion", err)
	}
	revision, err := f.core.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]string{revision.ConfigurationSource.Images, revision.ConfigurationSource.TaggedImages, revision.ConfigurationSource.ServiceRevisions} {
		if _, exists := values["web"]; exists {
			t.Fatal("deleted metadata retained", values)
		}
		if values["api"] == "" {
			t.Fatal("retained service metadata lost", values)
		}
	}
	if revision.ConfigurationSource.Entrypoint != nil || len(revision.ConfigurationSource.EntrypointIntents) != 0 {
		t.Fatal("deleted service retained ingress", revision.ConfigurationSource)
	}
	currentBase, err := f.core.store.GetApplicationRevision(ctx, base.ID)
	if err != nil || !reflect.DeepEqual(currentBase.ConfigurationSource, base.ConfigurationSource) {
		t.Fatal("deletion mutated immutable base", err)
	}
	retained, err := f.core.ApplicationSource(ctx, f.op.ApplicationID, revision.ID)
	if err != nil || strings.Contains(retained.ComposeYAML, "  web:") || !strings.Contains(retained.ComposeYAML, "  api:") {
		t.Fatal(retained, err)
	}
	replayed, err := f.core.QueueDeleteService(ctx, web.ID, "fixture", "delete-web", web.Revision, true)
	if err != nil || replayed.ID != op.ID {
		t.Fatal("delete replay created another revision", replayed, err)
	}
	if err = f.sql.ActivateApplicationRevision(ctx, f.runtime.ID, f.op.ApplicationID, op.RevisionID, op.RevisionID, []string{services["api"].ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, op)
	before, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || before.stage("workload").State == ReadinessConfirmed {
		t.Fatal("old inventory falsely confirmed removal", before, err)
	}
	clock := f.core.now().Add(time.Second)
	f.core.now = func() time.Time { return clock }
	delete(services, "web")
	observeScopedServices(t, &f, services)
	after, err := f.core.DeploymentReadiness(ctx, op.ID)
	if err != nil || after.stage("workload").State != ReadinessConfirmed || len(after.AccessTargets) != 0 || after.stage("gateway").State != ReadinessNotApplicable {
		t.Fatal("fresh absence did not confirm deletion", after, err)
	}
	resume, err := f.core.QueueApplicationOperation(ctx, f.op.ApplicationID, "REDEPLOY", "resume-after-delete")
	if err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, resume)
	resumed, err := f.core.DeploymentReadiness(ctx, resume.ID)
	if err != nil || resumed.stage("workload").State != ReadinessConfirmed {
		t.Fatal("retired service blocked remaining application readiness", resumed, err)
	}
}

func TestReadinessScopedVersionsFailClosedAndBindRelevantDesiredHead(t *testing.T) {
	f := newReadinessFixture(t)
	ctx := t.Context()
	op, err := f.c.QueueServiceOperation(ctx, f.service.ID, "RESTART", 0, "restart-version")
	if err != nil {
		t.Fatal(err)
	}
	sql := f.c.store.(*storage.SQLStore)
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	before, err := f.c.DeploymentReadiness(ctx, op.ID)
	if err != nil || before.stage("workload").State != ReadinessConfirmed {
		t.Fatal(before, err)
	}
	base, err := f.c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.c.store.GetApplication(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := f.c.store
	for _, scenario := range []string{"unrelated active", "missing active", "foreign active", "missing desired", "foreign desired", "changed desired"} {
		t.Run(scenario, func(t *testing.T) {
			application := app
			active := base
			active.ID = "current-aggregate"
			active.ConfigurationSource.ServiceRevisions = map[string]string{"web": base.ID}
			application.ActiveRevisionID, application.DesiredRevisionID = active.ID, active.ID
			revisions := map[string]model.ApplicationRevision{active.ID: active}
			want := ReadinessConfirmed
			switch scenario {
			case "missing active":
				delete(revisions, active.ID)
				want = ReadinessUnknown
			case "foreign active":
				active.ApplicationID = "foreign"
				revisions[active.ID] = active
				want = ReadinessUnknown
			case "missing desired":
				application.DesiredRevisionID = "future-missing"
				want = ReadinessUnknown
			case "foreign desired":
				desired := active
				desired.ID, desired.ApplicationID = "future", "foreign"
				revisions[desired.ID] = desired
				application.DesiredRevisionID = desired.ID
				want = ReadinessUnknown
			case "changed desired":
				desired := active
				desired.ID = "future"
				desired.ConfigurationSource.ServiceRevisions = map[string]string{"web": desired.ID}
				revisions[desired.ID] = desired
				application.DesiredRevisionID = desired.ID
			}
			f.c.store = entrypointIdentityStore{Store: readinessOverrideStore{Store: original, application: &application}, revisions: revisions}
			v, err := f.c.DeploymentReadiness(ctx, op.ID)
			if err != nil || v.stage("workload").State != want {
				t.Fatal("unavailable service version confirmed", v, err)
			}
			if scenario == "changed desired" && v.BaselineDigest == before.BaselineDigest {
				t.Fatal("target's desired configuration did not change baseline")
			}
			if scenario == "unrelated active" && v.BaselineDigest != before.BaselineDigest {
				t.Fatal("unrelated aggregate changed baseline")
			}
		})
	}
}

type deletionEvidenceStore struct {
	storage.Store
	presence storage.ControlDocument
	err      error
}

func (s deletionEvidenceStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == s.presence.ID {
		return s.presence, s.err
	}
	return s.Store.GetControlDocument(ctx, key)
}

func TestReadinessDeletionRequiresPostCompletionAbsenceProof(t *testing.T) {
	f, services := serviceReadinessScopeFixture(t)
	ctx := t.Context()
	web := services["web"]
	op, err := f.core.QueueDeleteService(ctx, web.ID, "fixture", "delete-evidence", web.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateApplicationRevision(ctx, f.runtime.ID, op.ApplicationID, op.RevisionID, op.RevisionID, []string{services["api"].ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	finishScopedOperation(t, f, op)
	clock := f.core.now().Add(time.Second)
	f.core.now = func() time.Time { return clock }
	delete(services, "web")
	observeScopedServices(t, &f, services)
	op, err = f.core.store.GetDeploymentOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := f.core.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	web, err = f.core.store.GetService(ctx, web.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := f.core.store.GetControlDocument(ctx, storage.RuntimePresenceID(f.runtime.ID))
	if err != nil {
		t.Fatal(err)
	}
	var membership storage.RuntimePresence
	if err = json.Unmarshal(doc.Payload, &membership); err != nil {
		t.Fatal(err)
	}
	original := f.core.store
	for _, scenario := range []string{"missing object", "unfinished", "missing presence", "wrong runtime", "zero observation", "wrong observation", "wrong timestamp", "before completion", "same completion", "missing service", "different object", "not removed", "active service", "present target", "legacy heads", "legacy redeploy"} {
		t.Run(scenario, func(t *testing.T) {
			task, requested, selected, proof := op, revision, web, membership
			scope := *revision.ConfigurationSource.ServiceScope
			requested.ConfigurationSource.ServiceScope = &scope
			values := []model.Service{selected}
			proofDoc := doc
			var proofErr error
			want := ReadinessUnknown
			switch scenario {
			case "missing object":
				scope.TargetRuntimeObjectID = ""
			case "unfinished":
				task.FinishedAt = nil
			case "missing presence":
				proofErr = storage.ErrNotFound
			case "wrong runtime":
				proof.RuntimeID = "different-runtime"
			case "zero observation":
				proof.Observation = 0
			case "wrong observation":
				proof.Observation++
			case "wrong timestamp":
				proof.ObservedAt = proof.ObservedAt.Add(time.Second)
			case "before completion":
				finish := clock.Add(time.Second)
				task.FinishedAt = &finish
			case "same completion":
				task.FinishedAt = &clock
			case "missing service":
				values = []model.Service{}
			case "different object":
				selected.RuntimeObjectID = "replacement"
				values = []model.Service{selected}
				want = ReadinessPending
			case "not removed":
				selected.RemovedAt = nil
				values = []model.Service{selected}
				want = ReadinessPending
			case "active service":
				selected.Status = model.ServiceDiscovered
				values = []model.Service{selected}
				want = ReadinessPending
			case "present target":
				proof.ServiceIDs = append(slices.Clone(proof.ServiceIDs), web.ID)
				want = ReadinessPending
			case "legacy heads":
				requested.ConfigurationSource.ServiceRevisions = nil
				want = ReadinessConfirmed
			case "legacy redeploy":
				requested.ConfigurationSource.ServiceRevisions = nil
				task.Type, task.ServiceID = "REDEPLOY", ""
				api, err := original.GetService(ctx, services["api"].ID)
				if err != nil {
					t.Fatal(err)
				}
				values = []model.Service{web, api}
				want = ReadinessConfirmed
			}
			proofDoc.Payload, err = json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			f.core.store = readinessOverrideStore{Store: deletionEvidenceStore{Store: original, presence: proofDoc, err: proofErr}, operation: &task, revision: &requested, services: values}
			v, err := f.core.DeploymentReadiness(ctx, op.ID)
			if err != nil || v.stage("workload").State != want || len(v.AccessTargets) != 0 {
				t.Fatal("unproven deletion reported as complete", scenario, v, err)
			}
		})
	}
}

func TestReadinessInitialScopedInstallRemainsPendingUntilActivation(t *testing.T) {
	f := serviceReviewBoundarySetup(t)
	ctx := t.Context()
	app, err := f.core.store.GetApplication(ctx, f.op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID = ""
	original := f.core.store
	for _, status := range []model.DeploymentOperationStatus{model.OperationQueued, model.OperationSucceeded} {
		op := f.op
		op.Status = status
		f.core.store = readinessOverrideStore{Store: original, application: &app, operation: &op}
		v, err := f.core.DeploymentReadiness(ctx, op.ID)
		want := ReadinessPending
		if status == model.OperationSucceeded {
			want = ReadinessUnknown
		}
		if err != nil || v.stage("workload").State != want {
			t.Fatal("initial acceptance confused with active evidence", v, err)
		}
	}
}

func TestReadinessEntrypointBelongsToSelectedService(t *testing.T) {
	f := newReadinessFixture(t)
	ctx := t.Context()
	op, err := f.c.QueueServiceOperation(ctx, f.service.ID, "RESTART", 0, "selected-ingress")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.store.(*storage.SQLStore).DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	revision, err := f.c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.ConfigurationSource.Entrypoint = &model.ServiceEntrypoint{Service: "api", GatewayID: "irrelevant-gateway"}
	original := f.c.store
	for _, current := range []bool{false, true} {
		if current {
			revision.ConfigurationSource.EntrypointIntents = map[string]model.ServiceEntrypointIntent{"web": {ID: "web-intent", Entry: model.ServiceEntrypoint{Service: "web", GatewayID: f.gateway.ID}}}
		}
		f.c.store = readinessOverrideStore{Store: original, revision: &revision}
		v, err := f.c.DeploymentReadiness(ctx, op.ID)
		if err != nil || v.stage("gateway").State != ReadinessConfirmed || len(v.GatewayTargets) != 1 || v.GatewayTargets[0].GatewayID != f.gateway.ID {
			t.Fatal("inherited entrypoint selected a different service's gateway", v, err)
		}
	}
}
