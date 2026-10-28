package nodecontrol

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
)

func TestNodeReportsOverMutualTLSRejectOtherNodeAndUntrustedCA(t *testing.T) {
	s, now, owner := reportsFixture(t)
	s.EventsEnabled = true
	binding := s.Bindings["controller-a"]
	binding.ClusterID = "cluster-a"
	s.Bindings["controller-a"] = binding
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated node reporting CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	issue := func(instance string) *tls.Config {
		t.Helper()
		uri, err := controlplane.IdentityURI(instance)
		if err != nil {
			t.Fatal(err)
		}
		cert, private := filepath.Join(dir, instance+".pem"), filepath.Join(dir, instance+".key")
		if err := pki.Issue(ca, key, cert, private, pki.CertificateOptions{CommonName: instance, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
			t.Fatal(err)
		}
		value, err := controlplane.InternalTLS(ca, cert, private, instance)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	serverTLS, peerTLS := issue("controller-a"), issue("controller-b")
	server := httptest.NewUnstartedServer(s.Handler())
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	client := func(cfg *tls.Config) *http.Client {
		transport := &http.Transport{TLSClientConfig: cfg}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 5 * time.Second}
	}
	ctx := context.Background()
	begin := BeginRequest{NodeID: "node-a", EngineID: "engine-a", BootID: "boot", Owner: *owner}
	var session Session
	if err := post(ctx, client(peerTLS), server.URL+"/internal/v1/node-sessions", begin, &session); err == nil {
		t.Fatal("another trusted node claimed local ownership")
	}
	if err := post(ctx, client(serverTLS), server.URL+"/internal/v1/node-sessions", begin, &session); err != nil {
		t.Fatal(err)
	}
	report := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
	if err := post(ctx, client(serverTLS), server.URL+"/internal/v1/node-observations", report, nil); err != nil {
		t.Fatal(err)
	}
	read, err := s.Read(ctx, "node-a")
	if err != nil || read.Sequence != 1 || len(read.Observation.Containers) != 1 {
		t.Fatal("authenticated report not persisted", err)
	}
	if err := post(ctx, client(peerTLS), server.URL+"/internal/v1/node-observations", report, nil); err == nil {
		t.Fatal("trusted foreign node replaced observation")
	}
	batch := EventBatch{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, At: *now}
	if err := post(ctx, client(serverTLS), server.URL+"/internal/v1/node-events", batch, nil); err != nil {
		t.Fatal(err)
	}
	if err := post(ctx, client(peerTLS), server.URL+"/internal/v1/node-events", batch, nil); err == nil {
		t.Fatal("foreign node sent event batch")
	}
	*owner = 12
	if err := post(ctx, client(serverTLS), server.URL+"/internal/v1/node-events", batch, nil); err == nil {
		t.Fatal("released node session sent event batch")
	}
	*owner = 11
	noCertificate := serverTLS.Clone()
	noCertificate.Certificates = nil
	if err := post(ctx, client(noCertificate), server.URL+"/internal/v1/node-observations", report, nil); err == nil {
		t.Fatal("client without certificate reached internal endpoint")
	}
	// A separately issued CA cannot authenticate a same-named Controller.
	ca, key = filepath.Join(dir, "other-ca.pem"), filepath.Join(dir, "other-ca.key")
	if err := pki.InitCA(ca, key, "untrusted CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	foreign := issue("controller-a")
	foreign.RootCAs = serverTLS.RootCAs
	if err := post(ctx, client(foreign), server.URL+"/internal/v1/node-observations", report, nil); err == nil {
		t.Fatal("untrusted CA authenticated a Controller")
	}
}
