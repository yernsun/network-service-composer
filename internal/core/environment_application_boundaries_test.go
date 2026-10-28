package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
)

func TestEnvironmentApplicationClaimRequiresCompatibleOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "explicit registry", "external", "foreign runtime", "missing application", "copy with application", "write failure"} {
		t.Run(mode, func(t *testing.T) {
			c, a, _ := restoreBoundaryFixture(t)
			ctx := context.Background()
			base := c.store
			p, e := c.CreateProject(ctx, "Project", "project")
			if e != nil {
				t.Fatal(e)
			}
			rt, e := base.GetRuntime(ctx, a.RuntimeID)
			if e != nil {
				t.Fatal(e)
			}
			a.RegistryCredentialRef = "application registry"
			f := &lifecycleAdmissionStore{Store: base, application: a, runtime: rt}
			c.store = f
			in := EnvironmentInput{Name: "test", RuntimeID: a.RuntimeID, ApplicationID: a.ID}
			switch mode {
			case "explicit registry":
				in.RegistryCredentialRef = "environment registry"
			case "external":
				f.application.Managed = false
			case "foreign runtime":
				f.application.RuntimeID = "foreign"
			case "missing application":
				f.failure = "application"
			case "copy with application":
				source, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "source", RuntimeID: rt.ID})
				if err != nil {
					t.Fatal(err)
				}
				in.CopyEnvironmentID = source.ID
			case "write failure":
				f.Store = &nginxBoundaryStore{Store: base, method: "write", failure: errors.New("claim unavailable")}
			}
			v, e := c.CreateEnvironment(ctx, p.ID, in)
			success := mode == "valid" || mode == "explicit registry"
			if success {
				if e != nil || v.ApplicationID != a.ID || v.ProjectName != a.ProjectName || v.RegistryCredentialRef != a.RegistryCredentialRef {
					t.Fatal("application identity or credential responsibility changed", v, e)
				}
				if _, e = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "second", RuntimeID: a.RuntimeID, ApplicationID: a.ID}); !errors.Is(e, storage.ErrConflict) {
					t.Fatal("application acquired duplicate environment ownership", e)
				}
			} else {
				if e == nil {
					t.Fatal("invalid application claim accepted")
				}
				docs, err := base.ListControlDocuments(ctx, "application-environment", a.ID, storage.Page{Limit: 10})
				if err != nil || len(docs) != 0 {
					t.Fatal("failed claim persisted", err)
				}
			}
		})
	}
}
func TestEnvironmentComposeRequiresReadableCurrentApplicationRevision(t *testing.T) {
	for _, mode := range []string{"active", "desired fallback", "application missing", "revision missing", "ciphertext corrupt"} {
		t.Run(mode, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			ctx := context.Background()
			base := c.store
			a, e := base.GetApplication(ctx, v.ApplicationID)
			if e != nil {
				t.Fatal(e)
			}
			f := &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, old: a.ActiveRevisionID}
			switch mode {
			case "application missing":
				f.method = "application"
				f.failure = storage.ErrNotFound
			case "revision missing":
				f.method = "revision"
				f.failure = storage.ErrNotFound
			case "ciphertext corrupt":
				f.mutate = func(r *model.ApplicationRevision) { r.EncryptedCompose = []byte("corrupt") }
			case "desired fallback":
				a.ActiveRevisionID = ""
				if e = base.UpdateApplication(ctx, &a, a.Revision); e != nil {
					t.Fatal(e)
				}
			}
			c.store = f
			_, input, e := c.EnvironmentPreflightInput(ctx, v.ID, "")
			if mode == "active" || mode == "desired fallback" {
				if e != nil || !strings.Contains(input.ComposeYAML, "registry.example.test/web@sha256:") || !input.ReplaceEnvironment {
					t.Fatal("current immutable application definition not inherited", input, e)
				}
			} else if e == nil {
				t.Fatal("unknown definition accepted")
			}
			current, e := base.GetApplication(ctx, v.ApplicationID)
			if e != nil || current.DesiredRevisionID != a.DesiredRevisionID {
				t.Fatal("read failure changed desired application revision", e)
			}
		})
	}
}
func TestEffectiveVariablesUseConfirmedRevisionAndKeepSecretsRedacted(t *testing.T) {
	for _, mode := range []string{"current", "changed public", "changed local", "application missing", "revision missing", "no active revision"} {
		t.Run(mode, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			ctx := context.Background()
			base := c.store
			if mode != "current" {
				owner := v.ProjectID
				if mode == "changed local" {
					owner = v.ID
				}
				raw := "TOKEN=private-value\nVISIBLE=value\n"
				if _, e := c.SaveVariables(ctx, owner, VariableInput{Raw: &raw}, 0); e != nil {
					t.Fatal(e)
				}
				if _, e := c.SaveVariables(ctx, owner, VariableInput{Values: []Variable{{Key: "TOKEN", Secret: true}, {Key: "VISIBLE"}}}, 1); e != nil {
					t.Fatal(e)
				}
			}
			a, e := base.GetApplication(ctx, v.ApplicationID)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "no active revision" {
				a.ActiveRevisionID = ""
				if e = base.UpdateApplication(ctx, &a, a.Revision); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "application missing" || mode == "revision missing" {
				method := strings.Fields(mode)[0]
				c.store = &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, method: method, failure: storage.ErrNotFound}}
			}
			result, e := c.EffectiveVariables(ctx, v.ID)
			if e != nil || result["pending"] != (mode != "current") {
				t.Fatal("variable drift misrepresented", result, e)
			}
			if mode != "current" {
				values := result["values"].([]Variable)
				if len(values) != 2 {
					t.Fatal("raw environment was not parsed", values)
				}
				if values[0].Key != "TOKEN" || !values[0].Secret || values[0].Value != nil {
					t.Fatal("effective variables leaked secret", values)
				}
			}
		})
	}
}
