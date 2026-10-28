package core

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestApplicationPreparationRejectsIncompleteInstallationWithoutWrites(t *testing.T) {
	for _, mode := range []string{"default name", "empty identity", "invalid project", "unknown registry", "invalid compose", "unknown catalog", "encryption unavailable", "unknown binding service", "invalid binding domain"} {
		t.Run(mode, func(t *testing.T) {
			c, rt, _ := controlFixture(t)
			ctx := context.Background()
			in := InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n"}, Name: "fixture", ProjectName: "fixture", IdempotencyKey: "install"}
			switch mode {
			case "default name":
				in.Name = " "
			case "empty identity":
				in.Name = ""
				in.ProjectName = ""
			case "invalid project":
				in.ProjectName = "Invalid/Project"
			case "unknown registry":
				in.RegistryCredentialRef = "unconfigured"
			case "invalid compose":
				in.ComposeYAML = "services: []"
			case "unknown catalog":
				in.ComposeYAML = ""
				in.CatalogAppVersionID = "missing"
			case "encryption unavailable":
				c.keyring = &security.Keyring{}
			case "unknown binding service", "invalid binding domain":
				service := "web"
				host := "bad/name"
				if mode == "unknown binding service" {
					service = "missing"
					host = "app.example.test"
				}
				in.DomainBinding = &InstallDomainBindingInput{Service: service, CreateDomainBindingInput: CreateDomainBindingInput{Domain: &DomainConfigInput{Host: host, EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}, Route: RouteConfigInput{Path: "/", Backend: RouteBackendInput{Port: 8080}}}}
			}
			a, op, _, e := c.InstallApplication(ctx, in)
			apps, readErr := c.store.ListApplications(ctx, rt.ID, storage.Page{Limit: 20})
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "default name" {
				if e != nil || a.Name != "fixture" || op.ID == "" || len(apps) != 1 {
					t.Fatal("default identity not retained", a, e)
				}
			} else if e == nil || len(apps) != 0 {
				t.Fatal("incomplete preparation persisted application", a, e)
			}
		})
	}
}
func TestRevisionPreparationRejectsCorruptEnvironmentAndMissingEncryption(t *testing.T) {
	for _, mode := range []string{"environment envelope", "environment syntax", "compose envelope", "invalid compose", "unknown catalog", "encryption unavailable", "desired fallback", "no baseline"} {
		t.Run(mode, func(t *testing.T) {
			c, a, _ := restoreBoundaryFixture(t)
			ctx := context.Background()
			base := c.store
			in := CreateRevisionInput{IdempotencyKey: "revision"}
			old := a.DesiredRevisionID
			f := &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, old: old}
			f.mutate = func(r *model.ApplicationRevision) {
				switch mode {
				case "environment envelope":
					r.EncryptedEnvironment = []byte("invalid")
				case "environment syntax":
					var e error
					r.EncryptedEnvironment, e = c.keyring.Encrypt([]byte("=invalid"), revisionAAD(r.ID, "environment"))
					if e != nil {
						t.Fatal(e)
					}
				case "compose envelope":
					r.EncryptedCompose = []byte("invalid")
				}
			}
			if mode == "unknown catalog" {
				in.CatalogAppVersionID = "missing"
			}
			if mode == "invalid compose" {
				in.ComposeYAML = "services: []"
			}
			if mode == "encryption unavailable" {
				c.now = func() time.Time { c.keyring = &security.Keyring{}; return time.Now().UTC() }
			}
			if mode == "desired fallback" || mode == "no baseline" {
				a.DesiredRevisionID = ""
				if mode == "no baseline" {
					a.ActiveRevisionID = ""
					in.ComposeYAML = "services:\n  web:\n    image: example.test/web:fixed\n"
				}
				if e := base.UpdateApplication(ctx, &a, a.Revision); e != nil {
					t.Fatal(e)
				}
				var e error
				a, e = base.GetApplication(ctx, a.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			c.store = f
			revision, op, _, e := c.CreateApplicationRevision(ctx, a.ID, in, a.Revision)
			success := mode == "desired fallback" || mode == "no baseline"
			if success {
				if e != nil || revision.ID == "" || op.RevisionID != revision.ID || revision.ParentRevisionID != a.ActiveRevisionID {
					t.Fatal("revision baseline not preserved", revision, e)
				}
			} else {
				if e == nil {
					t.Fatal("corrupt revision accepted")
				}
				current, err := base.GetApplication(ctx, a.ID)
				if err != nil || current.DesiredRevisionID != a.DesiredRevisionID || current.Revision != a.Revision {
					t.Fatal("failed preparation changed application", err)
				}
			}
		})
	}
}
func TestProjectNameBoundsAndProjectMembershipDoNotMergeNeighbors(t *testing.T) {
	for _, v := range []string{"0", "9", "a", "z", "a_b-0", strings.Repeat("a", 128)} {
		if !validProjectName(v) {
			t.Fatal("valid project rejected", v)
		}
	}
	for _, v := range []string{"", strings.Repeat("a", 129), "A", "/", "a@", "a:", "a.", "aé"} {
		if validProjectName(v) {
			t.Fatal("invalid project accepted", v)
		}
	}
	if !serviceBelongsToProject(model.Service{ServiceKey: "project/web"}, model.RuntimeDocker, "project") {
		t.Fatal("stable project key ignored")
	}
	if serviceBelongsToProject(model.Service{ServiceKey: "projector/web"}, model.RuntimeDocker, "project") {
		t.Fatal("neighbor project claimed")
	}
}
