package engine

import (
	"context"
	"errors"
	ct "github.com/moby/moby/api/types/container"
	et "github.com/moby/moby/api/types/events"
	nt "github.com/moby/moby/api/types/network"
	"github.com/yernsun/network-service-composer/internal/model"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestComposeSnapshotKeepsIncompleteReplicaEvidenceVisible(t *testing.T) {
	for _, mode := range []string{"missing project", "missing service", "oneoff", "missing state", "bad start", "zero start", "valid start", "no network settings", "missing network", "IPv6", "mapped IPv4", "no address", "missing alias", "bad labels", "different labels", "different ports", "inspect failure"} {
		t.Run(mode, func(t *testing.T) {
			client := newFakeDockerClient()
			labels := map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "api", "nsc.port": "8080"}
			inspect := composeInspectWithPort("10.42.0.2", 8080)
			switch mode {
			case "missing project":
				delete(labels, "com.docker.compose.project")
			case "missing service":
				delete(labels, "com.docker.compose.service")
			case "oneoff":
				labels["com.docker.compose.oneoff"] = "True"
			case "missing state":
				inspect.State = nil
			case "bad start":
				inspect.State.StartedAt = "invalid"
			case "zero start":
				inspect.State.StartedAt = "0001-01-01T00:00:00Z"
			case "valid start":
				inspect.State.StartedAt = "2026-01-01T01:00:00+01:00"
			case "no network settings":
				inspect.NetworkSettings = nil
			case "missing network":
				inspect.NetworkSettings.Networks = map[string]*nt.EndpointSettings{}
			case "IPv6":
				inspect.NetworkSettings.Networks["nsc-ingress"].IPAddress = netip.Addr{}
				inspect.NetworkSettings.Networks["nsc-ingress"].GlobalIPv6Address = netip.MustParseAddr("2001:db8::1")
			case "mapped IPv4":
				inspect.NetworkSettings.Networks["nsc-ingress"].IPAddress = netip.MustParseAddr("::ffff:192.0.2.1")
			case "no address":
				inspect.NetworkSettings.Networks["nsc-ingress"].IPAddress = netip.Addr{}
			case "missing alias":
				inspect.NetworkSettings.Networks["nsc-ingress"].Aliases = nil
			case "bad labels":
				labels["nsc.enable"] = "true"
				labels["nsc.port"] = "invalid"
			case "different ports":
				delete(labels, "nsc.port")
			case "inspect failure":
				client.inspectErrs["a"] = errors.New("inspection unavailable")
			}
			client.containers = []ct.Summary{{ID: "a", State: ct.StateRunning, Labels: labels}}
			client.inspections["a"] = inspect
			if mode == "different labels" || mode == "different ports" {
				other := map[string]string{}
				for k, v := range labels {
					other[k] = v
				}
				if mode == "different labels" {
					other["nsc.port"] = "9090"
				}
				client.containers = append(client.containers, ct.Summary{ID: "b", State: ct.StateRunning, Labels: other})
				client.inspections["b"] = composeInspectWithPort("10.42.0.3", 9090)
			}
			p := NewWithClient(client, model.Runtime{ID: "runtime", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
			snapshot, e := p.Snapshot(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if mode == "oneoff" || strings.HasPrefix(mode, "missing project") || mode == "missing service" {
				if len(snapshot.Services) != 0 {
					t.Fatal("unowned container became service")
				}
				return
			}
			if len(snapshot.Services) != 1 {
				t.Fatal("logical service identity lost", snapshot)
			}
			s := snapshot.Services[0]
			switch mode {
			case "no network settings", "missing network", "no address", "missing alias", "inspect failure":
				if s.RoutingEligible || s.DNSStatus == model.DNSReady {
					t.Fatal("incomplete evidence became ready", s)
				}
			case "different ports", "different labels":
				if s.RoutingEligible || s.ConfigurationError == "" || s.DesiredInstances != 2 {
					t.Fatal("replica disagreement hidden", s)
				}
			case "bad labels":
				if len(s.RoutingIssues) == 0 {
					t.Fatal("invalid labels hidden")
				}
			default:
				if !s.RoutingEligible || len(s.Endpoints) != 1 {
					t.Fatal("valid evidence lost", s)
				}
			}
			if mode == "valid start" && (s.Endpoints[0].StartedAt == nil || !s.Endpoints[0].StartedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))) {
				t.Fatal("start time normalization lost")
			}
			if (mode == "bad start" || mode == "zero start" || mode == "missing state") && s.Endpoints[0].StartedAt != nil {
				t.Fatal("unknown start time guessed")
			}
			if mode == "IPv6" && s.Endpoints[0].Address != "2001:db8::1" {
				t.Fatal("IPv6 target lost")
			}
			if mode == "mapped IPv4" && s.Endpoints[0].Address != "192.0.2.1" {
				t.Fatal("mapped IPv4 target not canonical")
			}
		})
	}
}
func TestDiscoveryWatchCancellationClosesBackpressuredStream(t *testing.T) {
	for _, mode := range []string{"closed events", "closed errors", "nil error", "error", "canceled", "backpressure"} {
		t.Run(mode, func(t *testing.T) {
			client := newFakeDockerClient()
			client.events = make(chan et.Message, 40)
			p := NewWithClient(client, model.Runtime{ID: "runtime", Type: model.RuntimeDocker}, "network", providerReadiness())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			signals, errs := p.Watch(ctx)
			failure := errors.New("event connection lost")
			switch mode {
			case "closed events":
				close(client.events)
			case "closed errors":
				close(client.eventErrors)
			case "nil error":
				client.eventErrors <- nil
			case "error":
				client.eventErrors <- failure
			case "canceled":
				cancel()
			case "backpressure":
				for i := 0; i < 40; i++ {
					client.events <- et.Message{Action: et.Action("start"), Actor: et.Actor{ID: "container"}, Time: 17}
				}
				deadline := time.Now().Add(time.Second)
				for len(signals) < 32 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if len(signals) != 32 {
					t.Fatal("fixture did not produce backpressure")
				}
				cancel()
			}
			for {
				select {
				case _, ok := <-errs:
					if !ok {
						goto closed
					}
				case <-time.After(2 * time.Second):
					t.Fatal("watch did not release after termination")
				}
			}
		closed:
			for s := range signals {
				if s.RuntimeID != "runtime" || s.ExternalID != "container" || s.Kind != "start" || !s.At.Equal(time.Unix(17, 0)) {
					t.Fatal("event identity changed", s)
				}
			}
			// Error channel values are validated separately to avoid discarding the error
			// while waiting for signal shutdown.
			if mode == "error" {
				client2 := newFakeDockerClient()
				p2 := NewWithClient(client2, model.Runtime{}, "network", providerReadiness())
				c2, x := context.WithCancel(context.Background())
				defer x()
				_, e2 := p2.Watch(c2)
				client2.eventErrors <- failure
				select {
				case e := <-e2:
					if !errors.Is(e, failure) {
						t.Fatal("watch failure lost", e)
					}
				case <-time.After(time.Second):
					t.Fatal("watch error missing")
				}
			}
		})
	}
}
