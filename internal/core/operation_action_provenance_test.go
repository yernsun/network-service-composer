package core

import (
	"encoding/json"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestServiceEditsDoNotInheritRestoreActionFromActiveSource(t *testing.T) {
	for _, action := range []string{"SCALE", "DELETE_SERVICE"} {
		t.Run(action, func(t *testing.T) {
			f, services := serviceReadinessScopeFixture(t)
			ctx := t.Context()
			base, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			base.ConfigurationSource.RestoredFrom = "previous-definition"
			raw, err := json.Marshal(base.ConfigurationSource)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(raw), base.ID); err != nil {
				t.Fatal(err)
			}
			var op model.DeploymentOperation
			if action == "SCALE" {
				op, err = f.core.QueueServiceOperation(ctx, services["web"].ID, "SCALE", 2, "scale-after-restore", ServiceOperationOptions{Persist: true})
			} else {
				op, err = f.core.QueueDeleteService(ctx, services["web"].ID, "fixture", "delete-after-restore", services["web"].Revision, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			revision, err := f.core.store.GetApplicationRevision(ctx, op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			if revision.ConfigurationSource.RestoredFrom != "" || model.OperationBusinessAction(op, revision.ConfigurationSource) != action {
				t.Fatal("follow-up action mislabeled restore", revision.ConfigurationSource)
			}
			prior, err := f.core.store.GetApplicationRevision(ctx, base.ID)
			if err != nil || prior.ConfigurationSource.RestoredFrom != "previous-definition" {
				t.Fatal("follow-up edit changed restored history", err)
			}
		})
	}
}
