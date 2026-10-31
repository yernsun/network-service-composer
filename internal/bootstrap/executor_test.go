package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	image := "example.invalid/nsc@sha256:" + strings.Repeat("a", 64)
	return Config{Mode: "single", Name: "nsc", MaintenanceNode: "one", DataNode: "one", NodeDir: "/srv/nsc", ConsolePort: 8443, KnownHostsFile: "/keys/known_hosts", Nodes: []Node{{Name: "one", Address: "192.0.2.1", SSHUser: "operator", SSHPort: 22, Manager: true}}, Images: Images{Controller: image, Gateway: image, PostgreSQL: image}}
}

type fixtureDriver struct {
	observation       string
	applied           int
	fail              error
	checked           error
	reconciled        *Resource
	reconciliationErr error
	observedErr       error
	beforeApply       func()
	result            *Resource
}

func (d *fixtureDriver) Observe(context.Context) (any, error) { return d.observation, d.observedErr }
func (d *fixtureDriver) Check(context.Context, Step) error    { return d.checked }
func (d *fixtureDriver) Apply(context.Context, Step) (Resource, error) {
	d.applied++
	if d.beforeApply != nil {
		d.beforeApply()
	}
	if d.result != nil {
		return *d.result, d.fail
	}
	return Resource{Kind: "service", ID: "actual-engine-service-id", Version: 1, SpecDigest: "exact-spec"}, d.fail
}

type rejectingCheckpoint struct {
	journal *Journal
	reject  func(*Record) bool
}

func (w rejectingCheckpoint) Save(r *Record) error {
	if w.reject(r) {
		return errors.New("checkpoint storage unavailable")
	}
	return w.journal.Save(r)
}

func TestDurabilityFailuresNeverAuthorizeAnotherWrite(t *testing.T) {
	for _, boundary := range []string{"pending", "intent", "receipt", "unknown", "blocked", "reconciliation"} {
		t.Run(boundary, func(t *testing.T) {
			j, r, p, d, e := setup(t)
			e.Now = nil
			if e.now().IsZero() {
				t.Fatal("missing clock")
			}
			e.Now = func() time.Time { return p.CreatedAt }
			if boundary == "unknown" {
				d.fail = context.DeadlineExceeded
			}
			if boundary == "blocked" {
				d.checked = errors.New("foreign resource")
			}
			if boundary == "reconciliation" {
				d.fail = context.DeadlineExceeded
				if _, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one"); err == nil {
					t.Fatal("fixture did not interrupt")
				}
				d.reconciled = &Resource{Kind: "service", ID: "reconciled-id", SpecDigest: "spec"}
			}
			e.Journal = rejectingCheckpoint{j, func(r *Record) bool {
				op := r.Operations[0]
				s := op.Plan.Steps[0]
				switch boundary {
				case "pending":
					return op.State == Pending
				case "intent":
					return s.State == Running
				case "receipt", "reconciliation":
					return s.State == Succeeded
				case "unknown":
					return s.State == Unknown
				case "blocked":
					return op.State == Blocked
				}
				return false
			}}
			var err error
			if boundary == "reconciliation" {
				_, err = e.Resume(context.Background(), &r, r.Operations[0].ID)
			} else {
				_, err = e.Execute(context.Background(), &r, p, p.Hash, "request-one")
			}
			if err == nil {
				t.Fatal("durability failure reported as success")
			}
			if (boundary == "pending" || boundary == "intent" || boundary == "blocked") && d.applied != 0 {
				t.Fatal("write without durable intent")
			}
			saved, readErr := j.Read()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(saved.Operations) > 0 && saved.Operations[0].State == Succeeded {
				t.Fatal("durable success invented")
			}
		})
	}
}

func TestInterruptionIncompleteReceiptsAndPendingExclusion(t *testing.T) {
	for _, receipt := range []Resource{{}, {Kind: "service", ID: "id"}, {Kind: "service", SpecDigest: "spec"}, {ID: "id", SpecDigest: "spec"}} {
		_, r, p, d, e := setup(t)
		d.result = &receipt
		op, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one")
		if err == nil || op.State != Unknown {
			t.Fatal("incomplete receipt accepted")
		}
	}
	j, r, p, d, e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	op, err := e.Execute(ctx, &r, p, p.Hash, "request-one")
	if !errors.Is(err, context.Canceled) || d.applied != 0 {
		t.Fatal("canceled operation wrote")
	}
	if _, err = e.Execute(context.Background(), &r, p, p.Hash, "request-two"); err == nil {
		t.Fatal("pending operation bypassed")
	}
	r.Operations[0].Plan.Steps[0].State = Running
	r.Operations[0].State = Running
	j.Save(&r)
	d.reconciliationErr = errors.New("cluster unavailable")
	if _, err = e.Resume(context.Background(), &r, op.ID); err == nil || d.applied != 0 {
		t.Fatal("interrupted intent retried")
	}
	d.reconciliationErr = nil
	d.reconciled = &Resource{Kind: "service", ID: "actual-id", SpecDigest: "spec"}
	if _, err = e.Resume(context.Background(), &r, op.ID); err != nil {
		t.Fatal(err)
	}
	newPlan, err := NewPlan(r, Verify, d.observation, p.Steps, e.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(context.Background(), &r, newPlan, newPlan.Hash, "request-two"); err != nil || d.applied != 1 {
		t.Fatal("subsequent revision blocked", err)
	}
}

func TestJournalRejectsDamagedOrUnwritableRecoveryStorage(t *testing.T) {
	j, _, _, _, _ := setup(t)
	if _, err := j.Initialize(testConfig(), ""); err == nil {
		t.Fatal("missing maintenance identity")
	}
	bad := testConfig()
	bad.Mode = "invalid"
	if _, err := j.Initialize(bad, "host"); err == nil {
		t.Fatal("invalid config initialized")
	}
	file := filepath.Join(j.dir, "ordinary-file")
	os.WriteFile(file, nil, 0o600)
	if _, err := OpenJournal(filepath.Join(file, "child")); err == nil {
		t.Fatal("journal directory failure ignored")
	}
	other := filepath.Join(t.TempDir(), "other")
	os.Mkdir(other, 0o700)
	os.Symlink(file, filepath.Join(other, "executor.lock"))
	if _, err := OpenJournal(other); err == nil {
		t.Fatal("lock symlink accepted")
	}
	if err := AtomicPrivateFile(filepath.Join(file, "child"), "checkpoint", nil); err == nil {
		t.Fatal("write failure ignored")
	}
	os.Mkdir(filepath.Join(j.dir, "directory-target"), 0o700)
	if err := AtomicPrivateFile(j.dir, "directory-target", nil); err == nil {
		t.Fatal("rename failure ignored")
	}
	AtomicPrivateFile(j.dir, "installation.json", []byte("corrupt"))
	if _, err := j.Initialize(testConfig(), "host"); err == nil {
		t.Fatal("corrupt journal replaced")
	}
}
func (d *fixtureDriver) Reconcile(context.Context, Step) (*Resource, error) {
	return d.reconciled, d.reconciliationErr
}

func setup(t *testing.T) (*Journal, Record, Plan, *fixtureDriver, Executor) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	r, err := j.Initialize(testConfig(), "machine-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	d := &fixtureDriver{observation: "fresh-engine-cluster-and-resource-versions"}
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p, err := NewPlan(r, Init, d.observation, []Step{{Key: "reserve", Description: "Reserve a stopped service"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return j, r, p, d, Executor{Journal: j, Driver: d, Now: func() time.Time { return now }}
}

func TestIntentReceiptAndDuplicate(t *testing.T) {
	j, r, p, d, e := setup(t)
	d.beforeApply = func() {
		saved, err := j.Read()
		if err != nil || saved.Operations[0].State != Running || saved.Operations[0].Plan.Steps[0].Receipt != nil {
			t.Fatalf("write without durable intent: %+v %v", saved, err)
		}
	}
	op, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one")
	if err != nil || op.State != Succeeded || r.Revision != 2 || r.Resources["reserve"].ID != "actual-engine-service-id" {
		t.Fatalf("incomplete operation: %+v %v", op, err)
	}
	duplicate, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one")
	if err != nil || duplicate.ID != op.ID || d.applied != 1 {
		t.Fatalf("duplicate write: %+v %v", duplicate, err)
	}
	changed := p
	changed.Hash = "other"
	if _, err = e.Execute(context.Background(), &r, changed, changed.Hash, "request-one"); err == nil {
		t.Fatal("idempotency key rebound")
	}
	if _, err = e.Resume(context.Background(), &r, op.ID); err != nil || d.applied != 1 {
		t.Fatal("completed operation reapplied")
	}
	if _, err = e.Resume(context.Background(), &r, "missing"); err == nil {
		t.Fatal("missing operation accepted")
	}
	if p.Steps[0].State != Pending || p.Steps[0].Receipt != nil {
		t.Fatal("execution mutated the approved plan")
	}
	next, err := NewPlan(r, Verify, d.observation, r.Operations[0].Plan.Steps, e.now())
	if err != nil || next.Steps[0].Receipt != nil || next.Steps[0].State != Pending || r.Operations[0].Plan.Steps[0].State != Succeeded {
		t.Fatal("planning changed historical receipts", err)
	}
	before := Resource{Kind: "service", ID: "before", SpecDigest: "spec"}
	receipt := Resource{Kind: "service", ID: "receipt", SpecDigest: "spec"}
	source := []Step{{Key: "review", Before: &before, Receipt: &receipt}}
	copy := cloneSteps(source)
	copy[0].Before.ID = "changed"
	copy[0].Receipt.ID = "changed"
	if source[0].Before.ID != "before" || source[0].Receipt.ID != "receipt" {
		t.Fatal("resource evidence was aliased")
	}
}

func TestUnknownNeverClaimsByName(t *testing.T) {
	j, r, p, d, e := setup(t)
	d.fail = context.DeadlineExceeded
	op, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one")
	if err == nil || op.State != Unknown {
		t.Fatalf("timeout reported as definite: %+v %v", op, err)
	}
	if _, err = e.Execute(context.Background(), &r, p, p.Hash, "request-two"); err == nil || d.applied != 1 {
		t.Fatal("second writer started over uncertain operation")
	}
	if op, err = e.Resume(context.Background(), &r, op.ID); err == nil || op.State != Unknown || d.applied != 1 {
		t.Fatal("unknown creation retried")
	}
	d.reconciled = &Resource{ID: "incomplete"}
	if op, err = e.Resume(context.Background(), &r, op.ID); err == nil || op.State != Unknown {
		t.Fatal("incomplete reconciliation established ownership")
	}
	saved, _ := j.Read()
	if saved.Operations[0].State != Unknown {
		t.Fatal("unknown result lost")
	}
	d.reconciled = &Resource{Kind: "service", ID: "receipt-from-engine", Version: 2, SpecDigest: "exact-spec"}
	op, err = e.Resume(context.Background(), &r, op.ID)
	if err != nil || op.State != Succeeded || d.applied != 1 || r.Resources["reserve"].ID != "receipt-from-engine" {
		t.Fatalf("verified receipt not resumed: %+v %v", op, err)
	}
}

func TestBlockedDriftAndDefiniteFailure(t *testing.T) {
	_, r, p, d, e := setup(t)
	d.checked = errors.New("foreign ClusterID")
	op, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one")
	if err == nil || op.State != Blocked || d.applied != 0 {
		t.Fatal("drift permitted a write")
	}
	d.checked = nil
	d.fail = &Rejected{Reason: "object version changed"}
	op, err = e.Resume(context.Background(), &r, op.ID)
	if err == nil || op.State != Failed {
		t.Fatal("definite rejection lost")
	}
	d.fail = nil
	op, err = e.Resume(context.Background(), &r, op.ID)
	if err != nil || op.State != Succeeded || d.applied != 2 {
		t.Fatalf("repair failed: %+v %v", op, err)
	}
}

func TestPlanApprovalFreshnessAndInput(t *testing.T) {
	_, r, p, d, e := setup(t)
	for _, mutate := range []func(*Plan, *Record){
		func(p *Plan, r *Record) { p.Hash = "tampered" }, func(p *Plan, r *Record) { p.Steps[0].Description = "unapproved" },
		func(p *Plan, r *Record) { r.Revision++ }, func(p *Plan, r *Record) { r.ID = "other" }, func(p *Plan, r *Record) { r.ConfigDigest = "changed" },
		func(p *Plan, r *Record) { p.ExpiresAt = e.now(); p.Hash = p.digest() }, func(p *Plan, r *Record) { p.CreatedAt = e.now().Add(time.Second); p.Hash = p.digest() },
	} {
		plan, record := p, r
		plan.Steps = append([]Step(nil), p.Steps...)
		mutate(&plan, &record)
		if err := plan.Validate(record, plan.Hash, d.observation, e.now()); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	if err := p.Validate(r, "", d.observation, e.now()); err == nil {
		t.Fatal("missing approval accepted")
	}
	d.observation = "changed engine"
	if _, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one"); err == nil || d.applied != 0 {
		t.Fatal("stale observation accepted")
	}
	if _, err := e.Execute(context.Background(), &r, p, p.Hash, "short"); err == nil {
		t.Fatal("invalid idempotency key")
	}
	d.observedErr = errors.New("no manager")
	if _, err := e.Execute(context.Background(), &r, p, p.Hash, "request-one"); err == nil {
		t.Fatal("unavailable observation accepted")
	}
	for _, tc := range []struct {
		action Action
		r      Record
		steps  []Step
	}{{"shell", r, p.Steps}, {Init, Record{}, p.Steps}, {Init, r, nil}, {Init, r, []Step{{Key: "x"}, {Key: "x"}}}, {Init, r, []Step{{}}}} {
		if _, err := NewPlan(tc.r, tc.action, nil, tc.steps, e.now()); err == nil {
			t.Fatal("invalid plan created")
		}
	}
	for _, action := range []Action{Init, Adopt, Repair, Upgrade, Rollback, Verify} {
		if _, err := NewPlan(r, action, nil, p.Steps, e.now()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivateJournalLockAndIdentity(t *testing.T) {
	j, r, _, _, _ := setup(t)
	if _, err := OpenJournal(j.dir); err == nil {
		t.Fatal("second executor acquired lock")
	}
	reloaded, err := j.Initialize(testConfig(), "machine-fingerprint")
	if err != nil || reloaded.ID != r.ID || reloaded.Nodes["one"] != r.Nodes["one"] {
		t.Fatal("identities regenerated")
	}
	if _, err = j.Initialize(testConfig(), "other-machine"); err == nil {
		t.Fatal("cross-node copy authorized takeover")
	}
	c := testConfig()
	c.Name = "changed"
	if _, err = j.Initialize(c, "machine-fingerprint"); err == nil {
		t.Fatal("configuration changed silently")
	}
	if info, err := os.Stat(filepath.Join(j.dir, "installation.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("journal is not private")
	}
	root := t.TempDir()
	unsafe := filepath.Join(root, "public")
	os.Mkdir(unsafe, 0o755)
	os.Chmod(unsafe, 0o755)
	if _, err := OpenJournal(unsafe); err == nil {
		t.Fatal("public journal accepted")
	}
	link := filepath.Join(root, "link")
	os.Symlink(j.dir, link)
	if _, err := OpenJournal(link); err == nil {
		t.Fatal("symlink journal accepted")
	}
	if _, err := OpenJournal("relative"); err == nil {
		t.Fatal("relative journal accepted")
	}
	if err := AtomicPrivateFile(j.dir, "../escape", nil); err == nil {
		t.Fatal("path escaped")
	}
	if err := AtomicPrivateFile(j.dir, "installation.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Read(); err == nil {
		t.Fatal("corrupt identity accepted")
	}
}
