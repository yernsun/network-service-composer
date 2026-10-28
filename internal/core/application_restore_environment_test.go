package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestHistoricalRestoreUsesHistoricalValuesAndCurrentConcurrencyTuple(t *testing.T) {
	for _, scenario := range []string{"restore", "changed after review", "missing reviewed source"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c, rt, _ := controlFixture(t)
			p, err := c.CreateProject(ctx, "History", "history")
			if err != nil {
				t.Fatal(err)
			}
			e, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "VALUE", Value: pointer("historical")}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			compose := "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("a", 64) + "\n    environment:\n      VALUE: ${VALUE}\n"
			op, err := c.DeployEnvironment(ctx, e.ID, compose, "owner", "initial", e.Revision)
			if err != nil {
				t.Fatal(err)
			}
			store := c.store.(*storage.SQLStore)
			if _, err = store.DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
				t.Fatal(err)
			}
			a, err := store.GetApplication(ctx, op.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			a.ActiveRevisionID = op.RevisionID
			a.Status = model.ApplicationRunning
			if err = store.UpdateApplication(ctx, &a, a.Revision); err != nil {
				t.Fatal(err)
			}
			old, err := store.GetApplicationRevision(ctx, op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			v, err = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "VALUE", Value: pointer("current")}}}, v.Revision)
			if err != nil {
				t.Fatal(err)
			}
			review, err := c.ReviewApplicationRestore(ctx, a.ID, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if review.Preview.Environment["VALUE"] != "historical" {
				t.Fatal("review substituted today's variable values")
			}
			if scenario == "missing reviewed source" {
				var saved savedApplicationRestore
				doc, err := c.readEncrypted(ctx, review.ID, "application-restore", &saved)
				if err != nil {
					t.Fatal(err)
				}
				saved.Source = nil
				write, err := c.encryptedWrite(doc.ID, doc.Kind, doc.ParentID, saved, doc.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.WriteControlDocuments(ctx, []storage.ControlWrite{write}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed after review" {
				_, err = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "VALUE", Value: pointer("newer")}}}, v.Revision)
				if err != nil {
					t.Fatal(err)
				}
			}
			restored, err := c.RestoreApplication(ctx, a.ID, review.ID, "owner", "restore", review.BaseRevision, true)
			if scenario != "restore" {
				if !errors.Is(err, storage.ErrPrecondition) {
					t.Fatal("stale reviewed variables accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal("current environment rejected historical restore", err)
			}
			rev, err := store.GetApplicationRevision(ctx, restored.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			if rev.ConfigurationSource.RestoredFrom != old.ID || rev.ConfigurationSource.PublicVariablesVersion != v.Revision || rev.ConfigurationSource.EnvironmentRevision <= old.ConfigurationSource.EnvironmentRevision {
				t.Fatal("restore lost history or current concurrency tuple", rev.ConfigurationSource)
			}
			raw, _, err := c.keyring.Decrypt(rev.EncryptedEnvironment, revisionAAD(rev.ID, "environment"))
			if err != nil || !strings.Contains(string(raw), "historical") {
				t.Fatal("historical environment not restored", err)
			}
			current, err := c.GetVariables(ctx, p.ID)
			if err != nil || current.Values[0].Value == nil || *current.Values[0].Value != "current" {
				t.Fatal("restore overwrote current variable settings", err)
			}
			again, err := store.GetApplicationRevision(ctx, old.ID)
			if err != nil || again.ConfigurationSource.EnvironmentRevision != old.ConfigurationSource.EnvironmentRevision {
				t.Fatal("immutable history rewritten", err)
			}
		})
	}
}
