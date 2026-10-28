package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
)

func TestGenerationStateFilesRejectInvalidNamesAndPreserveCommittedState(t *testing.T) {
	root := t.TempDir()
	state := GenerationState{PoolID: "pool", ConfigRevision: 4, ModelDigest: strings.Repeat("a", 64), RenderedDigest: strings.Repeat("b", 64), RendererVersion: nginxx.RendererVersion}
	if err := writeGenerationState(root, state); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "../outside", "directory/state.json"} {
		if err := writeGenerationStateFile(root, name, state); err == nil {
			t.Fatalf("unsafe state name accepted: %q", name)
		}
	}
	badTime := state
	badTime.AppliedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeGenerationState(root, badTime); err == nil {
		t.Fatal("invalid state serialized")
	}
	if err := writeGenerationState(filepath.Join(root, "missing"), state); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "target-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeGenerationStateFile(root, "target-directory", state); err == nil {
		t.Fatal("directory replaced by state file")
	}
	if err := os.Mkdir(filepath.Join(root, ".blocked-next"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeGenerationStateFile(root, "blocked", state); err == nil {
		t.Fatal("state temporary path collision ignored")
	}
	got, err := readGenerationState(filepath.Join(root, "state.json"))
	if err != nil || got != state {
		t.Fatalf("committed state changed: %#v %v", got, err)
	}
	if _, err := readGenerationState(filepath.Join(root, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing metadata %v", err)
	}
	corrupt := filepath.Join(root, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGenerationState(corrupt); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
}

func TestGenerationSymlinkReplacementRejectsTraversalAndDirectoryCollisions(t *testing.T) {
	root := t.TempDir()
	if err := replaceSymlink(root, "initial"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", ".", "../escape", "nested/target"} {
		if err := replaceSymlink(root, target); err == nil {
			t.Fatalf("unsafe symlink accepted: %q", target)
		}
	}
	current, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil || current != "initial" {
		t.Fatal("invalid target changed active generation")
	}
	if err := replaceSymlink(filepath.Join(root, "missing"), "next"); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := os.Remove(filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "current"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := replaceSymlink(root, "next"); err == nil {
		t.Fatal("directory collision ignored")
	}
	if _, err := os.Lstat(filepath.Join(root, ".current-next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed activation left pending symlink")
	}
	if err := syncDirectory(filepath.Join(root, "absent")); err == nil {
		t.Fatal("missing directory sync accepted")
	}
}

func TestFragmentReusePreservesChangedContentPermissionsAndMetadata(t *testing.T) {
	old, candidate := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{"same": "same content", "changed": "old", "mode": "mode content", "generation.json": "old identity"} {
		if err := os.WriteFile(filepath.Join(old, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"same": "same content", "changed": "new", "mode": "mode content", "generation.json": "new identity", "new": "new fragment"} {
		if err := os.WriteFile(filepath.Join(candidate, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(candidate, "mode"), 0640); err != nil {
		t.Fatal(err)
	}
	before, err := digestGeneration(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = reuseUnchangedFragments(old, candidate); err != nil {
		t.Fatal(err)
	}
	after, err := digestGeneration(candidate)
	if err != nil || before != after {
		t.Fatalf("reuse changed rendered generation: %q %q %v", before, after, err)
	}
	for _, name := range []string{"same", "changed", "mode", "generation.json"} {
		a, err := os.Stat(filepath.Join(old, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Stat(filepath.Join(candidate, name))
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(a, b) != (name == "same") {
			t.Fatalf("unsafe reuse of %s", name)
		}
	}
	if err := os.WriteFile(filepath.Join(candidate, "generation.json"), []byte("another identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if digest, err := digestGeneration(candidate); err != nil || digest != before {
		t.Fatal("metadata changed content digest")
	}
	if err := os.Chmod(filepath.Join(candidate, "mode"), 0600); err != nil {
		t.Fatal(err)
	}
	if digest, err := digestGeneration(candidate); err != nil || digest == before {
		t.Fatal("permission change not represented by digest")
	}
	if err := reuseUnchangedFragments(filepath.Join(old, "absent"), candidate); err != nil {
		t.Fatal(err)
	}
	if err := reuseUnchangedFragments(old, filepath.Join(candidate, "absent")); err == nil {
		t.Fatal("missing candidate accepted")
	}
	if _, err := digestGeneration(filepath.Join(candidate, "absent")); err == nil {
		t.Fatal("missing generation got digest")
	}
	if err := os.Symlink("missing", filepath.Join(candidate, "broken")); err != nil {
		t.Fatal(err)
	}
	if _, err := digestGeneration(candidate); err == nil {
		t.Fatal("unreadable fragment got digest")
	}
}

func TestGenerationPruningKeepsCurrentAndConfiguredHistory(t *testing.T) {
	for _, keep := range []int{0, 1, 3} {
		root := t.TempDir()
		for _, name := range []string{"gen-01", "gen-02", "gen-03", "gen-04", "gen-05", "unmanaged"} {
			if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "gen-file"), []byte("unmanaged file"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := replaceSymlink(root, "gen-01"); err != nil {
			t.Fatal(err)
		}
		manager := NewGenerationManager(config.NGINXConfig{GenerationDir: root, KeepGenerations: keep})
		if err := manager.prune(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"gen-01", "gen-05", "unmanaged", "gen-file"} {
			if _, err := os.Stat(filepath.Join(root, name)); err != nil {
				t.Fatalf("protected %s removed: %v", name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "gen-02")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("obsolete generation retained")
		}
		if keep == 1 {
			if _, err := os.Stat(filepath.Join(root, "gen-04")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("history limit ignored")
			}
		} else if _, err := os.Stat(filepath.Join(root, "gen-03")); err != nil {
			t.Fatal("default history lost")
		}
	}
	manager := NewGenerationManager(config.NGINXConfig{GenerationDir: filepath.Join(t.TempDir(), "missing")})
	if err := manager.prune(); err == nil {
		t.Fatal("failed history read hidden")
	}
}

func TestGenerationInputAndRenderFailuresLeaveCurrentUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, command, phase string
		change               func(*GenerationManager)
	}{
		{"invalid digest", "exit 0", "input", nil},
		{"missing pool", "exit 0", "input", nil},
		{"wrong pool", "exit 0", "input", func(m *GenerationManager) { m.state.PoolID = "other" }},
		{"render error", "exit 0", "render", nil},
		{"validation failure", "echo rejected; exit 1", "validate", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validationManagerFixture(t, tc.command)
			previous, err := os.Readlink(filepath.Join(m.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.change != nil {
				tc.change(m)
			}
			snapshot := validationSnapshot()
			digest := strings.Repeat("a", 64)
			switch tc.name {
			case "invalid digest":
				digest = "invalid"
			case "missing pool":
				snapshot.PoolId = ""
			case "render error":
				snapshot.ChallengeUpstream = "file:///invalid"
			}
			result, err := m.Apply(context.Background(), snapshot, digest)
			if err == nil || result.FailedPhase != tc.phase {
				t.Fatalf("failure phase %#v %v", result, err)
			}
			assertValidationCleaned(t, m, previous)
		})
	}
	m := validationManagerFixture(t, "exit 0")
	if _, err := m.Apply(context.Background(), nil, ""); !errors.Is(err, ErrOldRevision) {
		t.Fatalf("nil snapshot: %v", err)
	}
	snapshot := validationSnapshot()
	snapshot.ConfigRevision = 0
	if _, err := m.Apply(context.Background(), snapshot, strings.Repeat("a", 64)); !errors.Is(err, ErrOldRevision) {
		t.Fatalf("old revision: %v", err)
	}
	if truncateOutput([]byte(" \n short \n")) != "short" || len(truncateOutput([]byte(strings.Repeat("x", 4097)))) != 4096 {
		t.Fatal("invalid command output limit")
	}
}
