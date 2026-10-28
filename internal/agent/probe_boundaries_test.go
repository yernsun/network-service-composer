package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"google.golang.org/protobuf/proto"
)

func boundaryProbePlan() *gatewayv2.ProbePlan {
	return &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 3, ProbeRevision: 7, Targets: []*gatewayv2.ProbeTarget{{ServiceId: "service", DnsName: "project_api", Port: 8080, Mode: "tcp", IntervalNanos: int64(time.Second), TimeoutNanos: int64(time.Second), SuccessThreshold: 1, FailureThreshold: 1, TargetToken: strings.Repeat("a", 64)}}}
}

func TestProbePlanRejectsUnsafeTargetsWithoutChangingActivePlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*gatewayv2.ProbePlan)
	}{
		{"empty pool", "pool identity", func(p *gatewayv2.ProbePlan) { p.PoolId = "" }},
		{"long pool", "pool identity", func(p *gatewayv2.ProbePlan) { p.PoolId = strings.Repeat("p", 129) }},
		{"control identity", "pool identity", func(p *gatewayv2.ProbePlan) { p.PoolId = "pool\x00" }},
		{"space identity", "pool identity", func(p *gatewayv2.ProbePlan) { p.PoolId = "pool name" }},
		{"negative epoch", "negative", func(p *gatewayv2.ProbePlan) { p.Epoch = -1 }},
		{"negative revision", "negative", func(p *gatewayv2.ProbePlan) { p.ProbeRevision = -1 }},
		{"too many targets", "too many", func(p *gatewayv2.ProbePlan) { p.Targets = make([]*gatewayv2.ProbeTarget, 10001) }},
		{"nil target normalized by protobuf clone", "service identity", func(p *gatewayv2.ProbePlan) { p.Targets[0] = nil }},
		{"empty service", "service identity", func(p *gatewayv2.ProbePlan) { p.Targets[0].ServiceId = "" }},
		{"duplicate service", "duplicate", func(p *gatewayv2.ProbePlan) {
			p.Targets = append(p.Targets, proto.Clone(p.Targets[0]).(*gatewayv2.ProbeTarget))
		}},
		{"zero port", "port", func(p *gatewayv2.ProbePlan) { p.Targets[0].Port = 0 }},
		{"large port", "port", func(p *gatewayv2.ProbePlan) { p.Targets[0].Port = 65536 }},
		{"invalid mode", "mode", func(p *gatewayv2.ProbePlan) { p.Targets[0].Mode = "exec" }},
		{"invalid scheme", "scheme", func(p *gatewayv2.ProbePlan) { p.Targets[0].Mode = "http"; p.Targets[0].Scheme = "file" }},
		{"empty HTTP path", "path", func(p *gatewayv2.ProbePlan) { p.Targets[0].Mode = "http"; p.Targets[0].Scheme = "https" }},
		{"relative HTTP path", "path", func(p *gatewayv2.ProbePlan) {
			p.Targets[0].Mode = "http"
			p.Targets[0].Scheme = "https"
			p.Targets[0].Path = "ready"
		}},
		{"unsafe HTTP path", "path", func(p *gatewayv2.ProbePlan) {
			p.Targets[0].Mode = "http"
			p.Targets[0].Scheme = "http"
			p.Targets[0].Path = "/ready?injected"
		}},
		{"fast interval", "interval", func(p *gatewayv2.ProbePlan) { p.Targets[0].IntervalNanos = int64(99 * time.Millisecond) }},
		{"slow interval", "interval", func(p *gatewayv2.ProbePlan) { p.Targets[0].IntervalNanos = int64(25 * time.Hour) }},
		{"zero timeout", "timeout", func(p *gatewayv2.ProbePlan) { p.Targets[0].TimeoutNanos = 0 }},
		{"timeout exceeds interval", "timeout", func(p *gatewayv2.ProbePlan) { p.Targets[0].TimeoutNanos = int64(2 * time.Second) }},
		{"zero success threshold", "threshold", func(p *gatewayv2.ProbePlan) { p.Targets[0].SuccessThreshold = 0 }},
		{"large success threshold", "threshold", func(p *gatewayv2.ProbePlan) { p.Targets[0].SuccessThreshold = 1000001 }},
		{"zero failure threshold", "threshold", func(p *gatewayv2.ProbePlan) { p.Targets[0].FailureThreshold = 0 }},
		{"large failure threshold", "threshold", func(p *gatewayv2.ProbePlan) { p.Targets[0].FailureThreshold = 1000001 }},
		{"short token", "token", func(p *gatewayv2.ProbePlan) { p.Targets[0].TargetToken = "a" }},
		{"invalid hex token", "token", func(p *gatewayv2.ProbePlan) { p.Targets[0].TargetToken = strings.Repeat("z", 64) }},
		{"uppercase token", "token", func(p *gatewayv2.ProbePlan) { p.Targets[0].TargetToken = strings.Repeat("A", 64) }},
		{"invalid protobuf text", "encode probe plan", func(p *gatewayv2.ProbePlan) { p.PoolId = string([]byte{255}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewProbeRunner(0, make(chan *gatewayv2.AgentMessage, 1))
			defer runner.Stop()
			current := &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 2, ProbeRevision: 6}
			if err := runner.SetPlan(context.Background(), current); err != nil {
				t.Fatal(err)
			}
			generation, digest := runner.generation, runner.planDigest
			candidate := boundaryProbePlan()
			tc.mutate(candidate)
			err := runner.SetPlan(context.Background(), candidate)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid probe plan accepted or misdiagnosed: %v", err)
			}
			if runner.epoch != 2 || runner.revision != 6 || runner.generation != generation || runner.planDigest != digest {
				t.Fatal("rejected plan replaced active worker identity")
			}
		})
	}
	if err := validateProbePlan(nil); err == nil {
		t.Fatal("nil plan accepted as validated")
	}
	missingTarget := boundaryProbePlan()
	missingTarget.Targets[0] = nil
	if err := validateProbePlan(missingTarget); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing target accepted: %v", err)
	}
	for _, name := range []string{"", strings.Repeat("a", 254), "127.0.0.1", ".api", "api.", "api..local", "bad/name"} {
		p := boundaryProbePlan()
		p.Targets[0].DnsName = name
		if err := validateProbePlan(p); err == nil || !strings.Contains(err.Error(), "DNS name") {
			t.Fatalf("unsafe target %q accepted: %v", name, err)
		}
	}
	for _, path := range []string{"/control\x00", "/space here"} {
		if safeProbePath(path) {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	for _, mode := range []string{"tcp", " HTTP "} {
		for _, scheme := range []string{"http", "https"} {
			p := boundaryProbePlan()
			p.Targets[0].DnsName = "Az09-_x.local"
			p.Targets[0].Mode = mode
			p.Targets[0].Scheme = scheme
			p.Targets[0].Path = "/ready"
			p.Targets[0].IntervalNanos = int64(24 * time.Hour)
			p.Targets[0].TimeoutNanos = int64(24 * time.Hour)
			p.Targets[0].SuccessThreshold = 1000000
			p.Targets[0].FailureThreshold = 1000000
			if err := validateProbePlan(p); err != nil {
				t.Fatalf("supported boundary rejected: %v", err)
			}
		}
	}
}

func TestProbePlanStopAndEpochFencing(t *testing.T) {
	r := NewProbeRunner(1, make(chan *gatewayv2.AgentMessage, 1))
	defer r.Stop()
	ctx := context.Background()
	if err := r.SetPlan(ctx, &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 4, ProbeRevision: 9}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPlan(ctx, &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 5, ProbeRevision: 8}); err == nil {
		t.Fatal("higher epoch bypassed revision monotonicity")
	}
	if err := r.SetPlan(ctx, &gatewayv2.ProbePlan{PoolId: "pool", Epoch: 4, ProbeRevision: 10}); err != nil {
		t.Fatal(err)
	}
	before := r.generation
	if err := r.SetPlan(ctx, nil); err != nil || r.cancel != nil || r.generation <= before {
		t.Fatal("nil plan did not stop and fence workers")
	}
	r.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.workers <- struct{}{}
	r.runOnce(ctx, "pool", 4, 10, r.generation, boundaryProbePlan().Targets[0])
	<-r.workers
	if len(r.sequences) != 0 {
		t.Fatal("cancelled worker produced an observation")
	}
}

func TestProbeHTTPStatusesRedirectsAndTransportFailures(t *testing.T) {
	for _, status := range []int{200, 204, 302, 399, 400, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/" {
					t.Errorf("unexpected default probe path %s", r.URL.Path)
				}
				w.Header().Set("Location", "http://127.0.0.1:1/never-follow")
				w.WriteHeader(status)
			}))
			defer server.Close()
			address := server.Listener.Addr().(*net.TCPAddr)
			success, err := probe(context.Background(), &gatewayv2.ProbeTarget{Mode: "http", DnsName: address.IP.String(), Port: int32(address.Port)})
			want := status < 400
			if success != want || (err == nil) != want || calls != 1 {
				t.Fatalf("HTTP probe=%t %v calls=%d, want success=%t", success, err, calls, want)
			}
		})
	}
	for _, target := range []*gatewayv2.ProbeTarget{
		{Mode: "http", Scheme: "file"}, {Mode: "http", Scheme: "http", DnsName: "invalid\nheader", Port: 80}, {Mode: "exec"},
	} {
		success, err := probe(context.Background(), target)
		if success || err == nil {
			t.Fatalf("unsupported probe reported success: %t %v", success, err)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	address := server.Listener.Addr().(*net.TCPAddr)
	if success, err := probe(context.Background(), &gatewayv2.ProbeTarget{Mode: "http", Scheme: "https", DnsName: address.IP.String(), Port: int32(address.Port), Path: "/ready", TimeoutNanos: int64(time.Second)}); success || err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if success, err := probe(ctx, &gatewayv2.ProbeTarget{Mode: "http", DnsName: address.IP.String(), Port: int32(address.Port)}); success || err == nil {
		t.Fatal("cancelled HTTP probe accepted")
	}
}

func TestProbeObservationsCarryIdentitySequenceAndBoundedErrors(t *testing.T) {
	out := make(chan *gatewayv2.AgentMessage, 3)
	r := NewProbeRunner(1, out)
	r.epoch = 3
	r.generation = 7
	for sequence := int64(1); sequence <= 2; sequence++ {
		target := &gatewayv2.ProbeTarget{ServiceId: "service", Mode: strings.Repeat("x", 2000), TargetToken: strings.Repeat("a", 64)}
		r.runOnce(context.Background(), "pool", 3, 9, 7, target)
		result := (<-out).GetProbeResult()
		if result.Success || len(result.Error) != 1024 || result.Sequence != sequence || result.Epoch != 3 || result.ProbeRevision != 9 || result.PoolId != "pool" || result.TargetToken != target.TargetToken || result.ObservedAtUnixNano == 0 {
			t.Fatalf("probe failure evidence changed: %#v", result)
		}
	}
	r.runOnce(context.Background(), "pool", 2, 9, 7, &gatewayv2.ProbeTarget{ServiceId: "service", Mode: "unsupported"})
	if len(out) != 0 {
		t.Fatal("obsolete epoch reported observation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.outgoing = nil
	r.runOnce(ctx, "pool", 3, 9, 7, &gatewayv2.ProbeTarget{ServiceId: "service", Mode: "unsupported"})
	// A stopped runner must release a blocked delivery and worker slot.
	if len(r.workers) != 0 {
		t.Fatal("cancelled delivery retained worker capacity")
	}
}
