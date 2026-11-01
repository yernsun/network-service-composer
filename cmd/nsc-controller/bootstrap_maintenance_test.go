package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestMaintenanceCLIValidatesActionSpecificInputsBeforeSSH(t *testing.T) {
	file, dir := cliConfig(t)
	release := filepath.Join(t.TempDir(), "release.json")
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	value := bootstrap.Release{Version: "v0.1.0-fixture", Images: bootstrap.Images{Controller: image, Gateway: image, PostgreSQL: image}, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
	raw, _ := json.Marshal(value)
	if err := os.WriteFile(release, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "invalid.json")
	os.WriteFile(bad, []byte(`{"unknown":true}`), 0600)
	connected := 0
	factory := func(ctx context.Context, cfg bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, directory string, options bootstrapSessionOptions) (bootstrapSession, error) {
		connected++
		return cliFactory(&cliDriver{})(ctx, cfg, r, j, directory, options)
	}
	for _, args := range [][]string{
		{"init", "--prepare-web"}, {"adopt", "--prepare-web"}, {"resume", "--prepare-web"},
		{"upgrade", "--prepare-web"}, {"upgrade", "--prepare-web", "--ca-cert-file", "/offline/ca.crt"},
		{"upgrade", "--prepare-web", "--ca-cert-file", "relative-ca.crt", "--ca-key-file", "/offline/ca.key"},
		{"verify", "--ca-key-file", "/offline/ca.key"}, {"upgrade", "--ca-key-file", "/offline/ca.key"}, {"resume", "--ca-cert-file", "/offline/ca.crt"},
		{"repair", "--release", release}, {"verify", "--release", release}, {"status", "--release", release},
		{"upgrade", "--plan", "plan.json", "--release", release}, {"init", "--components", "controller"},
		{"repair", "--plan", "plan.json", "--components", "controller"}, {"upgrade", "--release", bad}, {"upgrade", "--release", release + "-missing"},
	} {
		args = append(args, "--config", file, "--state-dir", dir)
		if err := bootstrapCommandWithSession(args, nil, io.Discard, factory); err == nil {
			t.Fatal("invalid action-specific input accepted", args)
		}
	}
	if connected != 0 {
		t.Fatal("invalid input reached SSH/session connection")
	}
	for _, command := range []string{"upgrade", "repair"} {
		optionsSeen := false
		factory = func(ctx context.Context, cfg bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, directory string, options bootstrapSessionOptions) (bootstrapSession, error) {
			if options.CredentialFile != "/run/nsc/current-credential" {
				t.Fatal("credential reference not forwarded")
			}
			if command == "upgrade" {
				if options.Release == nil || options.Release.Version != value.Version || len(options.Components) != 0 {
					t.Fatal("release manifest was not bound to the session")
				}
			} else if len(options.Components) != 2 || options.Components[0] != "controller" || options.Release != nil {
				t.Fatal("repair components were not bound to the session")
			}
			optionsSeen = true
			return cliFactory(&cliDriver{})(ctx, cfg, r, j, directory, options)
		}
		args := []string{command, "--config", file, "--state-dir", dir, "--credential-file", "/run/nsc/current-credential"}
		if command == "upgrade" {
			args = append(args, "--release", release)
		} else {
			args = append(args, "--components", "controller,nginx")
		}
		if err := bootstrapCommandWithSession(args, nil, io.Discard, factory); err != nil || !optionsSeen {
			t.Fatal("valid maintenance inputs were not planned", err)
		}
	}
}

type sessionInstaller struct {
	cliSession
	executed []bootstrap.Action
	resumed  []string
}

func (s *sessionInstaller) ExecuteMaintenance(ctx context.Context, plan bootstrap.Plan, approval, key string) (bootstrap.Operation, error) {
	s.executed = append(s.executed, plan.Action)
	return s.cliSession.Execute(ctx, plan, approval, key)
}
func (s *sessionInstaller) ResumeMaintenance(ctx context.Context, id string) (bootstrap.Operation, error) {
	s.resumed = append(s.resumed, id)
	return s.cliSession.Resume(ctx, id)
}
func (*sessionInstaller) Handoff(io.Writer) error { return nil }
func TestInstallationSessionRoutesAllDayTwoActionsAndTheirOriginalResumes(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "metadata")
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	record := bootstrap.Record{ID: "installation-fixture", Revision: 1, ConfigDigest: "config", Resources: map[string]bootstrap.Resource{}, Operations: []bootstrap.Operation{}}
	executor := bootstrap.Executor{Journal: journal, Driver: &cliDriver{}}
	driver := &sessionInstaller{cliSession: cliSession{record: &record, executor: executor}}
	session := installationSession{driver: driver, executor: executor, record: &record}
	for index, action := range []bootstrap.Action{bootstrap.Init, bootstrap.Repair, bootstrap.Upgrade, bootstrap.Rollback} {
		plan, err := session.Plan(t.Context(), action)
		if err != nil {
			t.Fatal(err)
		}
		operation, err := session.Execute(t.Context(), plan, plan.Hash, "session-dispatch-key-"+string(rune('a'+index)))
		if err != nil || operation.State != bootstrap.Succeeded {
			t.Fatal("wrong executor dispatch", action, err)
		}
		resumed, err := session.Resume(t.Context(), operation.ID)
		if err != nil || resumed.ID != operation.ID {
			t.Fatal("resume changed operation identity", err)
		}
	}
	if len(driver.executed) != 3 || len(driver.resumed) != 3 {
		t.Fatal("maintenance escaped its independent execution adapter")
	}
	if _, err = session.Resume(t.Context(), "foreign-operation"); err == nil {
		t.Fatal("unknown operation resumed")
	}
	if err = session.Verify(t.Context(), io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPreparationCLIForwardsOnlyExplicitOfflineReferences(t *testing.T) {
	file, directory := cliConfig(t)
	seen := false
	factory := func(ctx context.Context, cfg bootstrap.Config, r *bootstrap.Record, j *bootstrap.Journal, stateDirectory string, options bootstrapSessionOptions) (bootstrapSession, error) {
		if !options.PrepareWeb || options.CACertificateFile != "/offline/ca.crt" || options.CAPrivateKeyFile != "/offline/ca.key" {
			t.Fatal("original offline references were not retained")
		}
		seen = true
		return cliFactory(&cliDriver{})(ctx, cfg, r, j, stateDirectory, options)
	}
	args := []string{"upgrade", "--config", file, "--state-dir", directory, "--prepare-web", "--ca-cert-file", "/offline/ca.crt", "--ca-key-file", "/offline/ca.key"}
	if err := bootstrapCommandWithSession(args, nil, io.Discard, factory); err != nil || !seen {
		t.Fatal("legacy preparation was not routed to its CLI session", err)
	}
}
