package agent

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/config"
	"testing"
	"time"
)

func TestAgentHeartbeatCannotAdvertiseAnUnacknowledgedGeneration(t *testing.T) {
	a, s := agentStreamFixture(t)
	a.config.Agent.HeartbeatInterval = config.Duration(5 * time.Millisecond)
	// Hold the apply boundary: observations must remain unknown and perform no network I/O.
	a.generations.operationMu.Lock()
	defer a.generations.operationMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.runStream(ctx, agentBoundaryClient{stream: s}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("stream cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("stream did not stop")
		}
	}()
	select {
	case m := <-s.output:
		if m.GetHello() == nil {
			t.Fatal("missing initial identity")
		}
	case <-ctx.Done():
		t.Fatal("handshake timed out")
	}
	select {
	case m := <-s.output:
		h := m.GetHeartbeat()
		if h == nil || h.AppliedConfigRevision != 0 || h.LoadingConfirmed || h.StartupState != "UNKNOWN" || h.RefreshIntervalMillis != 5 {
			t.Fatalf("unknown evidence misrepresented: %v", m)
		}
	case <-ctx.Done():
		t.Fatal("heartbeat missing")
	}
	a.generations.mu.Lock()
	a.generations.state.ConfigRevision = 9
	a.generations.mu.Unlock()
	// Allow already queued old evidence, but never emit the new revision without an ACK.
	timer := time.NewTimer(40 * time.Millisecond)
	defer timer.Stop()
loop:
	for {
		select {
		case m := <-s.output:
			if h := m.GetHeartbeat(); h == nil || h.AppliedConfigRevision != 0 {
				t.Fatalf("unacknowledged revision escaped: %v", m)
			}
		case <-timer.C:
			break loop
		case <-ctx.Done():
			t.Fatal("stream stalled")
		}
	}
	a.generations.mu.Lock()
	a.generations.state.ConfigRevision = 0
	a.generations.mu.Unlock()
	select {
	case m := <-s.output:
		if h := m.GetHeartbeat(); h == nil || h.AppliedConfigRevision != 0 {
			t.Fatalf("heartbeat did not resume: %v", m)
		}
	case <-ctx.Done():
		t.Fatal("heartbeat did not resume")
	}
}
