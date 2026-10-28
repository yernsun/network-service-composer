package agent

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/model"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgentStreamAcknowledgesBeforeAdvertisingNewGeneration(t *testing.T) {
	if !insideAgentNetworkFixture(t) {
		return
	}
	a, stream := agentStreamFixture(t)
	a.config.Agent.HeartbeatInterval = config.Duration(5 * time.Millisecond)
	listener, e := net.Listen("tcp", "127.0.0.1:80")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, e := os.Readlink(filepath.Join(a.generations.root, "current"))
		if e != nil {
			w.WriteHeader(503)
			return
		}
		revision := int64(0)
		if strings.HasPrefix(target, "gen-") {
			state, e := readGenerationState(filepath.Join(a.generations.root, target, "generation.json"))
			if e != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.runStream(ctx, agentBoundaryClient{stream: stream}) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if !errors.Is(e, context.Canceled) {
				t.Error("stream failed", e)
			}
		case <-time.After(time.Second):
			t.Error("stream did not stop")
		}
	}()
	select {
	case m := <-stream.output:
		if m.GetHello() == nil {
			t.Fatal("initial identity missing")
		}
	case <-ctx.Done():
		t.Fatal("handshake stalled")
	}
	for revision := int64(1); revision <= 2; revision++ {
		encoded, e := gateway.EncodeSnapshot(model.Snapshot{PoolID: a.config.Agent.PoolID, Revision: revision, ChallengeUpstream: "http://127.0.0.1:1"}, gateway.DefaultChunkSize)
		if e != nil {
			t.Fatal(e)
		}
		stream.input <- receivedControllerMessage{message: headerMessage(encoded.Header)}
		for _, chunk := range encoded.Chunks {
			stream.input <- receivedControllerMessage{message: chunkMessage(chunk)}
		}
		acknowledged := false
	loop:
		for {
			select {
			case m := <-stream.output:
				if nack := m.GetNack(); nack != nil {
					t.Fatalf("valid generation rejected: %+v", nack)
				}
				if ack := m.GetAck(); ack != nil {
					if ack.ConfigRevision != revision || ack.ModelDigest != encoded.Header.ModelDigest || ack.RenderedDigest == "" || ack.AppliedAtUnixNano <= 0 {
						t.Fatalf("ACK lost applied identity: %+v", ack)
					}
					acknowledged = true
				}
				if heartbeat := m.GetHeartbeat(); heartbeat != nil && heartbeat.AppliedConfigRevision == revision {
					if !acknowledged {
						t.Fatal("new generation heartbeat preceded ACK")
					}
					if heartbeat.LoadingConfirmed && heartbeat.StartupState == "CONFIRMED" {
						break loop
					}
				}
			case <-ctx.Done():
				t.Fatal("acknowledged generation never obtained heartbeat evidence")
			}
		}
		if state := a.generations.State(); state.ConfigRevision != revision || a.applied.Load() != revision || state.ModelDigest != encoded.Header.ModelDigest {
			t.Fatal("ACK differs from committed local state")
		}
	}
}
