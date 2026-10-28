package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerationEvidenceSeparatesLoadingFromRestartSafety(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	var revision atomic.Int64
	revision.Store(7)
	listener, e := net.Listen("tcp", "127.0.0.1:80")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_nsc/generation-health" {
			w.WriteHeader(404)
			return
		}
		if revision.Load() == 7 {
			w.Header().Set("X-NSC-Config-Revision", "7")
		} else {
			w.Header().Set("X-NSC-Config-Revision", "6")
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	for _, stage := range []string{"confirmed", "unknown revision", "applying", "old running revision", "missing startup", "broken fragment", "different disk digest", "foreign main config", "startup validation failed", "canceled observation"} {
		t.Run(stage, func(t *testing.T) {
			revision.Store(7)
			command := "exit 0"
			if stage == "startup validation failed" {
				command = "exit 1"
			}
			m := validationManagerFixture(t, command)
			directory, e := filepath.EvalSymlinks(filepath.Join(m.root, "current"))
			if e != nil {
				t.Fatal(e)
			}
			digest, e := digestGeneration(directory)
			if e != nil {
				t.Fatal(e)
			}
			m.state = GenerationState{PoolID: "pool", ConfigRevision: 7, ModelDigest: "model", RenderedDigest: digest}
			ctx := context.Background()
			locked := false
			switch stage {
			case "unknown revision":
				m.state.ConfigRevision = 0
			case "applying":
				m.operationMu.Lock()
				locked = true
				defer m.operationMu.Unlock()
			case "old running revision":
				revision.Store(6)
			case "missing startup":
				if e = os.Remove(filepath.Join(m.root, "current")); e != nil {
					t.Fatal(e)
				}
			case "broken fragment":
				if e = os.Symlink("missing-file", filepath.Join(directory, "broken.conf")); e != nil {
					t.Fatal(e)
				}
			case "different disk digest":
				m.state.RenderedDigest = "different"
			case "foreign main config":
				main := filepath.Join(directory, generationMainConfigName)
				if e = os.Remove(main); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(main, []byte("events {}"), 0600); e != nil {
					t.Fatal(e)
				}
				m.state.RenderedDigest, e = digestGeneration(directory)
				if e != nil {
					t.Fatal(e)
				}
			case "canceled observation":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before := m.State()
			started := time.Now()
			v := m.Observe(ctx)
			if v.State != before || m.State() != before {
				t.Fatal("observation mutated configured generation", v)
			}
			if stage == "unknown revision" || locked {
				if v.LoadingConfirmed || v.StartupState != "UNKNOWN" || !v.ObservedAt.IsZero() || time.Since(started) > 100*time.Millisecond {
					t.Fatal("unobserved generation presented as verified", v)
				}
				return
			}
			loading := stage != "old running revision" && stage != "canceled observation"
			startup := "CONFIRMED"
			switch stage {
			case "missing startup", "broken fragment", "different disk digest", "foreign main config", "startup validation failed", "canceled observation":
				startup = "RISK"
			}
			if v.LoadingConfirmed != loading || v.StartupState != startup || v.ObservedAt.Before(started) {
				t.Fatal("loading and startup evidence conflated", stage, v)
			}
			if startup == "CONFIRMED" && v.DiskDigest != before.RenderedDigest {
				t.Fatal("startup confirmed without matching disk evidence", v)
			}
		})
	}
}
