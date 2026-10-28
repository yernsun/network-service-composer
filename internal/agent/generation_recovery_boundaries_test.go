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
)

func TestPrepareRejectsCorruptRecoveryMetadataAndInvalidOldRenderer(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		corrupt, oldRenderer, commandFailure, blockState bool
	}{
		{"corrupt metadata", true, false, false, false}, {"older renderer rejected", false, true, true, false}, {"older renderer verified", false, true, false, false}, {"recovery write blocked", false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := "exit 0"
			if tc.commandFailure {
				command = "echo incompatible; exit 1"
			}
			m := validationManagerFixture(t, command)
			target, err := os.Readlink(filepath.Join(m.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			state := GenerationState{PoolID: "pool", ConfigRevision: 4, ModelDigest: strings.Repeat("a", 64), RenderedDigest: strings.Repeat("b", 64), RendererVersion: "older-renderer"}
			if !tc.oldRenderer {
				state.RendererVersion = m.State().RendererVersion
			}
			if err := writeGenerationStateFile(filepath.Join(m.root, target), "generation.json", state); err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				if err := os.WriteFile(filepath.Join(m.root, target, "generation.json"), []byte("["), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.blockState {
				if err := os.Mkdir(filepath.Join(m.root, ".state.json-next"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := m.Prepare()
			failure := tc.corrupt || tc.commandFailure || tc.blockState
			if (err != nil) != failure {
				t.Fatalf("recovery %#v %v", recovered, err)
			}
			if !failure && recovered != state {
				t.Fatalf("recovered wrong generation: %#v", recovered)
			}
			if current, err := os.Readlink(filepath.Join(m.root, "current")); err != nil || current != target {
				t.Fatal("recovery replaced current configuration")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		setup func(string) string
	}{
		{"root is file", func(root string) string {
			path := filepath.Join(root, "file")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"current is directory", func(root string) string {
			if err := os.Mkdir(filepath.Join(root, "current"), 0700); err != nil {
				t.Fatal(err)
			}
			return root
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.setup(t.TempDir())
			m := NewGenerationManager(config.NGINXConfig{GenerationDir: root})
			if _, err := m.Prepare(); err == nil {
				t.Fatal("invalid recovery filesystem accepted")
			}
		})
	}
	m := NewGenerationManager(config.NGINXConfig{GenerationDir: t.TempDir(), MainConfigPath: filepath.Join(t.TempDir(), "directory")})
	if err := os.Mkdir(m.config.MainConfigPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(); err == nil {
		t.Fatal("invalid fixed configuration path accepted")
	}
}

func TestGenerationProcessLifecycleDoesNotLeakOnCancellation(t *testing.T) {
	for _, ignoreQuit := range []bool{false, true} {
		root := t.TempDir()
		binary := filepath.Join(root, "process-fixture")
		ready := binary + ".ready"
		trap := "trap 'exit 0' QUIT"
		if ignoreQuit {
			trap = "trap '' QUIT"
		}
		// No child processes or sockets: only the command process under test.
		script := "#!/bin/sh\n" + trap + "\nprintf ready > \"$0.ready\"\nwhile :; do :; done\n"
		if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		m := NewGenerationManager(config.NGINXConfig{GenerationDir: root, Binary: binary})
		done, err := m.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if m.process != nil && m.process.Process != nil {
				_ = m.process.Process.Kill()
			}
		})
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fixture process did not start")
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := m.Start(); err == nil {
			t.Fatal("second process started")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if ignoreQuit {
			cancel()
		}
		err = m.Stop(ctx)
		cancel()
		if ignoreQuit {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("forced stop: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("process wait did not finish")
		}
		if err := m.Stop(context.Background()); err != nil {
			t.Fatalf("stopping exited process: %v", err)
		}
	}
	m := NewGenerationManager(config.NGINXConfig{Binary: filepath.Join(t.TempDir(), "missing")})
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(); err == nil {
		t.Fatal("missing executable reported started")
	}
}
