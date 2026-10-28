package composecheck

import (
	"strings"
	"testing"
)

func TestPublishedPortAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, ports, extra, code string
		swarm                    bool
	}{
		{name: "mysql ingress", ports: "[{target: 3306, published: '3306', protocol: tcp, mode: ingress}]", swarm: true},
		{name: "udp host", ports: "[{target: 53, published: '5353', protocol: udp, mode: host}]", swarm: true},
		{name: "automatic", ports: "['3306', {target: 80, published: '0'}]", swarm: true},
		{name: "short range", ports: "['8000-8002:80-82']", swarm: true},
		{name: "localhost", ports: "['127.0.0.1:3306:3306']"},
		{name: "ipv6", ports: "[{target: 3306, published: 3306, host_ip: '::1'}]"},
		{name: "ipv6 short", ports: "['[::1]:3306:3306']"},
		{name: "dynamic range standalone", ports: "['8000-8010:80']"},
		{name: "invalid short published", ports: "['99999:3306']", code: "COMPOSE_PORT_INVALID"},
		{name: "invalid long published", ports: "[{target: 3306, published: 'bogus'}]", code: "COMPOSE_PORT_INVALID"},
		{name: "invalid target", ports: "[{target: 65536, published: '3306'}]", code: "COMPOSE_PORT_INVALID"},
		{name: "zero target", ports: "['3306:0']", code: "COMPOSE_PORT_INVALID"},
		{name: "zero long target", ports: "[{target: 0, published: '3306'}]", code: "COMPOSE_PORT_INVALID"},
		{name: "bad target type", ports: "[{target: abc, published: '3306'}]", code: "COMPOSE_PORT_INVALID"},
		{name: "invalid mode", ports: "[{target: 3306, mode: invalid}]", code: "COMPOSE_PORT_INVALID"},
		{name: "invalid protocol", ports: "['3306:3306/sctp']", code: "COMPOSE_PORT_INVALID"},
		{name: "malformed protocol", ports: "[{target: 80, protocol: secret}]", code: "COMPOSE_PORT_INVALID"},
		{name: "invalid address", ports: "[{target: 3306, published: '3306', host_ip: secret}]", code: "COMPOSE_PORT_INVALID"},
		{name: "swarm address short", ports: "['127.0.0.1:3306:3306']", swarm: true, code: "SWARM_PORT_HOST_IP_UNSUPPORTED"},
		{name: "swarm address long", ports: "[{target: 3306, host_ip: '::1'}]", swarm: true, code: "SWARM_PORT_HOST_IP_UNSUPPORTED"},
		{name: "swarm dynamic range", ports: "['8000-8010:80']", swarm: true, code: "SWARM_PORT_RANGE_UNSUPPORTED"},
		{name: "swarm long range", ports: "[{target: 80, published: '8000-8010'}]", swarm: true, code: "SWARM_PORT_RANGE_UNSUPPORTED"},
		{name: "host network", ports: "['3306:3306']", extra: "    network_mode: host\n", code: "COMPOSE_PORT_NETWORK_CONFLICT"},
		{name: "dnsrr ingress", ports: "['3306:3306']", extra: "    deploy: {endpoint_mode: dnsrr}\n", swarm: true, code: "COMPOSE_PORT_NETWORK_CONFLICT"},
		{name: "dnsrr host", ports: "[{target: 3306, published: '3306', mode: host}]", extra: "    deploy: {endpoint_mode: dnsrr}\n", swarm: true},
		{name: "different protocols", ports: "['3306:3306', '3306:3306/udp']", swarm: true},
		{name: "same service duplicate", ports: "['3306:3306', {target: 3307, published: '3306'}]", swarm: true, code: "COMPOSE_PORT_CONFLICT"},
		{name: "different addresses", ports: "['127.0.0.1:3306:3306', '127.0.0.2:3306:3306']"},
		{name: "wildcard address", ports: "['127.0.0.1:3306:3306', '0.0.0.0:3306:3306']", code: "COMPOSE_PORT_CONFLICT"},
		{name: "ipv6 equivalent", ports: "['[::1]:3306:3306', '[0:0:0:0:0:0:0:1]:3306:3306']", code: "COMPOSE_PORT_CONFLICT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Analyze("services:\n  db:\n    image: mysql:8.4\n    ports: "+tc.ports+"\n"+tc.extra, Options{Swarm: tc.swarm, Plan: Plan{Acknowledged: []string{"COMPOSE_PORT_INVALID"}}})
			if tc.code == "" {
				if !r.Deployable {
					t.Fatalf("valid mapping rejected: %+v", r.Diagnostics)
				}
			} else if r.Deployable || !has(r, tc.code) || r.Diagnostics[0].Pointer != "/services/db/ports/0" && r.Diagnostics[0].Pointer != "/services/db/ports/1" {
				t.Fatalf("expected %s at port: %+v", tc.code, r)
			}
			for _, d := range r.Diagnostics {
				if strings.Contains(d.Reason+d.Resource, "secret") {
					t.Fatal("diagnostic leaked source values")
				}
			}
		})
	}
}

func TestCandidatePortCollisions(t *testing.T) {
	for _, tc := range []struct {
		mode1, mode2    string
		swarm, conflict bool
	}{
		{"ingress", "ingress", true, true},
		{"host", "ingress", true, true},
		{"host", "host", true, false},
		{"host", "host", false, true},
	} {
		raw := "services:\n  a:\n    image: mysql:8.4\n    ports: [{target: 3306, published: '3306', mode: " + tc.mode1 + "}]\n  b:\n    image: mysql:8.4\n    ports: [{target: 3307, published: '3306', mode: " + tc.mode2 + "}]\n"
		r := Analyze(raw, Options{Swarm: tc.swarm})
		if has(r, "COMPOSE_PORT_CONFLICT") != tc.conflict || r.Deployable == tc.conflict {
			t.Fatalf("unexpected collision result for %+v: %+v", tc, r)
		}
	}
}
