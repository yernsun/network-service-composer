package install

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestPreflightChecksEveryReservedListenerAndValidatesSocketEvidence(t *testing.T) {
	for _, port := range []int{80, 443, 8443, 9443, 9444, 22} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			i.Hosts["one"].(*fakeHost).preflight = fmt.Sprintf("660\n10485760\n00000000:%04X", port)
			_, err := i.Plan(t.Context(), bootstrap.Init)
			if port == 22 {
				if err != nil {
					t.Fatal("unrelated listener blocked", err)
				}
			} else if err == nil {
				t.Fatal("required port conflict not detected")
			}
		})
	}
	i, _ := installerFixture(t, 1)
	observed, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	o := observed.(Observation)
	host := i.Hosts["one"].(*fakeHost)
	host.runErr = fmt.Errorf("preflight sudo lost")
	if err = i.preflight(t.Context(), o); err == nil {
		t.Fatal("failed privilege preflight ignored")
	}
	host.runErr = nil
	host.preflight = "invalid-mode 10485760"
	if err = i.preflight(t.Context(), o); err == nil {
		t.Fatal("invalid permission evidence ignored")
	}
	host.preflight = ""
	o.GIDs["one"] = "invalid"
	if err = i.preflight(t.Context(), o); err == nil {
		t.Fatal("invalid socket identity accepted")
	}
	o.GIDs["one"] = "900"
	for _, password := range []string{strings.Repeat("a", 16), strings.Repeat("a", 73)} {
		i.Config.AdminPasswordFile = filepath.Join(t.TempDir(), "password")
		os.WriteFile(i.Config.AdminPasswordFile, []byte(password), 0600)
		err = i.preflight(t.Context(), o)
		if len(password) == 16 {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("oversized administrator password accepted")
		}
	}
}

func TestExistingSwarmPortClaimsBlockBeforeProvisioning(t *testing.T) {
	i, c := installerFixture(t, 1)
	h := i.Hosts["one"].(*fakeHost)
	h.active = true
	h.nodeID = "node-one"
	c.services["external"] = swarm.Service{ID: "external", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "external"}, EndpointSpec: &swarm.EndpointSpec{Ports: []swarm.PortConfig{{PublishedPort: 8443}, {PublishedPort: 22}, {PublishedPort: 0}}}}}
	if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
		t.Fatal("Swarm ingress port conflict missed")
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Fatal("port preflight mutated existing Swarm")
		}
	}
}

func TestFrozenEntryMustStayAnAuthenticatedManager(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	host := i.Hosts["one"]
	i.Hosts["one"] = interceptedHost{base: host, intercept: func(ctx context.Context, m, p string, in, out any) (bool, error) {
		if p == "/info" {
			var info NodeInfo
			host.Engine(ctx, m, p, in, &info)
			info.Swarm.ControlAvailable = false
			return true, encode(info, out)
		}
		return false, nil
	}}
	if _, err := i.Observe(t.Context()); err == nil {
		t.Fatal("Worker entry accepted as Manager")
	}
	if err := i.Check(t.Context(), bootstrap.Step{Key: "handoff"}); err == nil {
		t.Fatal("non-manager continued mutation")
	}
	i.Hosts["one"] = host
	c.fail = "GET /info"
	if err := i.Check(t.Context(), bootstrap.Step{Key: "handoff"}); err == nil {
		t.Fatal("unavailable identity accepted")
	}
	if _, err := i.prepareImages(t.Context()); err == nil {
		t.Fatal("unobserved image target accepted")
	}
}

func TestListenerEvidenceIgnoresHeadersAndIncludesRealTCPAndUDP(t *testing.T) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	data, err := exec.CommandContext(t.Context(), "sh", "-c", listenerEvidenceCommand).Output()
	if err != nil {
		t.Fatal(err)
	}
	observed := map[int]bool{}
	for _, endpoint := range strings.Fields(string(data)) {
		_, port, ok := strings.Cut(endpoint, ":")
		if !ok {
			t.Fatal("kernel table header was treated as a listener")
		}
		value, err := strconv.ParseUint(port, 16, 16)
		if err != nil {
			t.Fatal("invalid listener evidence")
		}
		observed[int(value)] = true
	}
	if !observed[tcp.Addr().(*net.TCPAddr).Port] || !observed[udp.LocalAddr().(*net.UDPAddr).Port] {
		t.Fatal("a real TCP or UDP listener was omitted")
	}
}
