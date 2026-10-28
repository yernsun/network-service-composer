package core

import (
	"context"
	"errors"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type applicationBoundaryStore struct {
	*restoreBoundaryStore
	services           []model.Service
	conflictAfterWrite bool
}

func (s *applicationBoundaryStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if err := s.fail("runtime", key); err != nil {
		return model.Runtime{}, err
	}
	return s.Store.GetRuntime(ctx, key)
}
func (s *applicationBoundaryStore) GetCatalogAppVersion(ctx context.Context, key string) (model.CatalogAppVersion, error) {
	if err := s.fail("catalog", key); err != nil {
		return model.CatalogAppVersion{}, err
	}
	return s.Store.GetCatalogAppVersion(ctx, key)
}
func (s *applicationBoundaryStore) ListAllServicesFiltered(ctx context.Context, f storage.ServiceFilter) ([]model.Service, error) {
	if err := s.fail("services", ""); err != nil {
		return nil, err
	}
	if s.services != nil {
		return s.services, nil
	}
	return s.Store.ListAllServicesFiltered(ctx, f)
}
func (s *applicationBoundaryStore) ListApplicationRevisions(ctx context.Context, key string, p storage.Page) ([]model.ApplicationRevision, error) {
	if err := s.fail("revisions", key); err != nil {
		return nil, err
	}
	return s.Store.ListApplicationRevisions(ctx, key, p)
}
func (s *applicationBoundaryStore) GetDeploymentOperationByIdempotency(ctx context.Context, runtime, key string) (model.DeploymentOperation, error) {
	if err := s.fail("idempotency", key); err != nil {
		return model.DeploymentOperation{}, err
	}
	return s.Store.GetDeploymentOperationByIdempotency(ctx, runtime, key)
}
func (s *applicationBoundaryStore) CreateApplicationInstall(ctx context.Context, a *model.Application, r *model.ApplicationRevision, services []model.Service, d *model.Domain, cert *model.Certificate, route *model.Route, op *model.DeploymentOperation) error {
	if err := s.fail("install", a.ID); err != nil {
		return err
	}
	err := s.Store.CreateApplicationInstall(ctx, a, r, services, d, cert, route, op)
	if err == nil && s.conflictAfterWrite {
		return storage.ErrConflict
	}
	return err
}
func (s *applicationBoundaryStore) CreateApplicationRevision(ctx context.Context, r *model.ApplicationRevision, op *model.DeploymentOperation, expected int64, now time.Time) error {
	if err := s.fail("create revision", r.ID); err != nil {
		return err
	}
	err := s.Store.CreateApplicationRevision(ctx, r, op, expected, now)
	if err == nil && s.conflictAfterWrite {
		return storage.ErrConflict
	}
	return err
}

func TestApplicationInstallAndRevisionStorageFailures(t *testing.T) {
	for _, action := range []string{"install", "revision", "preflight"} {
		methods := []string{"runtime", "pool", "idempotency", "services", "install"}
		if action == "revision" {
			methods = []string{"application", "runtime", "pool", "idempotency", "revision", "revisions", "create revision"}
		}
		if action == "preflight" {
			methods = []string{"runtime", "pool"}
		}
		for _, method := range methods {
			t.Run(action+"/"+method, func(t *testing.T) {
				c, rt, _ := controlFixture(t)
				var a model.Application
				if action == "revision" {
					c, a, _ = restoreBoundaryFixture(t)
					rt.ID = a.RuntimeID
				}
				base := c.store
				failure := errors.New("application dependency unavailable")
				c.store = &applicationBoundaryStore{restoreBoundaryStore: &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, method: method, failure: failure}}}
				in := ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"}
				var err error
				switch action {
				case "install":
					_, _, _, err = c.InstallApplication(context.Background(), InstallApplicationInput{ApplicationPreflightInput: in, Name: "application", ProjectName: "application", IdempotencyKey: "install"})
				case "revision":
					_, _, _, err = c.CreateApplicationRevision(context.Background(), a.ID, CreateRevisionInput{ApplicationPreflightInput: in, IdempotencyKey: "update"}, a.Revision)
				case "preflight":
					_, err = c.PreflightApplication(context.Background(), in)
				}
				if !errors.Is(err, failure) {
					t.Fatalf("original failure lost: %v", err)
				}
				if action == "revision" {
					actual, e := base.GetApplication(context.Background(), a.ID)
					if e != nil || actual.Revision != a.Revision || actual.DesiredRevisionID != a.DesiredRevisionID {
						t.Fatal("failed revision changed application target", e)
					}
				} else {
					apps, e := base.ListApplications(context.Background(), rt.ID, storage.Page{})
					if e != nil || len(apps) != 0 {
						t.Fatal("failed preparation persisted application", e)
					}
				}
			})
		}
	}
}

func TestApplicationRevisionPreviewRejectsCorruptBaseContent(t *testing.T) {
	for _, name := range []string{"compose envelope", "compose digest", "environment envelope", "environment syntax", "resolved envelope"} {
		t.Run(name, func(t *testing.T) {
			c, a, _ := restoreBoundaryFixture(t)
			base := c.store
			fault := &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, old: a.DesiredRevisionID}
			fault.mutate = func(r *model.ApplicationRevision) {
				switch name {
				case "compose envelope":
					r.EncryptedCompose = []byte("corrupt")
				case "compose digest":
					r.ComposeSHA256 = "mismatch"
				case "environment envelope":
					r.EncryptedEnvironment = []byte("corrupt")
				case "environment syntax":
					var err error
					r.EncryptedEnvironment, err = c.keyring.Encrypt([]byte("invalid assignment"), revisionAAD(r.ID, "environment"))
					if err != nil {
						t.Fatal(err)
					}
				case "resolved envelope":
					r.EncryptedResolved = []byte("corrupt")
				}
			}
			c.store = fault
			if _, err := c.PreflightApplicationRevision(context.Background(), a.ID, ApplicationPreflightInput{}); err == nil {
				t.Fatal("corrupt historical content accepted as preview baseline")
			}
			actual, err := base.GetApplication(context.Background(), a.ID)
			if err != nil || actual.Revision != a.Revision {
				t.Fatal("preview mutated application", err)
			}
		})
	}
}

func TestApplicationInstallAndUpgradeRecoverConcurrentIdempotentCommit(t *testing.T) {
	for _, action := range []string{"install", "upgrade"} {
		t.Run(action, func(t *testing.T) {
			c, rt, _ := controlFixture(t)
			var a model.Application
			if action == "upgrade" {
				c, a, _ = restoreBoundaryFixture(t)
				rt.ID = a.RuntimeID
			}
			c.store = &applicationBoundaryStore{restoreBoundaryStore: &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: c.store}}, conflictAfterWrite: true}
			in := ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"}
			var first, second model.DeploymentOperation
			var err error
			if action == "install" {
				input := InstallApplicationInput{ApplicationPreflightInput: in, Name: "application", ProjectName: "application", IdempotencyKey: "same-key"}
				a, first, _, err = c.InstallApplication(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				_, second, _, err = c.InstallApplication(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				input.ProjectName = "different"
				if _, _, _, err = c.InstallApplication(context.Background(), input); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("idempotency key accepted different installation", err)
				}
			} else {
				input := CreateRevisionInput{ApplicationPreflightInput: in, IdempotencyKey: "same-key"}
				_, first, _, err = c.CreateApplicationRevision(context.Background(), a.ID, input, a.Revision)
				if err != nil {
					t.Fatal(err)
				}
				_, second, _, err = c.CreateApplicationRevision(context.Background(), a.ID, input, a.Revision)
				if err != nil {
					t.Fatal(err)
				}
				input.ComposeYAML = "services:\n  web:\n    image: nginx:stable\n"
				if _, _, _, err = c.CreateApplicationRevision(context.Background(), a.ID, input, a.Revision); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("idempotency key accepted different upgrade", err)
				}
			}
			if first.ID == "" || first.ID != second.ID || first.ApplicationID != a.ID || first.Status != model.OperationQueued {
				t.Fatalf("concurrent request created unrelated task: %+v %+v", first, second)
			}
		})
	}
}

func TestApplicationAdoptionRequiresExactUnmanagedProject(t *testing.T) {
	for _, mode := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		for _, name := range []string{"exact", "missing", "extra", "managed", "removed", "ordinary install", "unrelated"} {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				c, rt, _ := controlFixture(t)
				rt.Type = mode
				key := "project/web"
				if mode == model.RuntimeSwarm {
					key = "project_web"
				}
				services := []model.Service{{ID: "observed", ServiceKey: key, Namespace: "project", Status: model.ServiceAvailable}}
				adopt := true
				valid := name == "exact" || name == "unrelated"
				switch name {
				case "missing":
					services = []model.Service{}
				case "extra":
					services = append(services, model.Service{ID: "extra", ServiceKey: key + "-extra", Namespace: "project"})
				case "managed":
					services[0].Managed = true
				case "removed":
					services[0].Status = model.ServiceRemoved
				case "ordinary install":
					adopt = false
				case "unrelated":
					services = append(services, model.Service{ID: "foreign", ServiceKey: "other/web", Namespace: "other"})
				}
				c.store = &applicationBoundaryStore{restoreBoundaryStore: &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: c.store}}, services: services}
				specs := []applicationx.ServiceSpec{{Name: "web", TargetPort: 8080}}
				if err := c.validateApplicationProjectClaim(context.Background(), rt, "project", specs, adopt); (err == nil) != valid {
					t.Fatalf("adoption scope %s accepted=%t: %v", name, valid, err)
				}
				if valid {
					created, selected, err := c.applicationInstallServices(context.Background(), rt, model.Application{ID: "application", ProjectName: "project"}, specs, true, c.now())
					if err != nil || len(created) != 0 || selected["web"].ID != "observed" || selected[key].ID != "observed" {
						t.Fatal("adoption rebuilt or misidentified workload", err)
					}
				}
			})
		}
	}
}
