package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

func TestHTTPProbe(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network namespace does not permit listeners: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.Listener = listener
	server.Start()
	defer server.Close()
	address := server.Listener.Addr().(*net.TCPAddr)
	success, err := probe(context.Background(), &gatewayv2.ProbeTarget{DnsName: address.IP.String(), Port: int32(address.Port), Mode: "http", Path: "/ready", TimeoutNanos: int64(time.Second)})
	if err != nil || !success {
		t.Fatalf("expected success, got success=%v err=%v", success, err)
	}
}

func TestTCPProbeFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network namespace does not permit listeners: %v", err)
	}
	address := listener.Addr().(*net.TCPAddr)
	_ = listener.Close()
	if success, _ := probe(context.Background(), &gatewayv2.ProbeTarget{DnsName: address.IP.String(), Port: int32(address.Port), Mode: "tcp", TimeoutNanos: int64(100 * time.Millisecond)}); success {
		t.Fatal("expected closed TCP port to fail")
	}
}

func TestSupersededProbeGenerationCannotReport(t *testing.T) {
	outgoing := make(chan *gatewayv2.AgentMessage, 1)
	runner := NewProbeRunner(1, outgoing)
	runner.mu.Lock()
	runner.epoch = 7
	runner.generation = 2
	runner.mu.Unlock()
	runner.runOnce(context.Background(), "pool", 7, 11, 1, &gatewayv2.ProbeTarget{ServiceId: "service", DnsName: "127.0.0.1", Port: 1, Mode: "tcp", TimeoutNanos: int64(time.Millisecond)})
	select {
	case message := <-outgoing:
		t.Fatalf("superseded generation reported a result: %#v", message)
	default:
	}
}

func TestProbeRunnerRepeatsPlan(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network namespace does not permit listeners: %v", err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	outgoing := make(chan *gatewayv2.AgentMessage, 4)
	runner := NewProbeRunner(1, outgoing)
	defer runner.Stop()
	err = runner.SetPlan(context.Background(), &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 3, ProbeRevision: 7, Targets: []*gatewayv2.ProbeTarget{{
		ServiceId: "service", DnsName: "localhost", Port: int32(address.Port), Mode: "tcp",
		IntervalNanos: int64(100 * time.Millisecond), TimeoutNanos: int64(100 * time.Millisecond), SuccessThreshold: 1,
		FailureThreshold: 1, TargetToken: strings.Repeat("a", 64),
	}}})
	if err != nil {
		t.Fatalf("set valid probe plan: %v", err)
	}
	for sequence := int64(1); sequence <= 2; sequence++ {
		select {
		case message := <-outgoing:
			result := message.GetProbeResult()
			if result == nil || !result.Success || result.Sequence != sequence {
				t.Fatalf("probe result %d = %#v", sequence, result)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for probe result %d", sequence)
		}
	}
}

func TestProbeRunnerRejectsInvalidAndRegressingPlansWithoutReplacingCurrent(t *testing.T) {
	runner := NewProbeRunner(1, make(chan *gatewayv2.AgentMessage, 1))
	defer runner.Stop()
	current := &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 4, ProbeRevision: 9}
	if err := runner.SetPlan(context.Background(), current); err != nil {
		t.Fatalf("set current plan: %v", err)
	}
	runner.mu.Lock()
	generation := runner.generation
	runner.mu.Unlock()

	invalid := &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 4, ProbeRevision: 10, Targets: []*gatewayv2.ProbeTarget{{
		ServiceId: "svc_test", DnsName: "127.0.0.1", Port: 80, Mode: "tcp", IntervalNanos: int64(time.Second),
		TimeoutNanos: int64(time.Second), SuccessThreshold: 1, FailureThreshold: 1, TargetToken: strings.Repeat("a", 64),
	}}}
	if err := runner.SetPlan(context.Background(), invalid); err == nil {
		t.Fatal("expected an IP probe target to be rejected")
	}
	if err := runner.SetPlan(context.Background(), &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 4, ProbeRevision: 8}); err == nil {
		t.Fatal("expected a regressing revision to be rejected")
	}
	if err := runner.SetPlan(context.Background(), &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 3, ProbeRevision: 10}); err == nil {
		t.Fatal("expected a regressing epoch to be rejected")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.epoch != 4 || runner.revision != 9 || runner.generation != generation {
		t.Fatalf("rejected plan replaced current state: epoch=%d revision=%d generation=%d", runner.epoch, runner.revision, runner.generation)
	}
}

func TestProbeRunnerRequiresRevisionForContentChange(t *testing.T) {
	runner := NewProbeRunner(1, make(chan *gatewayv2.AgentMessage, 1))
	defer runner.Stop()
	plan := &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 2, ProbeRevision: 3}
	if err := runner.SetPlan(context.Background(), plan); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	if err := runner.SetPlan(context.Background(), plan); err != nil {
		t.Fatalf("replay identical plan: %v", err)
	}
	changed := &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 2, ProbeRevision: 3, Targets: []*gatewayv2.ProbeTarget{{
		ServiceId: "svc_test", DnsName: "demo_web", Port: 80, Mode: "tcp", IntervalNanos: int64(time.Second),
		TimeoutNanos: int64(time.Second), SuccessThreshold: 1, FailureThreshold: 1, TargetToken: strings.Repeat("b", 64),
	}}}
	if err := runner.SetPlan(context.Background(), changed); err == nil {
		t.Fatal("expected content change without a revision to be rejected")
	}
}
