package controlplane

import (
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
)

// Maintenance credentials have a separate identity namespace. They are admitted
// only by the typed installation health handler, never by forwarding or node RPCs.
func MaintenanceIdentityURI(installation, maintenance string) (*url.URL, error) {
	if _, err := IdentityURI(installation); err != nil {
		return nil, err
	}
	if _, err := IdentityURI(maintenance); err != nil {
		return nil, err
	}
	return url.Parse("spiffe://nsc.local/installations/" + installation + "/maintenance/" + maintenance)
}
func MaintenanceCertificateIdentity(cert *x509.Certificate) (string, string, error) {
	if cert == nil || len(cert.URIs) != 1 {
		return "", "", errors.New("maintenance certificate requires one identity URI")
	}
	uri := cert.URIs[0]
	parts := strings.Split(uri.Path, "/")
	if len(parts) != 5 || parts[1] != "installations" || parts[3] != "maintenance" {
		return "", "", errors.New("invalid maintenance certificate identity")
	}
	expected, err := MaintenanceIdentityURI(parts[2], parts[4])
	if err != nil || expected.String() != uri.String() {
		return "", "", errors.New("invalid maintenance certificate identity")
	}
	return parts[2], parts[4], nil
}
