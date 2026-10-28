package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type materialBoundaryStore struct {
	storage.Store
	stage    string
	failure  error
	mutate   func(*model.ApplicationRevision)
	bindings []model.DomainBinding
}

func (s *materialBoundaryStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if s.stage == "revision" {
		return model.ApplicationRevision{}, s.failure
	}
	r, e := s.Store.GetApplicationRevision(ctx, key)
	if e == nil && s.mutate != nil {
		s.mutate(&r)
	}
	return r, e
}
func (s *materialBoundaryStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	if s.stage == "pool" {
		return model.GatewayPool{}, s.failure
	}
	return s.Store.GetPool(ctx, key)
}
func (s *materialBoundaryStore) ListAllServicesFiltered(ctx context.Context, f storage.ServiceFilter) ([]model.Service, error) {
	if s.stage == "services" {
		return nil, s.failure
	}
	return s.Store.ListAllServicesFiltered(ctx, f)
}
func (s *materialBoundaryStore) ListServiceDomainBindings(ctx context.Context, key string) ([]model.DomainBinding, error) {
	if s.stage == "bindings" {
		return nil, s.failure
	}
	if s.bindings != nil {
		return s.bindings, nil
	}
	return s.Store.ListServiceDomainBindings(ctx, key)
}

func TestExecutionMaterialFailuresPreventRuntimeCommands(t *testing.T) {
	for _, stage := range []string{"revision", "pool", "resolved ciphertext", "environment ciphertext", "environment syntax", "Compose syntax", "operation ciphertext", "operation JSON", "services", "bindings", "work directory", "registry missing", "registry unreadable", "registry invalid", "registry no auths"} {
		t.Run(stage, func(t *testing.T) {
			f := newWorkerFixture(t)
			a, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			failure := errors.New("material read unavailable")
			store := &materialBoundaryStore{Store: f.store, stage: stage, failure: failure}
			runner := &recordingRunner{}
			w := NewWorkerWithRunner(store, nil, f.keyring, f.config, nil, runner)
			switch stage {
			case "operation ciphertext":
				op.EncryptedInput = []byte("invalid")
			case "operation JSON":
				var e error
				op.EncryptedInput, e = f.keyring.Encrypt([]byte("{"), []byte("deployment-operation:"+op.ID))
				if e != nil {
					t.Fatal(e)
				}
			case "resolved ciphertext", "environment ciphertext", "environment syntax", "Compose syntax":
				store.mutate = func(r *model.ApplicationRevision) {
					switch stage {
					case "resolved ciphertext":
						r.EncryptedResolved = []byte("invalid")
					case "environment ciphertext":
						r.EncryptedEnvironment = []byte("invalid")
					default:
						raw, kind := "services: [broken", "resolved"
						if stage == "environment syntax" {
							raw, kind = "=invalid", "environment"
						}
						b, e := f.keyring.Encrypt([]byte(raw), []byte("application-revision:"+r.ID+":"+kind))
						if e != nil {
							t.Fatal(e)
						}
						if kind == "resolved" {
							r.EncryptedResolved = b
						} else {
							r.EncryptedEnvironment = b
						}
					}
				}
			case "work directory":
				p := filepath.Join(t.TempDir(), "file")
				if e := os.WriteFile(p, []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
				w.config.Management.WorkDir = p
			case "registry missing", "registry unreadable", "registry invalid", "registry no auths":
				a.RegistryCredentialRef = "credential"
				if e := f.store.UpdateApplication(context.Background(), &a, a.Revision); e != nil {
					t.Fatal(e)
				}
				if stage != "registry missing" {
					path := filepath.Join(t.TempDir(), "config.json")
					w.config.Registries["credential"] = config.RegistryCredential{ConfigFile: path}
					if stage != "registry unreadable" {
						raw := "{"
						if stage == "registry no auths" {
							raw = `{}`
						}
						if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
							t.Fatal(e)
						}
					}
				}
			}
			e := w.execute(context.Background(), f.runtime, &op)
			if e == nil || len(runner.calls) != 0 {
				t.Fatalf("unverified material executed: calls=%d error=%v", len(runner.calls), e)
			}
			if stage == "revision" || stage == "pool" || stage == "services" || stage == "bindings" {
				if !errors.Is(e, failure) {
					t.Fatal("material failure cause lost", e)
				}
			}
			current, e := f.store.GetApplication(context.Background(), a.ID)
			if e != nil || current.ActiveRevisionID != "" {
				t.Fatal("failed material became active", e)
			}
			if stage != "work directory" {
				entries, e := os.ReadDir(w.config.Management.WorkDir)
				if e != nil || len(entries) != 0 {
					t.Fatal("failed materialization left secrets on disk", e)
				}
			}
		})
	}
}

func TestRestrictedRevisionCannotExecuteHostMount(t *testing.T) {
	f := newWorkerFixture(t)
	a, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n    volumes: [/srv/fixture:/data:ro]\n")
	store := &materialBoundaryStore{Store: f.store, mutate: func(revision *model.ApplicationRevision) {
		revision.ConfigurationSource.RestrictedHostAccess = true
	}}
	runner := &recordingRunner{}
	w := NewWorkerWithRunner(store, nil, f.keyring, f.config, nil, runner)
	if err := w.execute(t.Context(), f.runtime, &op); err == nil || !strings.Contains(err.Error(), "project API keys") {
		t.Fatalf("restricted stored material was not rejected: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("restricted mount reached Docker execution")
	}
	current, err := f.store.GetApplication(t.Context(), a.ID)
	if err != nil || current.ActiveRevisionID != "" {
		t.Fatal("rejected revision became active", err)
	}
}

func TestMaterialRegistryCredentialsExistOnlyForCommandLifetime(t *testing.T) {
	f := newWorkerFixture(t)
	a, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
	a.RegistryCredentialRef = "registry"
	raw := `{"auths":{"example.test":{"username":"fixture","password":"private"}}}`
	path := filepath.Join(t.TempDir(), "credentials.json")
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	f.config.Registries["registry"] = config.RegistryCredential{ConfigFile: path}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	value, e := w.loadMaterial(context.Background(), f.runtime, a, op.RevisionID)
	if e != nil {
		t.Fatal(e)
	}
	dir, compose, _, env, cleanup, e := w.materialize(context.Background(), f.runtime, value)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	if len(env) != 1 || !strings.HasPrefix(env[0], "DOCKER_CONFIG="+dir) {
		t.Fatal("registry credentials escaped command directory")
	}
	secret := filepath.Join(strings.TrimPrefix(env[0], "DOCKER_CONFIG="), "config.json")
	contents, e := os.ReadFile(secret)
	if e != nil || string(contents) != raw {
		t.Fatal("command registry credentials lost", e)
	}
	for _, p := range []string{compose, secret} {
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("sensitive material permissions", e)
		}
	}
	cleanup()
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("command secrets retained after cleanup")
	}
}

func TestNetworkOverrideRejectsUnavailableBindingState(t *testing.T) {
	for _, stage := range []string{"services", "bindings", "pool"} {
		t.Run(stage, func(t *testing.T) {
			f := newWorkerFixture(t)
			a, _ := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
			failure := errors.New("binding state unavailable")
			s := &materialBoundaryStore{Store: f.store, stage: stage, failure: failure, bindings: []model.DomainBinding{{Route: model.Route{Status: model.RoutePending}}}}
			w := NewWorkerWithRunner(s, nil, f.keyring, f.config, nil, &recordingRunner{})
			raw, e := w.networkOverride(context.Background(), a, f.runtime, []applicationx.ServiceSpec{{Name: "web"}})
			if !errors.Is(e, failure) || raw != "" {
				t.Fatal("partial network intent accepted", e)
			}
		})
	}
}

func TestExecutionRunnerReturnsBoundedErrorsWithoutInheritedSecrets(t *testing.T) {
	if _, e := (ExecRunner{}).Run(context.Background(), t.TempDir(), nil, nil); e == nil {
		t.Fatal("empty command accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (ExecRunner{}).Run(ctx, t.TempDir(), nil, []string{"/bin/sh", "-c", "exit 0"}); e == nil {
		t.Fatal("cancelled command executed")
	}
	t.Setenv("NSC_TEST_PRIVATE_PARENT", "must-not-inherit")
	output, e := (ExecRunner{}).Run(context.Background(), t.TempDir(), []string{"VISIBLE=fixture"}, []string{"/bin/sh", "-c", `test -z "$NSC_TEST_PRIVATE_PARENT" && printf '%s' "$VISIBLE"`})
	if e != nil || output != "fixture" {
		t.Fatal("deployment inherited controller environment", e)
	}
	if safeError(nil) != "" || len(truncateLog(strings.Repeat("x", 70<<10))) >= 70<<10 || !strings.HasSuffix(truncateLog(strings.Repeat("x", 70<<10)), "[truncated]") {
		t.Fatal("deployment diagnostics not bounded")
	}
}
