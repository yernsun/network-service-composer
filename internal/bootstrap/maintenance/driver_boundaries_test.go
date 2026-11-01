package maintenance

import (
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func assertNoInfrastructureWrite(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if strings.HasPrefix(call, "POST /services/") {
			t.Fatal("unconfirmed evidence changed a service", call)
		}
	}
}
func TestRepairAndVerificationCannotSmuggleAnUpgradeOrNewBaseline(t *testing.T) {
	for _, action := range []bootstrap.Action{bootstrap.Verify, bootstrap.Repair, bootstrap.Rollback} {
		driver, engine, _ := maintenanceFixture(t)
		target := release("b")
		original := bootstrap.Digest(driver.Record.Release)
		if _, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: action, Revision: 2, Release: &target}); err == nil {
			t.Fatal("release accepted outside upgrade", action)
		}
		if original != bootstrap.Digest(driver.Record.Release) {
			t.Fatal("preflight changed the verified baseline")
		}
		assertNoInfrastructureWrite(t, engine.calls)
	}
	for _, action := range []bootstrap.Action{bootstrap.Verify, bootstrap.Upgrade, bootstrap.Rollback} {
		driver, _, _ := maintenanceFixture(t)
		if _, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: action, Revision: 2, Components: []string{"postgresql"}}); err == nil {
			t.Fatal("ignored action-specific component input", action)
		}
	}
}
func TestRollbackRequiresEveryCompatibilityAndOwnershipProof(t *testing.T) {
	for _, scenario := range []string{"database", "protocol", "component-id", "missing-digest", "changed-digest", "baseline"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			snapshot, err := driver.Runtime.Observe(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			previous := bootstrap.ReleaseReceipt{Release: *driver.Record.Release, ConfigurationDigest: driver.Record.ConfigDigest, Components: snapshot.Components, NonImageDigests: map[string]string{}, Revision: 1}
			for key, value := range snapshot.Services {
				previous.NonImageDigests[key] = nonImageDigest(value.Spec)
			}
			switch scenario {
			case "database":
				previous.Release.Images.PostgreSQL = release("b").Images.Controller
			case "protocol":
				previous.Release.Protocol++
			case "component-id":
				value := previous.Components["controller"]
				value.ID = "foreign"
				previous.Components["controller"] = value
			case "missing-digest":
				delete(previous.NonImageDigests, "nginx")
			case "changed-digest":
				previous.NonImageDigests["nginx"] = "external"
			case "baseline":
				driver.Record.Release = nil
			}
			driver.Record.History = []bootstrap.ReleaseReceipt{previous}
			action := bootstrap.Rollback
			if scenario == "baseline" {
				action = bootstrap.Upgrade
			}
			target := release("b")
			input := bootstrap.PlanRequest{Action: action, Revision: 2}
			if action == bootstrap.Upgrade {
				input.Release = &target
			}
			if _, err = driver.Plan(t.Context(), input); err == nil {
				t.Fatal("unproven rollback/upgrade admitted", scenario)
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
func TestMaintenanceReconciliationDoesNotRecognizeUnrelatedIntent(t *testing.T) {
	for _, scenario := range []string{"no-intent", "wrong-object", "wrong-original", "pending-step", "wrong-spec", "old-version", "different-step"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			before := component(driver.Record, "controller")
			actual := engine.services[before.ID]
			actual.Spec.Name = "external-drift"
			actual.Version.Index++
			engine.services[actual.ID] = actual
			step := bootstrap.Step{Key: "update:controller", Before: &before, State: bootstrap.Unknown}
			intended := Receipt(actual)
			switch scenario {
			case "no-intent":
				intended = bootstrap.Resource{}
			case "wrong-object":
				intended.ID = "foreign"
			case "wrong-original":
				copy := before
				copy.ID = "foreign"
				step.Before = &copy
			case "pending-step":
				step.State = bootstrap.Pending
			case "wrong-spec":
				intended.SpecDigest = "different"
			case "old-version":
				intended.Version = actual.Version.Index + 1
			case "different-step":
				step.Key = "verify:controller"
			}
			driver.Record.Resources["intent:"+step.Key] = intended
			driver.Record.Operations = []bootstrap.Operation{{ID: "unknown-original", State: bootstrap.Unknown, Plan: bootstrap.Plan{Steps: []bootstrap.Step{step}}}}
			if _, err := driver.Observe(t.Context()); err == nil {
				t.Fatal("foreign or incomplete intent authorized drift", scenario)
			}
			if scenario == "no-intent" || scenario == "wrong-spec" {
				if receipt, err := driver.Reconcile(t.Context(), bootstrap.Step{Key: "update:controller", Before: &before, State: bootstrap.Unknown}); err == nil || receipt != nil {
					t.Fatal("external change became a creation/update receipt")
				}
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
func TestInterruptedRetentionCanResumeWithoutDuplicatingTheRollbackBaseline(t *testing.T) {
	driver, _, journal := maintenanceFixture(t)
	for _, history := range [][]bootstrap.ReleaseReceipt{nil, {{Revision: 1}}} {
		driver.Record.History = history
		_, err := driver.Reconcile(t.Context(), bootstrap.Step{Key: "retain-release", State: bootstrap.Running})
		var safe *bootstrap.RetrySafe
		if !errors.As(err, &safe) {
			t.Fatal("interruption before durable retention cannot continue", err)
		}
	}
	driver.Record.History = nil
	receipt, err := driver.Apply(t.Context(), bootstrap.Step{Key: "retain-release"})
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := driver.Reconcile(t.Context(), bootstrap.Step{Key: "retain-release", State: bootstrap.Unknown})
	if err != nil || reconciled.ID != receipt.ID || len(driver.Record.History) != 1 || journal.saved != 1 {
		t.Fatal("retention reconciliation duplicated baseline", err)
	}
}
func TestMaintenanceWritesRequireCurrentEvidenceAndADurableIntent(t *testing.T) {
	for _, scenario := range []string{"unsupported-step", "unsupported-component", "missing-before", "changed-after-approval", "missing-service", "missing-target", "missing-baseline", "intent-write", "retain-write", "finalize-write", "observe-unavailable", "database-image"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, journal := maintenanceFixture(t)
			before := component(driver.Record, "controller")
			step := bootstrap.Step{Key: "update:controller", Before: &before}
			switch scenario {
			case "unsupported-step":
				step.Key = "shell"
			case "unsupported-component":
				step.Key = "update:external"
			case "missing-before":
				step.Before = nil
			case "changed-after-approval":
				copy := before
				copy.SpecDigest = "stale"
				step.Before = &copy
			case "missing-service":
				delete(engine.services, "controller")
			case "missing-target":
				step.Key = "prepare-images"
			case "missing-baseline":
				step.Key = "retain-release"
				driver.Record.Release = nil
			case "intent-write":
				journal.fail = true
			case "retain-write":
				step.Key = "retain-release"
				journal.fail = true
			case "finalize-write":
				step.Key = "finalize-maintenance"
				journal.fail = true
			case "observe-unavailable":
				engine.fail = "GET /info"
			case "database-image":
				target := release("b")
				driver.Target = &target
				before = component(driver.Record, "postgresql")
				step = bootstrap.Step{Key: "update:postgresql", Before: &before}
			}
			if _, err := driver.Apply(t.Context(), step); err == nil {
				t.Fatal("unsafe maintenance step passed", scenario)
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
func TestApprovalCannotRebaseChangedObjectOrAcceptOlderReceipts(t *testing.T) {
	for _, scenario := range []string{"id", "spec", "older-version"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2, Components: []string{"controller"}})
			if err != nil {
				t.Fatal(err)
			}
			step := plan.Steps[0]
			if scenario == "older-version" {
				service := engine.services["controller"]
				service.Version.Index--
				engine.services[service.ID] = service
			} else {
				before := *step.Before
				if scenario == "id" {
					before.ID = "other"
				} else {
					before.SpecDigest = "other"
				}
				step.Before = &before
			}
			if err = driver.Check(t.Context(), step); err == nil {
				t.Fatal("changed approval retained write rights")
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
	driver, engine, _ := maintenanceFixture(t)
	before := component(driver.Record, "postgresql")
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2, Components: []string{"postgresql"}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := (bootstrap.Executor{Journal: driver.Journal, Driver: driver}).Execute(t.Context(), driver.Record, plan, plan.Hash, "explicit-database-restart")
	if err != nil || op.State != bootstrap.Succeeded || engine.services[before.ID].Spec.TaskTemplate.ForceUpdate != 1 || engine.services[before.ID].Spec.TaskTemplate.ContainerSpec.Image != before.Image {
		t.Fatal("explicit database restart changed ownership or image", err)
	}
	if _, err = driver.Reconcile(t.Context(), bootstrap.Step{Key: "external", State: bootstrap.Unknown}); err == nil {
		t.Fatal("unknown external outcome invented a receipt")
	}
}
func TestRegistryAuthorityNormalizationDoesNotForwardCredentialsToAnotherRegistry(t *testing.T) {
	cases := map[string]string{"nsc/controller@sha256:a": "docker.io", "docker.io/nsc/controller@sha256:a": "docker.io", "index.docker.io/nsc/controller@sha256:a": "docker.io", "localhost/nsc@sha256:a": "localhost", "registry.example.test:5000/nsc@sha256:a": "registry.example.test:5000", "controller": "docker.io"}
	for image, want := range cases {
		if got := registry(image); got != want {
			t.Fatal(image, got, want)
		}
	}
}
