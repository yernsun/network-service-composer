package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
)

func TestHealthcheckReadOnlyAndFailureBoundaries(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-NSC-Config-Revision", "7")
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	for _, stage := range []string{"healthy disconnected", "missing root", "missing current", "escape", "empty generation", "wrong pool", "missing pool", "missing renderer", "invalid model digest", "invalid rendered digest", "missing metadata", "malformed metadata", "missing state", "state mismatch", "disk corruption", "old loaded revision", "invalid nginx", "concurrent switch", "concurrent valid switch", "concurrent metadata", "concurrent disk", "concurrent unreadable disk", "cancelled"} {
		t.Run(stage, func(t *testing.T) {
			m := validationManagerFixture(t, "exit 0")
			directory, err := filepath.EvalSymlinks(filepath.Join(m.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			digest, err := digestGeneration(directory)
			if err != nil {
				t.Fatal(err)
			}
			state := GenerationState{PoolID: "pool", ConfigRevision: 7, ModelDigest: strings.Repeat("a", 64), RenderedDigest: digest, RendererVersion: "previous-renderer", AppliedAt: time.Now().UTC()}
			if stage == "empty generation" {
				state.ConfigRevision = 0
			}
			if stage == "old loaded revision" {
				state.ConfigRevision = 6
			}
			switch stage {
			case "missing renderer":
				state.RendererVersion = ""
			case "invalid model digest":
				state.ModelDigest = "invalid"
			case "invalid rendered digest":
				state.RenderedDigest = "invalid"
			}
			if err := writeGenerationStateFile(directory, "generation.json", state); err != nil {
				t.Fatal(err)
			}
			if err := writeGenerationState(m.root, state); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{NGINX: m.config, Agent: config.AgentConfig{PoolID: "pool"}}
			write := func(path, value string) {
				t.Helper()
				if e := os.WriteFile(path, []byte(value), 0700); e != nil {
					t.Fatal(e)
				}
			}
			switch stage {
			case "missing root":
				cfg.NGINX.GenerationDir = filepath.Join(t.TempDir(), "absent")
			case "missing current":
				if err := os.Remove(filepath.Join(m.root, "current")); err != nil {
					t.Fatal(err)
				}
			case "escape":
				if err := os.Remove(filepath.Join(m.root, "current")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(m.root, "current")); err != nil {
					t.Fatal(err)
				}
			case "wrong pool":
				cfg.Agent.PoolID = "different"
			case "missing pool":
				cfg.Agent.PoolID = ""
			case "missing metadata":
				if err := os.Remove(filepath.Join(directory, "generation.json")); err != nil {
					t.Fatal(err)
				}
			case "malformed metadata":
				write(filepath.Join(directory, "generation.json"), "{")
			case "state mismatch":
				write(filepath.Join(m.root, "state.json"), "{}")
			case "missing state":
				if err := os.Remove(filepath.Join(m.root, "state.json")); err != nil {
					t.Fatal(err)
				}
			case "disk corruption":
				write(filepath.Join(directory, "injected.conf"), "invalid")
			case "invalid nginx":
				write(m.config.Binary, "#!/bin/sh\nexit 1\n")
			case "concurrent switch":
				write(m.config.Binary, "#!/bin/sh\nrm '"+filepath.Join(m.root, "current")+"'\n")
			case "concurrent valid switch":
				other := filepath.Join(m.root, "replacement")
				write(m.config.Binary, "#!/bin/sh\ncp -a '"+directory+"' '"+other+"'\nrm '"+filepath.Join(m.root, "current")+"'\nln -s '"+other+"' '"+filepath.Join(m.root, "current")+"'\n")
			case "concurrent metadata":
				state.ModelDigest = strings.Repeat("b", 64)
				raw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				candidate := filepath.Join(filepath.Dir(m.root), "candidate.json")
				write(candidate, string(raw))
				write(m.config.Binary, "#!/bin/sh\ncp '"+candidate+"' '"+filepath.Join(directory, "generation.json")+"'\ncp '"+candidate+"' '"+filepath.Join(m.root, "state.json")+"'\n")
			case "concurrent disk":
				write(m.config.Binary, "#!/bin/sh\nprintf changed > '"+filepath.Join(directory, "injected.conf")+"'\n")
			case "concurrent unreadable disk":
				write(m.config.Binary, "#!/bin/sh\nln -s absent '"+filepath.Join(directory, "broken.conf")+"'\n")
			}
			before := healthFiles(t, m.root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "cancelled" {
				cancel()
			}
			err = Healthcheck(ctx, cfg)
			if (err == nil) != (stage == "healthy disconnected") {
				t.Fatalf("unexpected health result: %v", err)
			}
			if !strings.HasPrefix(stage, "concurrent ") && !reflect.DeepEqual(before, healthFiles(t, m.root)) {
				t.Fatal("healthcheck modified generation state")
			}
		})
	}
}

func healthFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[path+":mtime"] = info.ModTime().String()
		if entry.Type()&os.ModeSymlink != 0 {
			files[path], err = os.Readlink(path)
		} else if !entry.IsDir() {
			var data []byte
			data, err = os.ReadFile(path)
			files[path] = string(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
