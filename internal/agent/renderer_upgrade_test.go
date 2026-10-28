package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
)

func TestRendererUpgradeRejectsChangedOrReplayedModels(t *testing.T) {
	digest := strings.Repeat("a", 64)
	old := GenerationState{ConfigRevision: 7, ModelDigest: digest, RendererVersion: "previous-renderer"}
	if !old.acceptsSnapshotRevision(7, digest) {
		t.Fatal("identical upgrade denied")
	}
	for _, v := range []struct {
		revision int64
		digest   string
	}{{6, digest}, {7, strings.Repeat("b", 64)}, {7, ""}} {
		if old.acceptsSnapshotRevision(v.revision, v.digest) {
			t.Fatal("unsafe replay accepted", v)
		}
	}
	old.RendererVersion = nginxx.RendererVersion
	if old.acceptsSnapshotRevision(7, digest) {
		t.Fatal("ordinary replay accepted")
	}
	if !old.acceptsSnapshotRevision(8, digest) {
		t.Fatal("new revision denied")
	}
}

func TestRendererUpgradeHandshakeRequestsModelWithoutFalseEvidence(t *testing.T) {
	a, s := agentStreamFixture(t)
	digest := strings.Repeat("a", 64)
	a.generations.mu.Lock()
	a.generations.state = GenerationState{PoolID: "pool_test", ConfigRevision: 7, ModelDigest: digest, RenderedDigest: strings.Repeat("b", 64), RendererVersion: "previous-renderer"}
	a.generations.mu.Unlock()
	a.config.Agent.HeartbeatInterval = config.Duration(5 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.runStream(ctx, agentBoundaryClient{stream: s}) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("stream did not stop")
		}
	}()
	select {
	case msg := <-s.output:
		h := msg.GetHello()
		if h == nil || h.AppliedConfigRevision != 0 || h.AppliedModelDigest != "" || h.RenderedDigest != "" || h.RendererVersion != nginxx.RendererVersion {
			t.Fatal("false upgrade evidence", msg)
		}
	case <-ctx.Done():
		t.Fatal("missing upgrade handshake")
	}
	select {
	case msg := <-s.output:
		t.Fatal("old renderer evidence escaped", msg)
	case <-time.After(30 * time.Millisecond):
	}
	state := a.generations.State()
	if state.ConfigRevision != 7 || state.ModelDigest != digest {
		t.Fatal("serving state discarded", state)
	}
}
