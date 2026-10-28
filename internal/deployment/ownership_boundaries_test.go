package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

type boundaryLocker struct {
	err    error
	held   lease.Lease
	cancel context.CancelFunc
	calls  int
}

func (l *boundaryLocker) TryLock(context.Context, string) (lease.Lease, error) {
	l.calls++
	if l.cancel != nil {
		l.cancel()
	}
	return l.held, l.err
}

type boundaryLease struct {
	checkError error
	started    <-chan struct{}
	checks     atomic.Int32
	releases   atomic.Int32
}

func (l *boundaryLease) Check(context.Context) error {
	if l.started != nil {
		select {
		case <-l.started:
		default:
			return nil
		}
	}
	l.checks.Add(1)
	return l.checkError
}
func (l *boundaryLease) Release(context.Context) error { l.releases.Add(1); return nil }

type ownershipStore struct {
	storage.Store
	resource string
	started  <-chan struct{}
	failure  error
	cancel   context.CancelFunc
}

func (s *ownershipStore) GetRuntime(ctx context.Context, id string) (model.Runtime, error) {
	if s.resource == "runtime" {
		return model.Runtime{}, s.failure
	}
	return s.Store.GetRuntime(ctx, id)
}
func (s *ownershipStore) GetApplication(ctx context.Context, id string) (model.Application, error) {
	if s.resource == "application" {
		return model.Application{}, s.failure
	}
	return s.Store.GetApplication(ctx, id)
}
func (s *ownershipStore) HeartbeatDeploymentOperation(ctx context.Context, id, worker string, now time.Time) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.resource == "heartbeat" {
		if s.started == nil {
			return s.failure
		}
		select {
		case <-s.started:
			return s.failure
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.HeartbeatDeploymentOperation(ctx, id, worker, now)
}

type waitingRunner struct {
	calls   atomic.Int32
	started chan struct{}
}

func (r *waitingRunner) Run(ctx context.Context, _ string, _ []string, _ []string) (string, error) {
	if r.calls.Add(1) == 1 && r.started != nil {
		close(r.started)
	}
	<-ctx.Done()
	return "operation interrupted", ctx.Err()
}

func TestWorkerOwnershipChecksPreventUnauthorizedCommands(t *testing.T) {
	failure := errors.New("fixture boundary failure")
	for _, tc := range []struct {
		name, resource, code                    string
		disableGlobal, disableRuntime, external bool
		lockFailure                             bool
	}{
		{"runtime missing", "runtime", "runtime_not_found", false, false, false, false},
		{"application missing", "application", "application_not_found", false, false, false, false},
		{"global disabled", "", "management_disabled", true, false, false, false},
		{"runtime management disabled", "", "management_disabled", false, true, false, false},
		{"runtime disabled", "", "runtime_disabled", false, false, false, false},
		{"external workload", "", "management_disabled", false, false, true, false},
		{"lease failure", "", "lease_failed", false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			app, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
			ctx := context.Background()
			if tc.disableGlobal {
				fixture.config.Management.Enabled = false
			}
			if tc.disableRuntime {
				r := fixture.runtime
				r.ManagementEnabled = false
				if err := fixture.store.UpdateRuntime(ctx, &r, r.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "runtime disabled" {
				runtime := fixture.runtime
				runtime.Enabled = false
				if err := fixture.store.UpdateRuntime(ctx, &runtime, runtime.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if tc.external {
				app.Managed = false
				if err := fixture.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
					t.Fatal(err)
				}
			}
			store := &ownershipStore{Store: fixture.store, resource: tc.resource, failure: failure}
			locker := &boundaryLocker{err: failure}
			runner := &recordingRunner{}
			worker := NewWorkerWithRunner(store, locker, fixture.keyring, fixture.config, nil, runner)
			if err := worker.claimAndRun(ctx); err != nil {
				t.Fatal(err)
			}
			stored, err := fixture.store.GetDeploymentOperation(ctx, operation.ID)
			if err != nil || stored.Status != model.OperationFailed || stored.ErrorCode != tc.code || len(runner.calls) != 0 {
				t.Fatalf("unsafe execution: %#v calls=%d error=%v", stored, len(runner.calls), err)
			}
			if (locker.calls > 0) != tc.lockFailure {
				t.Fatal("lease acquired before ownership validation")
			}
		})
	}
}

func TestBusyDeploymentLeaseKeepsOperationPendingOnCancellationOrHeartbeatFailure(t *testing.T) {
	failure := errors.New("heartbeat rejected")
	for _, heartbeatFailure := range []bool{false, true} {
		fixture := newWorkerFixture(t)
		_, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
		ctx, cancel := context.WithCancel(context.Background())
		store := &ownershipStore{Store: fixture.store, failure: failure}
		locker := &boundaryLocker{err: lease.ErrBusy}
		runner := &recordingRunner{}
		if heartbeatFailure {
			store.resource = "heartbeat"
		} else {
			store.cancel = cancel
		}
		worker := NewWorkerWithRunner(store, locker, fixture.keyring, fixture.config, nil, runner)
		err := worker.claimAndRun(ctx)
		cancel()
		want := error(context.Canceled)
		if heartbeatFailure {
			want = failure
		}
		if !errors.Is(err, want) || len(runner.calls) != 0 {
			t.Fatalf("busy lease execution: %v calls=%d", err, len(runner.calls))
		}
		stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
		if err != nil || stored.Status == model.OperationSucceeded || stored.Status == model.OperationFailed {
			t.Fatalf("interrupted waiting task falsely completed: %#v %v", stored, err)
		}
	}
}

func TestLeaseOwnershipLossCancelsCommandAndReleasesLease(t *testing.T) {
	for _, heartbeatFailure := range []bool{false, true} {
		fixture := newWorkerFixture(t)
		_, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
		started := make(chan struct{})
		held := &boundaryLease{checkError: lease.ErrLost, started: started}
		store := &ownershipStore{Store: fixture.store, failure: lease.ErrLost, started: started}
		if heartbeatFailure {
			store.resource = "heartbeat"
		}
		runner := &waitingRunner{started: started}
		worker := NewWorkerWithRunner(store, &boundaryLocker{held: held}, fixture.keyring, fixture.config, nil, runner)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := worker.claimAndRun(ctx)
		cancel()
		if !errors.Is(err, lease.ErrLost) {
			t.Fatalf("lost execution ownership must remain uncertain: %v", err)
		}
		stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
		if err != nil || stored.Status != model.OperationRunning || stored.FinishedAt != nil || len(stored.EncryptedInput) == 0 || runner.calls.Load() != 1 || held.releases.Load() != 2 {
			t.Fatalf("lost lease result: %#v calls=%d releases=%d error=%v", stored, runner.calls.Load(), held.releases.Load(), err)
		}
		if (held.checks.Load() > 0) == heartbeatFailure {
			t.Fatal("lease check did not follow successful heartbeat")
		}
	}
}

type orderedBoundaryLocker struct {
	leases []lease.Lease
}

func (l *orderedBoundaryLocker) TryLock(context.Context, string) (lease.Lease, error) {
	value := l.leases[0]
	l.leases = l.leases[1:]
	return value, nil
}

func TestCompletionWithoutCurrentOwnershipRemainsRecoverable(t *testing.T) {
	for _, failure := range []string{"deployment lease", "storage lease", "controller canceled"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			fixture.config.Management.HeartbeatInterval = config.Duration(time.Hour)
			_, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
			started := make(chan struct{})
			storageLease := &boundaryLease{started: started}
			deploymentLease := &boundaryLease{started: started}
			if failure == "deployment lease" {
				deploymentLease.checkError = lease.ErrLost
			}
			if failure == "storage lease" {
				storageLease.checkError = lease.ErrLost
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := &recordingRunner{run: func(runnerCall) (string, error) {
				close(started)
				if failure == "controller canceled" {
					cancel()
				}
				return "accepted by Docker", nil
			}}
			worker := NewWorkerWithRunner(fixture.store, &orderedBoundaryLocker{leases: []lease.Lease{storageLease, deploymentLease}}, fixture.keyring, fixture.config, nil, runner)
			err := worker.claimAndRun(ctx)
			if !errors.Is(err, lease.ErrLost) && !errors.Is(err, context.Canceled) {
				t.Fatalf("uncertain completion reported successful: %v", err)
			}
			stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
			if err != nil || stored.Status != model.OperationRunning || stored.FinishedAt != nil || len(stored.EncryptedInput) == 0 || len(runner.calls) != 1 {
				t.Fatalf("lost retry evidence or issued cleanup without ownership: %+v calls=%d err=%v", stored, len(runner.calls), err)
			}
			if storageLease.releases.Load() != 1 || deploymentLease.releases.Load() != 1 {
				t.Fatal("lease cleanup was lost")
			}
		})
	}
}

type deadlineExecRunner struct {
	commandError error
	check        func(context.Context)
}

func (r *deadlineExecRunner) Run(ctx context.Context, directory string, environment, arguments []string) (string, error) {
	output, err := (ExecRunner{}).Run(ctx, directory, environment, arguments)
	r.commandError = err
	r.check(ctx)
	return output, err
}

func TestOwnExecutionDeadlinePreservesAcceptedInstallAndUpgrade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint("upgrade=", upgrade), func(t *testing.T) {
			fixture := newWorkerFixture(t)
			app, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
			ctx := context.Background()
			previous := ""
			if upgrade {
				initial := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, nil, &recordingRunner{})
				if err := initial.claimAndRun(ctx); err != nil {
					t.Fatal(err)
				}
				var err error
				app, err = fixture.store.GetApplication(ctx, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				previous = app.ActiveRevisionID
				raw := ""
				_, operation, _, err = fixture.core.CreateApplicationRevision(ctx, app.ID, core.CreateRevisionInput{
					ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web: {image: 'example.test/web:candidate'}", Environment: applicationx.EnvironmentInput{Raw: &raw}}, Actor: "test",
				}, app.Revision)
				if err != nil {
					t.Fatal(err)
				}
			}
			// A real CommandContext child records acceptance and waits for its
			// deadline. Further commands would expose cleanup/rollback as another
			// line; exec replaces the shell so cancellation leaves no sleeping child.
			binary := filepath.Join(t.TempDir(), "docker-fixture")
			const script = "#!/bin/sh\nif [ -e \"$0.calls\" ]; then printf 'unexpected compensation\\n' >> \"$0.calls\"; exit 0; fi\nprintf 'accepted\\n' > \"$0.calls\"\nprintf 'Docker accepted fixture command\\n'\nexec /bin/sleep 30\n"
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture.config.Management.DockerCLI = binary
			fixture.config.Management.DeployTimeout = config.Duration(2 * time.Second)
			locker := lease.New(fixture.store.DB(), fixture.store.Dialect())
			runner := &deadlineExecRunner{check: func(child context.Context) {
				if !errors.Is(child.Err(), context.DeadlineExceeded) || ctx.Err() != nil {
					t.Fatal("deadline was not confined to execution child")
				}
				for _, key := range []string{volumemanager.LockKey(fixture.runtime), "deployment:" + fixture.runtime.ID + ":" + app.ProjectName} {
					held, err := locker.TryLock(ctx, key)
					if held != nil {
						_ = held.Release(ctx)
					}
					if !errors.Is(err, lease.ErrBusy) {
						t.Fatalf("deadline lost otherwise-current lease %s: %v", key, err)
					}
				}
			}}
			worker := NewWorkerWithRunner(fixture.store, locker, fixture.keyring, fixture.config, nil, runner)
			if err := worker.claimAndRun(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("uncertain accepted deadline became terminal: %v", err)
			}
			if runner.commandError == nil || errors.Is(runner.commandError, context.DeadlineExceeded) {
				t.Fatalf("fixture did not reproduce CommandContext's non-context exit error: %v", runner.commandError)
			}
			calls, err := os.ReadFile(binary + ".calls")
			if err != nil || string(calls) != "accepted\n" {
				t.Fatalf("deadline caused destructive compensation: %q %v", calls, err)
			}
			stored, err := fixture.store.GetDeploymentOperation(ctx, operation.ID)
			if err != nil || stored.Status != model.OperationRunning || stored.FinishedAt != nil || stored.StartedAt == nil || stored.ErrorCode != "" || stored.Attempt != 1 || !bytes.Equal(stored.EncryptedInput, operation.EncryptedInput) || stored.RevisionID != operation.RevisionID {
				t.Fatalf("deadline lost durable immutable retry/history: %+v %v", stored, err)
			}
			current, err := fixture.store.GetApplication(ctx, app.ID)
			if err != nil || current.ActiveRevisionID != previous || current.Status == model.ApplicationFailed {
				t.Fatalf("deadline falsely activated or rolled back accepted state: %+v %v", current, err)
			}
			reclaimed, err := fixture.store.ClaimDeploymentOperation(ctx, "replacement-worker", time.Now().Add(10*time.Minute), time.Now().Add(5*time.Minute))
			if err != nil || reclaimed.ID != operation.ID || reclaimed.Attempt != 2 || reclaimed.RevisionID != stored.RevisionID || !bytes.Equal(reclaimed.EncryptedInput, stored.EncryptedInput) || !reclaimed.StartedAt.Equal(*stored.StartedAt) {
				t.Fatalf("deadline no longer permits immutable stale reconciliation: %+v %v", reclaimed, err)
			}
		})
	}
}

type compensationBoundaryRunner struct {
	t       *testing.T
	started chan struct{}
	calls   []runnerCall
}

type compensationHeartbeatStore struct {
	storage.Store
	started <-chan struct{}
}

func (s *compensationHeartbeatStore) HeartbeatDeploymentOperation(ctx context.Context, operation, worker string, now time.Time) error {
	select {
	case <-s.started:
		return errors.New("heartbeat persistence unavailable")
	default:
		return s.Store.HeartbeatDeploymentOperation(ctx, operation, worker, now)
	}
}

func (r *compensationBoundaryRunner) Run(ctx context.Context, directory string, environment, arguments []string) (string, error) {
	r.calls = append(r.calls, runnerCall{directory: directory, environment: environment, arguments: arguments})
	if len(r.calls) == 1 {
		return "candidate failed", errors.New("known candidate failure")
	}
	if len(r.calls) != 2 {
		r.t.Fatal("uncertain compensation issued another Docker command")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		r.t.Fatal("compensation escaped the deployment deadline")
	}
	// Docker has accepted the rollback or DOWN. The returned process error does
	// not establish whether that accepted mutation completed on the daemon.
	close(r.started)
	select {
	case <-ctx.Done():
		return "compensation accepted; result unavailable", errors.New("command result lost")
	case <-time.After(4 * time.Second):
		return "", errors.New("compensation owner was not canceled")
	}
}

func TestCompensationKeepsDeadlineAndOwnershipUntilItsOutcomeIsKnown(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		for _, boundary := range []string{"deadline", "deployment lease", "storage lease", "heartbeat"} {
			t.Run(fmt.Sprintf("upgrade=%v/%s", upgrade, boundary), func(t *testing.T) {
				fixture := newWorkerFixture(t)
				ctx := context.Background()
				app, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
				if upgrade {
					initial := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, nil, &recordingRunner{})
					if err := initial.claimAndRun(ctx); err != nil {
						t.Fatal(err)
					}
					services, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
					if err != nil || len(services) != 1 {
						t.Fatal("initial managed Service unavailable", err)
					}
					if _, binding, err := fixture.core.CreateDomainBinding(ctx, services[0].ID, core.CreateDomainBindingInput{
						Domain: &core.DomainConfigInput{Host: "compensation.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
						Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}},
					}); err != nil || binding == nil {
						t.Fatal("managed routing history unavailable", err)
					}
					if err := initial.claimAndRun(ctx); err != nil {
						t.Fatal(err)
					}
					app, err = fixture.store.GetApplication(ctx, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					raw := ""
					_, operation, _, err = fixture.core.CreateApplicationRevision(ctx, app.ID, core.CreateRevisionInput{
						ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web: {image: 'example.test/web:candidate'}", Environment: applicationx.EnvironmentInput{Raw: &raw}}, Actor: "test",
					}, app.Revision)
					if err != nil {
						t.Fatal(err)
					}
				}
				beforeApp, err := fixture.store.GetApplication(ctx, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				beforeServices, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
				if err != nil {
					t.Fatal(err)
				}
				beforeRoutes, err := fixture.store.ListRoutesFiltered(ctx, storage.ConsoleFilter{ServiceFilter: storage.ServiceFilter{ApplicationID: app.ID}}, storage.Page{Limit: 200})
				if err != nil || upgrade && len(beforeRoutes) != 1 {
					t.Fatal(err)
				}
				fixture.config.Management.DeployTimeout = config.Duration(3 * time.Second)
				if boundary == "deadline" {
					fixture.config.Management.DeployTimeout = config.Duration(2 * time.Second)
				}
				started := make(chan struct{})
				storageLease, deploymentLease := &boundaryLease{}, &boundaryLease{}
				if boundary == "deployment lease" {
					deploymentLease.started, deploymentLease.checkError = started, lease.ErrLost
				}
				if boundary == "storage lease" {
					storageLease.started, storageLease.checkError = started, lease.ErrLost
				}
				var store storage.Store = fixture.store
				if boundary == "heartbeat" {
					store = &compensationHeartbeatStore{Store: fixture.store, started: started}
				}
				runner := &compensationBoundaryRunner{t: t, started: started}
				worker := NewWorkerWithRunner(store, &orderedBoundaryLocker{leases: []lease.Lease{storageLease, deploymentLease}}, fixture.keyring, fixture.config, nil, runner)
				err = worker.claimAndRun(ctx)
				want := context.Canceled
				if boundary == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) || ctx.Err() != nil {
					t.Fatalf("uncertain accepted compensation became terminal: %v", err)
				}
				if len(runner.calls) != 2 || storageLease.releases.Load() != 1 || deploymentLease.releases.Load() != 1 {
					t.Fatal("compensation continued or lost lease cleanup")
				}
				command := strings.Join(runner.calls[1].arguments, " ")
				if upgrade && !strings.Contains(command, " up -d --wait") || !upgrade && !strings.Contains(command, " down --remove-orphans") {
					t.Fatalf("fixture did not reach the accepted compensation: %s", command)
				}
				stored, err := fixture.store.GetDeploymentOperation(ctx, operation.ID)
				if err != nil || stored.Status != model.OperationRunning || stored.FinishedAt != nil || stored.ErrorCode != "" || stored.Attempt != 1 || stored.RevisionID != operation.RevisionID || !bytes.Equal(stored.EncryptedInput, operation.EncryptedInput) {
					t.Fatalf("uncertain compensation lost immutable retry/history: %+v %v", stored, err)
				}
				currentApp, err := fixture.store.GetApplication(ctx, app.ID)
				if err != nil || !reflect.DeepEqual(currentApp, beforeApp) {
					t.Fatal("uncertain compensation changed the active revision or Application failure history", err)
				}
				currentServices, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
				if err != nil || !reflect.DeepEqual(currentServices, beforeServices) {
					t.Fatal("uncertain compensation changed managed Service history", err)
				}
				currentRoutes, err := fixture.store.ListRoutesFiltered(ctx, storage.ConsoleFilter{ServiceFilter: storage.ServiceFilter{ApplicationID: app.ID}}, storage.Page{Limit: 200})
				if err != nil || !reflect.DeepEqual(currentRoutes, beforeRoutes) {
					t.Fatal("uncertain compensation changed routing history", err)
				}
				reclaimed, err := fixture.store.ClaimDeploymentOperation(ctx, "replacement-worker", time.Now().Add(10*time.Minute), time.Now().Add(5*time.Minute))
				if err != nil || reclaimed.ID != operation.ID || reclaimed.Attempt != 2 || !bytes.Equal(reclaimed.EncryptedInput, operation.EncryptedInput) {
					t.Fatal("uncertain compensation prevented immutable stale reconciliation", err)
				}
			})
		}
	}
}

func TestKnownInstallFailureStillCompletesVerifiedCleanup(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), " up -d --wait") {
			return "candidate rejected", errors.New("known candidate failure")
		}
		return "cleanup completed", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, nil, runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil || stored.Status != model.OperationFailed || stored.FinishedAt == nil || stored.ErrorCode != "deployment_failed" || !strings.Contains(stored.RedactedLog, "cleanup completed") || len(runner.calls) != 2 {
		t.Fatal("known failure lost its completed cleanup result", stored, err)
	}
	current, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil || current.Status != model.ApplicationFailed || current.ActiveRevisionID != "" {
		t.Fatal("known failed installation was falsely activated", current, err)
	}
}

func TestResourceIdentityUnavailablePreservesEarlierUnknownRetry(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			fixture := newWorkerFixture(t)
			var infoRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					w.Header().Set("API-Version", "1.52")
					_, _ = io.WriteString(w, "OK")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					infoRequests.Add(1)
					if deadline {
						<-r.Context().Done()
					} else {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"message":"fixture engine unavailable"}`)
					}
					return
				}
				t.Errorf("identity timeout issued unexpected Docker action: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			// SQL fixture only: production SQLite rejects Swarm. This transport boundary
			// needs a persisted Swarm identity without connecting to a real engine.
			if _, err := fixture.store.DB().Exec(`UPDATE runtimes SET runtime_type='swarm',cluster_id='fixture-cluster',endpoint=? WHERE id=?`, server.URL, fixture.runtime.ID); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			operation := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: fixture.runtime.ID, Type: "DOCKER_RESOURCE_CREATE", Status: model.OperationQueued,
				EncryptionKeyID: "v1", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			raw, err := json.Marshal(dockerResourceInput{Kind: "config", Name: "immutable-fixture", Content: []byte("fixture configuration")})
			if err != nil {
				t.Fatal(err)
			}
			operation.EncryptedInput, err = fixture.keyring.Encrypt(raw, []byte("deployment-operation:"+operation.ID))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = fixture.store.CreateDeploymentOperation(context.Background(), &operation); err != nil {
				t.Fatal(err)
			}
			old, err := fixture.store.ClaimDeploymentOperation(context.Background(), "earlier-unknown-owner", now.Add(-10*time.Minute), now.Add(-11*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			fixture.config.Management.DeployTimeout = config.Duration(250 * time.Millisecond)
			worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, nil, &recordingRunner{})
			err = worker.claimAndRun(context.Background())
			if err == nil || deadline != errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unavailable identity observation became terminal: %v", err)
			}
			stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
			if err != nil || infoRequests.Load() != 1 || stored.Status != model.OperationRunning || stored.FinishedAt != nil || stored.ErrorCode != "" || stored.Attempt != 2 || !bytes.Equal(stored.EncryptedInput, operation.EncryptedInput) || !stored.StartedAt.Equal(*old.StartedAt) {
				t.Fatalf("identity deadline destroyed earlier retry evidence: %+v info=%d %v", stored, infoRequests.Load(), err)
			}
		})
	}
}

func TestResourceCompletedBeforeDeadlinePersistsOwnershipAndFinishes(t *testing.T) {
	fixture := newWorkerFixture(t)
	operationID := id.New(id.Operation)
	var created atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.52")
			_, _ = io.WriteString(w, "OK")
		case strings.HasSuffix(r.URL.Path, "/info"):
			_, _ = io.WriteString(w, `{"ID":"fixture-engine","Swarm":{"ControlAvailable":true,"Cluster":{"ID":"fixture-cluster"}}}`)
		case strings.HasSuffix(r.URL.Path, "/services"), strings.HasSuffix(r.URL.Path, "/configs"):
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/configs/create"):
			var value struct {
				Name   string
				Labels map[string]string
				Data   []byte
			}
			if err := json.NewDecoder(r.Body).Decode(&value); err != nil || value.Name != "immutable-fixture" || string(value.Data) != "fixture configuration" || value.Labels["com.nsc.docker-resource.owner"] != "nsc" || value.Labels["com.nsc.docker-resource.cluster"] != "fixture-cluster" || value.Labels["com.nsc.docker-resource.operation"] != operationID {
				t.Errorf("creation omitted immutable input or ownership labels: %+v %v", value, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			created.Add(1)
			_, _ = io.WriteString(w, `{"ID":"owned-config"}`)
		default:
			t.Errorf("unexpected resource action: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	// Transport-only fixture, as in the deadline case above; no host Swarm.
	if _, err := fixture.store.DB().Exec(`UPDATE runtimes SET runtime_type='swarm',cluster_id='fixture-cluster',endpoint=? WHERE id=?`, server.URL, fixture.runtime.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := model.DeploymentOperation{ID: operationID, RuntimeID: fixture.runtime.ID, Type: "DOCKER_RESOURCE_CREATE", Status: model.OperationQueued,
		EncryptionKeyID: "v1", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	raw, err := json.Marshal(dockerResourceInput{Kind: "config", Name: "immutable-fixture", Content: []byte("fixture configuration")})
	if err != nil {
		t.Fatal(err)
	}
	operation.EncryptedInput, err = fixture.keyring.Encrypt(raw, []byte("deployment-operation:"+operation.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.store.CreateDeploymentOperation(context.Background(), &operation); err != nil {
		t.Fatal(err)
	}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, nil, &recordingRunner{})
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil || stored.Status != model.OperationSucceeded || stored.FinishedAt == nil || len(stored.EncryptedInput) != 0 || created.Load() != 1 {
		t.Fatalf("verified resource result failed to complete: %+v created=%d %v", stored, created.Load(), err)
	}
	record, err := fixture.store.GetDockerResource(context.Background(), "fixture-cluster", "config", "owned-config")
	if err != nil || record.CreateOperationID != operationID || record.Name != "immutable-fixture" {
		t.Fatalf("verified resource lost durable ownership: %+v %v", record, err)
	}
}

func TestSupersededExecutorCannotActivateOrCleanUpAcceptedWorkload(t *testing.T) {
	for _, commandFailed := range []bool{false, true} {
		t.Run(fmt.Sprint("command_failed=", commandFailed), func(t *testing.T) {
			fixture := newWorkerFixture(t)
			fixture.config.Management.HeartbeatInterval = config.Duration(time.Hour)
			application, operation := installFixtureApplication(t, fixture, "services:\n  web: {image: 'example.test/web:fixed'}")
			runner := &recordingRunner{run: func(runnerCall) (string, error) {
				old, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				// The same instance identity is deliberately reused. Only the claim
				// attempt distinguishes this accepted-but-delayed command from its owner.
				reclaimed, err := fixture.store.ClaimDeploymentOperation(context.Background(), fixture.config.Controller.InstanceID, time.Now().Add(10*time.Minute), time.Now().Add(5*time.Minute))
				if err != nil || reclaimed.ID != operation.ID || reclaimed.Attempt != 2 {
					t.Fatalf("replacement claim: %+v %v", reclaimed, err)
				}
				old.Status = model.OperationFailed
				if err := fixture.store.FinishDeploymentOperation(context.Background(), &old, time.Now()); err == nil {
					t.Fatal("old attempt completed a replacement claim with the same worker identity")
				}
				stale := storage.WithDeploymentOperation(context.Background(), old)
				if err := fixture.store.HeartbeatDeploymentOperation(stale, old.ID, "another-controller", time.Now()); !errors.Is(err, storage.ErrOperationOwnership) {
					t.Fatalf("claim context renewed another worker identity: %v", err)
				}
				if err := fixture.store.HeartbeatDeploymentOperation(stale, "another-operation", old.WorkerID, time.Now()); !errors.Is(err, storage.ErrOperationOwnership) {
					t.Fatalf("claim context renewed another operation: %v", err)
				}
				if err := fixture.store.HeartbeatDeploymentOperation(stale, old.ID, old.WorkerID, time.Now()); err == nil {
					t.Fatal("old attempt heartbeated the replacement claim")
				}
				if err := fixture.store.SetApplicationStatus(stale, application.ID, model.ApplicationStopped, "", time.Now()); !errors.Is(err, storage.ErrOperationOwnership) {
					t.Fatalf("stale lifecycle write was not fenced: %v", err)
				}
				if err := fixture.store.SetApplicationReplicaOverride(stale, application.ID, "web", 0, time.Now()); !errors.Is(err, storage.ErrOperationOwnership) {
					t.Fatalf("stale replica write was not fenced: %v", err)
				}
				if commandFailed {
					return "", errors.New("accepted command result was lost")
				}
				return "Docker accepted the old command", nil
			}}
			worker := NewWorkerWithRunner(fixture.store, &boundaryLocker{held: &boundaryLease{}}, fixture.keyring, fixture.config, nil, runner)
			if err := worker.claimAndRun(context.Background()); !errors.Is(err, storage.ErrOperationOwnership) {
				t.Fatalf("superseded executor must remain uncertain: %v", err)
			}
			stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
			if err != nil || stored.Status != model.OperationRunning || stored.Attempt != 2 || stored.FinishedAt != nil || len(stored.EncryptedInput) == 0 {
				t.Fatalf("replacement claim overwritten: %+v %v", stored, err)
			}
			current, err := fixture.store.GetApplication(context.Background(), application.ID)
			if err != nil || current.ActiveRevisionID != "" || len(runner.calls) != 1 {
				t.Fatalf("stale activation or cleanup: %+v calls=%d err=%v", current, len(runner.calls), err)
			}
		})
	}
}

func TestDisabledWorkerWaitsForCancellationWithoutClaiming(t *testing.T) {
	cfg := config.Defaults()
	cfg.Management.Enabled = false
	cfg.Controller.InstanceID = " "
	worker := NewWorker(nil, nil, nil, cfg, nil, nil)
	if worker.workerID == "" {
		t.Fatal("missing generated worker identity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("disabled worker: %v", err)
	}
	fixture := newWorkerFixture(t)
	worker = NewWorkerWithRunner(fixture.store, nil, fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("idle worker: %v", err)
	}
}
