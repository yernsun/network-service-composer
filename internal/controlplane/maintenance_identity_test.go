package controlplane

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMaintenanceIdentityCannotForwardOrClaimControllerReadiness(t *testing.T) {
	uri, err := MaintenanceIdentityURI("install-one", "maintenance-one")
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	installation, maintenance, err := MaintenanceCertificateIdentity(cert)
	if err != nil || installation != "install-one" || maintenance != "maintenance-one" {
		t.Fatal(installation, maintenance, err)
	}
	if _, err = CertificateIdentity(cert); err == nil {
		t.Fatal("maintenance identity became a Controller identity")
	}
	forward := Forwarder{}
	for _, handler := range []http.Handler{forward.Internal(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("maintenance identity forwarded a public mutation") })), forward.ReadyHandler(func(int64) bool { t.Fatal("maintenance identity reached scheduling readiness"); return true })} {
		request := httptest.NewRequest("GET", ReadyPath, nil)
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		if reply.Code < 400 {
			t.Fatal("maintenance obtained Controller permissions")
		}
	}
	for _, value := range []*x509.Certificate{nil, {URIs: nil}, {URIs: []*url.URL{uri, uri}}, {URIs: []*url.URL{{Scheme: "https", Host: "nsc.local", Path: uri.Path}}}, {URIs: []*url.URL{{Scheme: "spiffe", Host: "nsc.local", Path: "/installations//maintenance/"}}}, {URIs: []*url.URL{{Scheme: "spiffe", Host: "nsc.local", Path: "/wrong"}}}} {
		if _, _, err = MaintenanceCertificateIdentity(value); err == nil {
			t.Fatal("malformed maintenance identity accepted")
		}
	}
	if _, err = MaintenanceIdentityURI("bad/installation", "valid"); err == nil {
		t.Fatal("invalid installation accepted")
	}
	if _, err = MaintenanceIdentityURI("valid", "bad/maintenance"); err == nil {
		t.Fatal("invalid maintenance identity accepted")
	}
}
