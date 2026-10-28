package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"google.golang.org/protobuf/proto"
)

func TestGenerationApplyRecoveryAndExternalChangesInIsolatedNetwork(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	// Model only the NGINX command boundary. The HTTP fixture reports the
	// candidate identity so Apply must still wait for generation-specific evidence.
	m := validationManagerFixture(t, "exit 0")
	listener, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		target, err := os.Readlink(filepath.Join(m.root, "current"))
		if err != nil {
			w.WriteHeader(503)
			return
		}
		revision := int64(0)
		if strings.HasPrefix(target, "gen-") {
			state, err := readGenerationState(filepath.Join(m.root, target, "generation.json"))
			if err != nil {
				w.WriteHeader(503)
				return
			}
			revision = state.ConfigRevision
		}
		w.Header().Set("X-NSC-Config-Revision", strconv.FormatInt(revision, 10))
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	snapshot := validationSnapshot()
	firstDigest := strings.Repeat("a", 64)
	result, err := m.Apply(context.Background(), snapshot, firstDigest)
	if err != nil || result.FailedPhase != "" || m.State().ConfigRevision != 1 {
		t.Fatalf("initial application %#v %v", result, err)
	}
	first := m.State()
	current, err := os.Readlink(filepath.Join(m.root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := readGenerationState(filepath.Join(m.root, "state.json"))
	if err != nil || persisted != first {
		t.Fatalf("applied state not persisted: %#v %v", persisted, err)
	}
	if _, err := m.Apply(context.Background(), snapshot, firstDigest); !errors.Is(err, ErrOldRevision) {
		t.Fatalf("old candidate accepted: %v", err)
	}
	snapshot.ConfigRevision = 2
	if err := os.Rename(filepath.Join(m.root, "current"), filepath.Join(m.root, "saved-current")); err != nil {
		t.Fatal(err)
	}
	if result, err := m.Apply(context.Background(), snapshot, firstDigest); err == nil || result.FailedPhase != "external-change" {
		t.Fatalf("missing current generation was replaced: %#v %v", result, err)
	}
	if err := os.Rename(filepath.Join(m.root, "saved-current"), filepath.Join(m.root, "current")); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(m.root, current, "broken-fragment")
	if err := os.Symlink("missing", broken); err != nil {
		t.Fatal(err)
	}
	if result, err := m.Apply(context.Background(), snapshot, firstDigest); err == nil || result.FailedPhase != "external-change" {
		t.Fatalf("unverifiable generation was replaced: %#v %v", result, err)
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	mainBytes, err := os.ReadFile(m.mainConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, err := os.Stat(m.mainConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(m.root, "alternate.conf")
	if err := os.WriteFile(alternate, mainBytes, mainInfo.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	mainLink := filepath.Join(m.root, current, generationMainConfigName)
	if err := os.Remove(mainLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alternate, mainLink); err != nil {
		t.Fatal(err)
	}
	if result, err := m.Apply(context.Background(), snapshot, firstDigest); err == nil || result.FailedPhase != "external-change" {
		t.Fatalf("unmanaged main configuration was adopted: %#v %v", result, err)
	}
	if err := os.Remove(mainLink); err != nil {
		t.Fatal(err)
	}
	if err := m.linkGenerationMainConfig(filepath.Join(m.root, current)); err != nil {
		t.Fatal(err)
	}
	foreign := proto.Clone(snapshot).(*gatewayv2.Snapshot)
	foreign.PoolId = "foreign"
	if result, err := m.Apply(context.Background(), foreign, firstDigest); err == nil || result.FailedPhase != "input" {
		t.Fatalf("pool identity changed: %#v %v", result, err)
	}
	external := filepath.Join(m.root, current, "external.conf")
	if err := os.WriteFile(external, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := m.Apply(context.Background(), snapshot, firstDigest); err == nil || result.FailedPhase != "external-change" {
		t.Fatalf("external configuration overwritten: %#v %v", result, err)
	}
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.config.Binary, []byte("#!/bin/sh\nif [ \"$1\" = \"-s\" ]; then\n case \"$6\" in *current)\n target=$(readlink \"$6\")\n case \"$target\" in gen-00000000000000000002-*) exit 1;; esac;;\n esac\nfi\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	result, err = m.Apply(context.Background(), snapshot, strings.Repeat("b", 64))
	if err == nil || result.FailedPhase != "reload" || !strings.Contains(err.Error(), "rolled back to last known good") {
		t.Fatalf("failed reload recovery: %#v %v", result, err)
	}
	if state := m.State(); state != first {
		t.Fatalf("failed reload changed committed state: %#v", state)
	}
	if target, err := os.Readlink(filepath.Join(m.root, "current")); err != nil || target != current {
		t.Fatal("failed reload did not restore restart configuration")
	}
	if err := os.WriteFile(m.config.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(m.root, ".state.json-next")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	result, err = m.Apply(context.Background(), snapshot, strings.Repeat("b", 64))
	if err == nil || result.FailedPhase != "persist" || !strings.Contains(err.Error(), "rolled back to last known good") {
		t.Fatalf("persistence failure recovery: %#v %v", result, err)
	}
	if m.State() != first {
		t.Fatal("uncommitted generation became confirmed")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	result, err = m.Apply(context.Background(), snapshot, strings.Repeat("b", 64))
	if err != nil || result.FailedPhase != "" || m.State().ConfigRevision != 2 {
		t.Fatalf("retry after recovery: %#v %v", result, err)
	}
	if m.State().ModelDigest == first.ModelDigest {
		t.Fatal("new revision kept stale model identity")
	}
}
