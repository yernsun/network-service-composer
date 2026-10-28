package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestAgentSnapshotRejectionPreservesConfirmedState(t *testing.T) {
	for _, phase := range []string{"input", "decode", "identity", "render", "validate"} {
		t.Run(phase, func(t *testing.T) {
			a, _ := agentStreamFixture(t)
			candidate := model.Snapshot{PoolID: "pool_test", Revision: 1, ChallengeUpstream: "http://127.0.0.1:1"}
			if phase == "identity" {
				candidate.PoolID = "foreign"
			}
			if phase == "render" {
				candidate.ChallengeUpstream = "file:///invalid"
			}
			encoded, err := gateway.EncodeSnapshot(candidate, 16)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "input" {
				encoded.Header.ConfigRevision = 0
			}
			if phase == "decode" {
				encoded.Header.TransportDigest = "invalid"
			}
			if phase == "validate" {
				if err = os.WriteFile(a.config.NGINX.Binary, []byte("#!/bin/sh\nprintf '%s' '"+strings.Repeat("x", 3000)+"'\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			previous, err := os.Readlink(filepath.Join(a.generations.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			out := make(chan *gatewayv2.AgentMessage, 1)
			a.applySnapshot(context.Background(), &pendingSnapshot{header: encoded.Header, chunks: encoded.Chunks}, out)
			select {
			case m := <-out:
				nack := m.GetNack()
				if nack == nil || nack.FailedPhase != phase || nack.ConfigRevision != encoded.Header.ConfigRevision || nack.ModelDigest != encoded.Header.ModelDigest || nack.ObservedAtUnixNano <= 0 {
					t.Fatalf("uncorrelated rejection: %v", m)
				}
				if len(nack.Error) == 0 || len(nack.Error) > 2048 {
					t.Fatalf("invalid error bound: %d", len(nack.Error))
				}
			default:
				t.Fatal("rejection not reported")
			}
			current, err := os.Readlink(filepath.Join(a.generations.root, "current"))
			if err != nil || current != previous || a.applied.Load() != 0 || a.generations.State().ConfigRevision != 0 {
				t.Fatalf("rejected candidate changed running state: %s %v", current, err)
			}
		})
	}
}

func TestAgentStreamAssemblesChunksBeforeRejection(t *testing.T) {
	a, s := agentStreamFixture(t)
	encoded, err := gateway.EncodeSnapshot(model.Snapshot{PoolID: "foreign", Revision: 1, ChallengeUpstream: "http://127.0.0.1:1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.runStream(ctx, agentBoundaryClient{stream: s}) }()
	select {
	case m := <-s.output:
		if m.GetHello() == nil {
			t.Fatal("hello missing")
		}
	case <-ctx.Done():
		t.Fatal("handshake timeout")
	}
	s.input <- receivedControllerMessage{message: headerMessage(encoded.Header)}
	for _, chunk := range encoded.Chunks {
		s.input <- receivedControllerMessage{message: chunkMessage(chunk)}
	}
	select {
	case m := <-s.output:
		if m.GetNack() == nil || m.GetNack().FailedPhase != "identity" {
			t.Fatalf("complete snapshot rejection: %v", m)
		}
	case <-ctx.Done():
		t.Fatal("NACK missing")
	}
	// A handled NACK finishes this transfer and releases the pending slot.
	s.input <- receivedControllerMessage{message: headerMessage(encoded.Header)}
	for _, chunk := range encoded.Chunks {
		s.input <- receivedControllerMessage{message: chunkMessage(chunk)}
	}
	select {
	case m := <-s.output:
		if m.GetNack() == nil {
			t.Fatalf("second transfer not handled: %v", m)
		}
	case <-ctx.Done():
		t.Fatal("second NACK missing")
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancellation: %v", err)
	}
}

func TestAgentSandboxCancellationCannotActivateProduction(t *testing.T) {
	a, s := agentStreamFixture(t)
	a.config.Agent.Mode = "sandbox"
	s.input <- receivedControllerMessage{message: validationMessage(&gatewayv2.ValidationRequest{TaskId: "unknown", Cancel: true})}
	// An empty probe plan must still be refused after the cancellation request.
	s.input <- receivedControllerMessage{message: planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test"})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.runStream(ctx, agentBoundaryClient{stream: s}); err == nil || !strings.Contains(err.Error(), "production command") {
		t.Fatalf("cancel path escaped sandbox role: %v", err)
	}
	if a.generations.State().ConfigRevision != 0 {
		t.Fatal("sandbox cancellation changed production")
	}
}

func TestAgentQueueCancellationAndBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := enqueue(ctx, make(chan *gatewayv2.AgentMessage), &gatewayv2.AgentMessage{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked send ignored cancellation: %v", err)
	}
	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("backoff ignored cancellation: %v", err)
	}
	if err := sleep(context.Background(), time.Nanosecond); err != nil {
		t.Fatalf("completed backoff: %v", err)
	}
}
