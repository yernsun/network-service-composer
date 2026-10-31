package pki

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func TestSelfSignedIPCalendarValidityAndExplicitTrust(t *testing.T) {
	now := time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)
	for _, names := range [][]string{nil, {"console.example.test"}} {
		certificate, key, err := SelfSigned(names, []net.IP{net.ParseIP("192.0.2.17"), net.ParseIP("2001:db8::17")}, 20, now)
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.X509KeyPair(certificate, key)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if leaf.IsCA || !leaf.NotAfter.Equal(now.AddDate(20, 0, 0)) || leaf.SerialNumber.Sign() <= 0 || len(leaf.IPAddresses) != 2 {
			t.Fatal("invalid management leaf", leaf)
		}
		if err = leaf.VerifyHostname("192.0.2.17"); err != nil {
			t.Fatal(err)
		}
		if err = leaf.VerifyHostname("2001:db8::17"); err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		roots.AddCert(leaf)
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "192.0.2.17", CurrentTime: now}); err != nil {
			t.Fatal(err)
		}
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: x509.NewCertPool(), DNSName: "192.0.2.17", CurrentTime: now}); err == nil {
			t.Fatal("self-signed leaf trusted without installation trust")
		}
	}
	for _, years := range []int{0, 101} {
		if _, _, err := SelfSigned(nil, []net.IP{net.ParseIP("192.0.2.1")}, years, now); err == nil {
			t.Fatal("invalid validity")
		}
	}
	if _, _, err := SelfSigned(nil, nil, 20, now); err == nil {
		t.Fatal("missing SAN accepted")
	}
	if _, _, err := SelfSigned(nil, []net.IP{{1, 2, 3}}, 20, now); err == nil {
		t.Fatal("invalid IP accepted")
	}
}
