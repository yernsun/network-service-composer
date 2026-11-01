package maintenance

import (
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestRepairPlansKeepTheExplicitComponentAllowlistAndDataPin(t *testing.T) {
	for _, components := range [][]string{{"controller", "controller"}, {"shell"}, {"nginx-sandbox", "shell"}, {"postgresql", "shell"}, {"postgresql"}, {"nginx-sandbox"}} {
		t.Run(strings.Join(components, "-"), func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			before := driver.Record.Database
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: driver.Record.Revision, Components: components})
			valid := len(components) == 1 && (components[0] == "postgresql" || components[0] == "nginx-sandbox")
			if valid {
				if err != nil || len(plan.Scope.Components) != 1 || plan.Scope.Components[0] != components[0] || !plan.Scope.DatabaseSinglePointOfFailure {
					t.Fatal("the pinned repair scope was lost", err)
				}
				updates := 0
				for _, step := range plan.Steps {
					if strings.HasPrefix(step.Key, "update:") {
						updates++
						if step.Key != "update:"+components[0] || step.Before == nil || step.Before.ID != components[0] {
							t.Fatal("repair escaped its owned target")
						}
					}
				}
				if updates != 1 {
					t.Fatal("unexpected repair update count")
				}
			} else if err == nil {
				t.Fatal("a duplicate or arbitrary component was admitted")
			}
			if driver.Record.Database != before {
				t.Fatal("repair planning moved data")
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}

func TestMaintenanceRejectsGatewayOriginChangesDatabaseRollbackAndOldReceipts(t *testing.T) {
	for _, scenario := range []string{"gateway-origin", "database-rollback", "old-service-version"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			target := release("b")
			input := bootstrap.PlanRequest{Action: bootstrap.Upgrade, Revision: driver.Record.Revision, Release: &target}
			switch scenario {
			case "gateway-origin":
				target.Images.Gateway = strings.Replace(target.Images.Gateway, "registry.example.test", "unprepared.example.test", 1)
			case "database-rollback":
				input.Action = bootstrap.Rollback
				input.Release = nil
				target.Images.PostgreSQL = target.Images.Controller
				driver.Record.History = []bootstrap.ReleaseReceipt{{Release: target, ConfigurationDigest: driver.Record.ConfigDigest}}
			case "old-service-version":
				receipt := driver.Record.Resources["reserve:controller"]
				receipt.Version++
				driver.Record.Resources["reserve:controller"] = receipt
			}
			if _, err := driver.Plan(t.Context(), input); err == nil {
				t.Fatal("unprepared origin, data version or old receipt was admitted")
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
