package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/install"
	"github.com/yernsun/network-service-composer/internal/pki"
)

type cliDriver struct {
	writes int
	failed bool
}

func (d *cliDriver) Observe(context.Context) (any, error)        { return "verified-fixture", nil }
func (d *cliDriver) Check(context.Context, bootstrap.Step) error { return nil }
func (d *cliDriver) Apply(context.Context, bootstrap.Step) (bootstrap.Resource, error) {
	d.writes++
	r := bootstrap.Resource{Kind: "fixture", ID: "known-id", SpecDigest: "spec"}
	if d.failed {
		return r, context.DeadlineExceeded
	}
	return r, nil
}
func (d *cliDriver) Reconcile(context.Context, bootstrap.Step) (*bootstrap.Resource, error) {
	if d.failed {
		return nil, errors.New("unknown")
	}
	return &bootstrap.Resource{Kind: "fixture", ID: "known-id", SpecDigest: "spec"}, nil
}

type cliSession struct {
	record   *bootstrap.Record
	executor bootstrap.Executor
}

func (s cliSession) Plan(_ context.Context, action bootstrap.Action) (bootstrap.Plan, error) {
	return bootstrap.NewPlan(*s.record, action, "verified-fixture", []bootstrap.Step{{Key: "probe", Description: "isolated command contract fixture"}}, time.Now())
}
func (s cliSession) Execute(ctx context.Context, p bootstrap.Plan, approval, key string) (bootstrap.Operation, error) {
	return s.executor.Execute(ctx, s.record, p, approval, key)
}
func (s cliSession) Resume(ctx context.Context, id string) (bootstrap.Operation, error) {
	return s.executor.Resume(ctx, s.record, id)
}
func (s cliSession) Verify(_ context.Context, w io.Writer) error {
	_, err := w.Write([]byte(`{"state":"VERIFIED"}`))
	return err
}

func cliConfig(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	c := bootstrap.Config{Name: "nsc", Mode: "single", DataNode: "one", MaintenanceNode: "one", NodeDir: "/var/lib/nsc", ConsolePort: 8443, KnownHostsFile: "/keys/known_hosts", Nodes: []bootstrap.Node{{Name: "one", Address: "192.0.2.1", SSHUser: "root", SSHPort: 22, Manager: true}}, Images: bootstrap.Images{Controller: image, Gateway: image, PostgreSQL: image}}
	data, _ := json.Marshal(c)
	path := filepath.Join(dir, "install.json")
	os.WriteFile(path, data, 0o600)
	return path, filepath.Join(dir, "state")
}

func cliFactory(d *cliDriver) bootstrapSessionFactory {
	return func(_ context.Context, _ bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, _ string, _ bootstrapSessionOptions) (bootstrapSession, error) {
		return cliSession{record: r, executor: bootstrap.Executor{Journal: j, Driver: d}}, nil
	}
}

func TestBootstrapCLIPlanApprovalIdempotencyStatusAndResume(t *testing.T) {
	path, dir := cliConfig(t)
	d := &cliDriver{}
	factory := cliFactory(d)
	args := []string{"init", "--config", path, "--state-dir", dir}
	var output bytes.Buffer
	if err := bootstrapCommandWithSession(args, strings.NewReader(""), &output, factory); err != nil {
		t.Fatal(err)
	}
	var plan bootstrap.Plan
	data, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(data, &plan)
	if plan.Hash == "" || d.writes != 0 || !strings.Contains(output.String(), "Plan saved") {
		t.Fatal("plan performed an external write")
	}
	apply := append(append([]string{}, args...), "--plan", filepath.Join(dir, "plan.json"), "--approve", plan.Hash, "--idempotency-key", "command-fixture-request")
	if err = bootstrapCommandWithSession(apply, strings.NewReader(""), io.Discard, factory); err != nil || d.writes != 1 {
		t.Fatal("approved command failed", err)
	}
	if err = bootstrapCommandWithSession(apply, strings.NewReader(""), io.Discard, factory); err != nil || d.writes != 1 {
		t.Fatal("duplicate command repeated writes", err)
	}
	var status bytes.Buffer
	if err = bootstrapCommand([]string{"status", "--state-dir", dir}, nil, &status); err != nil || !strings.Contains(status.String(), "SUCCEEDED") {
		t.Fatal("status unavailable", err)
	}
	if err = bootstrapCommandWithSession([]string{"verify", "--config", path, "--state-dir", dir}, nil, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
	if err = bootstrapCommandWithSession([]string{"resume", "--config", path, "--state-dir", dir}, nil, io.Discard, factory); err == nil {
		t.Fatal("completed operation resumed as new work")
	}
	// A separate installation interrupts after intent, then resumes its recorded
	// operation without requiring a newly generated or stale approval.
	path2, dir2 := cliConfig(t)
	d.failed = true
	newArgs := []string{"init", "--config", path2, "--state-dir", dir2}
	if err = bootstrapCommandWithSession(newArgs, nil, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir2, "plan.json"))
	json.Unmarshal(data, &plan)
	newArgs = append(newArgs, "--plan", filepath.Join(dir2, "plan.json"), "--approve", plan.Hash, "--idempotency-key", "interrupted-command")
	if err = bootstrapCommandWithSession(newArgs, nil, io.Discard, factory); err == nil {
		t.Fatal("uncertain command reported success")
	}
	d.failed = false
	if err = bootstrapCommandWithSession([]string{"resume", "--config", path2, "--state-dir", dir2}, nil, io.Discard, factory); err != nil {
		t.Fatal("recorded continuation failed", err)
	}
}

func TestBootstrapCLIRejectsInvalidOrUnapprovedCommands(t *testing.T) {
	path, dir := cliConfig(t)
	d := &cliDriver{}
	factory := cliFactory(d)
	for _, args := range [][]string{nil, {"shell"}, {"init", "--unknown"}, {"init", "extra"}, {"init", "--timeout", "0s"}, {"init", "--state-dir", dir}, {"status", "--state-dir", filepath.Join(t.TempDir(), "missing")}, {"init", "--state-dir", "relative"}} {
		if err := bootstrapCommandWithSession(args, nil, io.Discard, factory); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	base := []string{"init", "--config", path, "--state-dir", dir}
	if err := bootstrapCommandWithSession(append(append([]string{}, base...), "--approve", "unbound"), nil, io.Discard, factory); err == nil {
		t.Fatal("approval without saved plan accepted")
	}
	if err := bootstrapCommandWithSession(base, nil, writerFailure{}, factory); err == nil {
		t.Fatal("plan output failure discarded")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"unknown":true}`), 0o600)
	if err := bootstrapCommandWithSession(append(append([]string{}, base...), "--plan", bad), nil, io.Discard, factory); err == nil {
		t.Fatal("unknown plan fields accepted")
	}
	os.WriteFile(bad, []byte(`{"action":"rollback"}`), 0o600)
	if err := bootstrapCommandWithSession(append(append([]string{}, base...), "--plan", bad), nil, io.Discard, factory); err == nil {
		t.Fatal("different action approved")
	}
	if d.writes != 0 {
		t.Fatal("invalid command wrote external resources")
	}
	if err := bootstrapCommandWithSession(base, nil, io.Discard, func(context.Context, bootstrap.Config, *bootstrap.Record, *bootstrap.Journal, string, bootstrapSessionOptions) (bootstrapSession, error) {
		return nil, errors.New("unavailable verified node")
	}); err == nil {
		t.Fatal("connection failure hidden")
	}
	os.Chmod(path, 0o644)
	if err := bootstrapCommandWithSession(base, nil, io.Discard, factory); err == nil {
		t.Fatal("public installation config accepted")
	}
}

type writerFailure struct{}

func (writerFailure) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestBootstrapGuideDoesNotConsumeConfirmationInput(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		dir := t.TempDir()
		count := 1
		if mode == "multi" {
			count = 3
		}
		values := []string{mode}
		for n := 0; n < count; n++ {
			values = append(values, "192.0.2."+string(rune('1'+n)), "root", "")
		}
		image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
		values = append(values, "/keys/known_hosts", image, image, image, "confirmation-retained")
		reader := bufio.NewReader(strings.NewReader(strings.Join(values, "\n") + "\n"))
		var output bytes.Buffer
		path, err := bootstrapGuide(reader, &output, dir)
		if err != nil {
			t.Fatal(err)
		}
		data, err := bootstrap.ReadPrivate(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := bootstrap.DecodeConfig(data)
		if err != nil || len(cfg.Nodes) != count {
			t.Fatal("guided config failed", err)
		}
		if rest, _ := reader.ReadString('\n'); strings.TrimSpace(rest) != "confirmation-retained" {
			t.Fatal("guide consumed the confirmation input")
		}
	}
	if _, err := bootstrapGuide(strings.NewReader(""), io.Discard, t.TempDir()); err == nil {
		t.Fatal("incomplete guide accepted")
	}
}

func TestGuidedCLIStopsOnIncompleteInputWithoutCreatingAPlan(t *testing.T) {
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	fields := []string{"single", "192.0.2.1", "root", "", "/keys/known_hosts", image, image, image}
	for n := 0; n < len(fields); n++ {
		dir := t.TempDir()
		input := strings.Join(fields[:n], "\n")
		if n > 0 {
			input += "\n"
		}
		if _, err := bootstrapGuide(strings.NewReader(input), io.Discard, dir); err == nil {
			t.Fatal("incomplete input created configuration", n)
		}
		if _, err := os.Stat(filepath.Join(dir, "install.yaml")); !os.IsNotExist(err) {
			t.Fatal("incomplete configuration persisted")
		}
	}
	path, dir := cliConfig(t)
	factory := cliFactory(&cliDriver{})
	if err := bootstrapCommandWithSession([]string{"init", "--interactive", "--state-dir", dir}, strings.NewReader(""), io.Discard, factory); err == nil {
		t.Fatal("empty interactive guide accepted")
	}
	os.WriteFile(path, []byte("mode: invalid"), 0600)
	if err := bootstrapCommandWithSession([]string{"init", "--config", path, "--state-dir", dir}, nil, io.Discard, factory); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := bootstrapCommandWithSession([]string{"init", "--config", missing, "--state-dir", dir}, nil, io.Discard, factory); err == nil {
		t.Fatal("missing configuration accepted")
	}
}

func TestInteractivePlanConfirmationAndExplicitResumeRemainBound(t *testing.T) {
	path, dir := cliConfig(t)
	d := &cliDriver{}
	factory := cliFactory(d)
	base := []string{"init", "--config", path, "--state-dir", dir}
	if err := bootstrapCommandWithSession(base, nil, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
	var p bootstrap.Plan
	json.Unmarshal(data, &p)
	args := append(append([]string{}, base...), "--plan", filepath.Join(dir, "plan.json"), "--interactive")
	if err := bootstrapCommandWithSession(args, strings.NewReader("\n"), io.Discard, factory); err != nil || d.writes != 0 {
		t.Fatal("empty confirmation executed", err)
	}
	if err := bootstrapCommandWithSession(args, strings.NewReader(p.Hash+"\n"), io.Discard, factory); err != nil || d.writes != 1 {
		t.Fatal("exact confirmation failed", err)
	}
	r, err := bootstrap.ReadJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = bootstrapCommandWithSession([]string{"resume", "--config", path, "--state-dir", dir, "--operation", r.Operations[0].ID}, nil, io.Discard, factory); err != nil {
		t.Fatal("explicit completed request not idempotent", err)
	}
	if err = bootstrapCommandWithSession([]string{"init", "--config", path, "--state-dir", dir, "--plan", filepath.Join(dir, "missing.json")}, nil, io.Discard, factory); err == nil {
		t.Fatal("missing saved plan ignored")
	}
}

type outputAfterPlan struct{ writes int }

func (w *outputAfterPlan) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(data), nil
}

type inputFailure struct{}

func (inputFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCLIOutputFailureRetainsCompletedOperationAndInteractiveRejectionDoesNotExecute(t *testing.T) {
	path, dir := cliConfig(t)
	driver := &cliDriver{}
	factory := cliFactory(driver)
	base := []string{"init", "--config", path, "--state-dir", dir}
	if err := bootstrapCommandWithSession(base, nil, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
	var plan bootstrap.Plan
	json.Unmarshal(data, &plan)
	saved := append(append([]string{}, base...), "--plan", filepath.Join(dir, "plan.json"))
	if err := bootstrapCommandWithSession(append(append([]string{}, saved...), "--interactive"), inputFailure{}, io.Discard, factory); err == nil {
		t.Fatal("failed approval input accepted")
	}
	if driver.writes != 0 {
		t.Fatal("rejected approval executed changes")
	}
	approved := append(append([]string{}, saved...), "--approve", plan.Hash, "--idempotency-key", "output-failure-request")
	if err := bootstrapCommandWithSession(approved, nil, &outputAfterPlan{}, factory); err == nil {
		t.Fatal("delivery failure hidden")
	}
	record, err := bootstrap.ReadJournal(dir)
	if err != nil || len(record.Operations) != 1 || record.Operations[0].State != bootstrap.Succeeded || driver.writes != 1 {
		t.Fatal("stdout failure lost actual completion", err)
	}
	resume := []string{"resume", "--config", path, "--state-dir", dir, "--operation", record.Operations[0].ID}
	if err = bootstrapCommandWithSession(resume, nil, writerFailure{}, factory); err == nil {
		t.Fatal("resume output failure hidden")
	}
	if err = bootstrapCommand([]string{"status", "--state-dir", dir}, nil, writerFailure{}); err == nil {
		t.Fatal("status output failure hidden")
	}
	if driver.writes != 1 {
		t.Fatal("delivery retry repeated external changes")
	}
}

func TestCLIPlanCheckpointFailurePreventsExecution(t *testing.T) {
	path, dir := cliConfig(t)
	os.MkdirAll(filepath.Join(dir, "plan.json"), 0700)
	driver := &cliDriver{}
	if err := bootstrapCommandWithSession([]string{"init", "--config", path, "--state-dir", dir}, nil, io.Discard, cliFactory(driver)); err == nil {
		t.Fatal("unwritable plan checkpoint accepted")
	}
	if driver.writes != 0 {
		t.Fatal("changes executed without a durable plan")
	}
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	input := strings.Join([]string{"single", "192.0.2.1", "root", "", "/keys/known_hosts", image, image, image}, "\n") + "\n"
	guide := t.TempDir()
	os.Mkdir(filepath.Join(guide, "install.yaml"), 0700)
	if _, err := bootstrapGuide(strings.NewReader(input), io.Discard, guide); err == nil {
		t.Fatal("unwritable guided configuration accepted")
	}
	if _, err := bootstrapGuide(strings.NewReader("invalid-mode\n192.0.2.1\nroot\n\n/keys/known_hosts\n"+image+"\n"+image+"\n"+image+"\n"), io.Discard, t.TempDir()); err == nil {
		t.Fatal("invalid guided mode accepted")
	}
}

func TestCLIIndependentSessionReturnsHandoffOnlyAfterDurableCompletion(t *testing.T) {
	path, dir := cliConfig(t)
	driver := &cliDriver{}
	base := []string{"init", "--config", path, "--state-dir", dir}
	if err := bootstrapCommandWithSession(base, nil, io.Discard, cliFactory(driver)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
	var plan bootstrap.Plan
	json.Unmarshal(data, &plan)
	certificate, _, err := pki.SelfSigned(nil, []net.IP{net.ParseIP("192.0.2.1")}, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	factory := func(_ context.Context, cfg bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, directory string, options bootstrapSessionOptions) (bootstrapSession, error) {
		if err := os.MkdirAll(filepath.Join(directory+".offline", "materials"), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(directory+".offline", "materials", "console.crt"), certificate, 0600); err != nil {
			return nil, err
		}
		actual := &install.Installer{Config: cfg, Record: r, Journal: j, Directory: directory}
		return installationSession{driver: actual, executor: bootstrap.Executor{Journal: j, Driver: driver}, record: r}, nil
	}
	approved := append(append([]string{}, base...), "--plan", filepath.Join(dir, "plan.json"), "--approve", plan.Hash, "--idempotency-key", "handoff-metadata-fixture", "--credential-file", filepath.Join(dir, "current-credential"))
	var output bytes.Buffer
	if err = bootstrapCommandWithSession(approved, nil, &output, factory); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "certificate_sha256") || !strings.Contains(output.String(), "database_single_point_of_failure") {
		t.Fatal("durable handoff metadata missing")
	}
	badFactory := func(ctx context.Context, c bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, d string, options bootstrapSessionOptions) (bootstrapSession, error) {
		session, err := factory(ctx, c, r, j, d, options)
		if err != nil {
			return nil, err
		}
		os.Remove(filepath.Join(d+".offline", "materials", "console.crt"))
		return session, nil
	}
	if err = bootstrapCommandWithSession(approved, nil, io.Discard, badFactory); err == nil {
		t.Fatal("missing handoff trust file was hidden")
	}
	if driver.writes != 1 {
		t.Fatal("handoff retry repeated infrastructure changes")
	}
}

func TestDuplicateUnknownCLIOperationDoesNotReturnSuccessfulExit(t *testing.T) {
	path, dir := cliConfig(t)
	driver := &cliDriver{}
	factory := cliFactory(driver)
	base := []string{"init", "--config", path, "--state-dir", dir}
	if err := bootstrapCommandWithSession(base, nil, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "plan.json"))
	var plan bootstrap.Plan
	json.Unmarshal(data, &plan)
	approved := append(append([]string{}, base...), "--plan", filepath.Join(dir, "plan.json"), "--approve", plan.Hash, "--idempotency-key", "unknown-duplicate-request")
	driver.failed = true
	if err := bootstrapCommandWithSession(approved, nil, io.Discard, factory); err == nil {
		t.Fatal("unknown result reported success")
	}
	writes := driver.writes
	if err := bootstrapCommandWithSession(approved, nil, io.Discard, factory); err == nil || !strings.Contains(err.Error(), "UNKNOWN") {
		t.Fatal("duplicate unknown result reported success", err)
	}
	if driver.writes != writes {
		t.Fatal("unknown duplicate repeated changes")
	}
}
