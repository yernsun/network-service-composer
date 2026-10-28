package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

// The command fixture models NGINX command results, not NGINX syntax validity.
// Real syntax/reload verification remains in the pinned-image integration suite.
func validationManagerFixture(t *testing.T, command string) *GenerationManager {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "nginx-fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+command+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m := NewGenerationManager(config.NGINXConfig{GenerationDir: filepath.Join(root, "generations"), Binary: binary, KeepGenerations: 3, WorkerProcesses: "1"})
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	return m
}
func validationSnapshot() *gatewayv2.Snapshot {
	return &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 1, ChallengeUpstream: "http://127.0.0.1:1"}
}
func assertValidationCleaned(t *testing.T, m *GenerationManager, previous string) {
	t.Helper()
	current, err := os.Readlink(filepath.Join(m.root, "current"))
	if err != nil || current != previous {
		t.Fatalf("candidate changed current configuration: %q %v", current, err)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".validation-") {
			t.Fatalf("candidate directory leaked: %s", entry.Name())
		}
	}
	if m.State().ConfigRevision != 0 {
		t.Fatal("candidate validation changed last known running revision")
	}
}
func TestCandidateValidationNeverActivatesProductionConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, command, want string
		mutate              func(*gatewayv2.ValidationRequest)
	}{
		{"valid candidate", "exit 0", "", func(*gatewayv2.ValidationRequest) {}},
		{"missing snapshot", "exit 0", "role mismatch", func(r *gatewayv2.ValidationRequest) { r.Snapshot = nil }},
		{"production activation forbidden", "exit 0", "role mismatch", func(r *gatewayv2.ValidationRequest) { r.ActivateSandbox = true }},
		{"invalid rendered data", "exit 0", "challenge", func(r *gatewayv2.ValidationRequest) { r.Snapshot.ChallengeUpstream = "file:///invalid" }},
		{"NGINX rejects candidate", "echo 'candidate rejected'; exit 1", "nginx -t: candidate rejected", func(*gatewayv2.ValidationRequest) {}},
		{"candidate content lost after validation", `rm "$5/nginx.conf"
ln -s "$5/missing" "$5/nginx.conf"
exit 0`, "no such file", func(*gatewayv2.ValidationRequest) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validationManagerFixture(t, tc.command)
			previous, err := os.Readlink(filepath.Join(m.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			r := &gatewayv2.ValidationRequest{TaskId: "task", Snapshot: validationSnapshot()}
			tc.mutate(r)
			result := m.Validate(context.Background(), r, false)
			if result.TaskId != "task" {
				t.Fatal("validation lost task identity")
			}
			if tc.want == "" {
				if !result.Success || result.RenderedDigest == "" || result.Error != "" {
					t.Fatalf("valid candidate result: %#v", result)
				}
			} else if result.Success || !strings.Contains(result.Error, tc.want) {
				t.Fatalf("candidate failure misreported: %#v", result)
			}
			assertValidationCleaned(t, m, previous)
		})
	}
}

func TestCandidateValidationWorkspaceAndCancellationFailures(t *testing.T) {
	t.Run("missing workspace", func(t *testing.T) {
		m := NewGenerationManager(config.NGINXConfig{GenerationDir: filepath.Join(t.TempDir(), "missing")})
		result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{Snapshot: validationSnapshot()}, false)
		if result.Success || result.Error != "cannot create validation directory" {
			t.Fatalf("missing workspace result=%#v", result)
		}
	})
	t.Run("cancelled candidate", func(t *testing.T) {
		m := validationManagerFixture(t, "exit 0")
		previous, err := os.Readlink(filepath.Join(m.root, "current"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := m.Validate(ctx, &gatewayv2.ValidationRequest{Snapshot: validationSnapshot()}, false)
		if result.Success || !strings.Contains(result.Error, "nginx -t") {
			t.Fatalf("cancelled candidate reported success: %#v", result)
		}
		assertValidationCleaned(t, m, previous)
	})
	t.Run("sandbox without previous configuration", func(t *testing.T) {
		m := validationManagerFixture(t, "exit 0")
		if err := os.Remove(filepath.Join(m.root, "current")); err != nil {
			t.Fatal(err)
		}
		result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{Snapshot: validationSnapshot(), ActivateSandbox: true}, true)
		if result.Success || !strings.Contains(result.Error, "current") {
			t.Fatalf("sandbox without recovery point activated: %#v", result)
		}
	})
	t.Run("sandbox command and restore fail", func(t *testing.T) {
		m := validationManagerFixture(t, "if [ \"$1\" = '-t' ]; then exit 0; fi\necho reload-rejected\nexit 1")
		previous, err := os.Readlink(filepath.Join(m.root, "current"))
		if err != nil {
			t.Fatal(err)
		}
		result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{Snapshot: validationSnapshot(), ActivateSandbox: true}, true)
		if result.Success || !strings.Contains(result.Error, "sandbox restore failed") {
			t.Fatalf("failed recovery hidden: %#v", result)
		}
		assertValidationCleaned(t, m, previous)
	})
}

func TestHeldValidationCancellationKeepsUnrelatedTaskAndRecoveryEvidence(t *testing.T) {
	m := validationManagerFixture(t, "exit 1")
	if err := m.CancelValidation(context.Background(), "unknown"); err != nil {
		t.Fatal(err)
	}
	hold := &heldValidation{task: "held", previous: "old", directory: filepath.Join(m.root, "held-directory")}
	m.held = hold
	if err := m.CancelValidation(context.Background(), "another"); err != nil || m.held != hold {
		t.Fatal("unrelated cancel discarded held validation")
	}
	// An unexpected directory in place of the current symlink prevents a safe
	// switch; retain the recovery record instead of claiming cancellation.
	if err := os.Remove(filepath.Join(m.root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.root, "current"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"held", ""} {
		if err := m.CancelValidation(context.Background(), task); err == nil || m.held != hold {
			t.Fatalf("failed cancellation cleared recovery record: %v", err)
		}
	}
	result := m.Validate(context.Background(), &gatewayv2.ValidationRequest{Snapshot: validationSnapshot()}, false)
	if result.Success || result.Error == "" || m.held != hold {
		t.Fatalf("new validation bypassed failed recovery: %#v", result)
	}
}
