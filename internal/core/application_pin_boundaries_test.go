package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestApplicationImagePinScopeRejectsUnknownServicesBeforePersistence(t *testing.T) {
	for _, action := range []string{"install", "preflight revision", "create revision"} {
		t.Run(action, func(t *testing.T) {
			c, rt, _ := controlFixture(t)
			var app model.Application
			if action != "install" {
				c, app, _ = restoreBoundaryFixture(t)
				rt.ID = app.RuntimeID
			}
			ctx := context.Background()
			before, e := c.store.ListDeploymentOperations(ctx, "", storage.Page{Limit: 200})
			if e != nil {
				t.Fatal(e)
			}
			in := ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n", ConfigurationSource: model.DeploymentSource{Images: map[string]string{"unknown": "nginx@sha256:" + strings.Repeat("a", 64)}}}
			switch action {
			case "install":
				_, _, _, e = c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, Name: "pin", ProjectName: "pin", IdempotencyKey: "unknown-lock"})
			case "preflight revision":
				_, e = c.PreflightApplicationRevision(ctx, app.ID, in)
			case "create revision":
				_, _, _, e = c.CreateApplicationRevision(ctx, app.ID, CreateRevisionInput{ApplicationPreflightInput: in, IdempotencyKey: "unknown-lock"}, app.Revision)
			}
			if e == nil || !strings.Contains(e.Error(), "unknown service") {
				t.Fatal("unknown service image lock accepted", e)
			}
			after, e := c.store.ListDeploymentOperations(ctx, "", storage.Page{Limit: 200})
			if e != nil || len(after) != len(before) {
				t.Fatal("invalid image lock queued deployment", e)
			}
			if app.ID != "" {
				actual, e := c.store.GetApplication(ctx, app.ID)
				if e != nil || actual.DesiredRevisionID != app.DesiredRevisionID || actual.Revision != app.Revision {
					t.Fatal("invalid lock changed application", e)
				}
			}
		})
	}
}

func TestApplicationRestoreRetainsVariableIdentityAndEncryptedReview(t *testing.T) {
	for _, stage := range []string{"variable history", "historical image lock", "redacted image unusable", "review encryption unavailable"} {
		t.Run(stage, func(t *testing.T) {
			c, app, old := restoreBoundaryFixture(t)
			ctx := context.Background()
			base := c.store
			ring := c.keyring
			cipher := func(kind, raw string) []byte {
				b, e := ring.Encrypt([]byte(raw), revisionAAD(old, kind))
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			wrapped := &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, old: old}
			wrapped.mutate = func(r *model.ApplicationRevision) {
				r.EncryptedEnvironment = cipher("environment", "ZETA=last\nALPHA=first\n")
				switch stage {
				case "historical image lock":
					r.EncryptedResolved = cipher("resolved", "services:\n  web:\n    image: example.test/app:old\n")
					r.ConfigurationSource.Images = map[string]string{"web": "example.test/app@sha256:" + strings.Repeat("a", 64)}
				case "redacted image unusable":
					r.EncryptedCompose = cipher("compose", "services:\n  web:\n    image: ${IMAGE}\n")
					r.EncryptedEnvironment = cipher("environment", "IMAGE=example.test/app@sha256:"+strings.Repeat("a", 64)+"\n")
					r.SecretKeys = []string{"IMAGE"}
				}
			}
			c.store = wrapped
			if stage == "review encryption unavailable" {
				c.now = func() time.Time { c.keyring = &security.Keyring{}; return time.Now() }
			}
			review, e := c.ReviewApplicationRestore(ctx, app.ID, old)
			if stage == "redacted image unusable" || stage == "review encryption unavailable" {
				if e == nil {
					t.Fatal("unusable restore material accepted")
				}
				docs, e := base.ListControlDocuments(ctx, "application-restore", app.ID, storage.Page{})
				if e != nil || len(docs) != 0 {
					t.Fatal("failed review created recovery authorization", e)
				}
				return
			}
			if e != nil || review.DataRestored || review.SourceRevisionID != old {
				t.Fatal("restore review lost boundary", e)
			}
			var saved savedApplicationRestore
			if _, e = c.readEncrypted(ctx, review.ID, "application-restore", &saved); e != nil {
				t.Fatal(e)
			}
			vars := saved.Input.Environment.Values
			if vars == nil || len(*vars) != 2 || (*vars)[0].Key != "ALPHA" || (*vars)[1].Key != "ZETA" {
				t.Fatal("historical environment identity lost", vars)
			}
			actual, e := base.GetApplication(ctx, app.ID)
			if e != nil || actual.Revision != app.Revision || actual.DesiredRevisionID != app.DesiredRevisionID {
				t.Fatal("review changed running application", e)
			}
		})
	}
}
