package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/controlplane"
)

func (c *Controller) configureInstallation() {
	if c.config.Controller.Installation.MetadataDirectory == "" {
		return
	}
	engine := maintenance.NewLocalEngine()
	c.core.Installation = &maintenance.Provider{Repository: maintenance.Repository{Store: c.store}, Engine: engine, Directory: c.config.Controller.Installation.MetadataDirectory, Runtime: func(record *bootstrap.Record) maintenance.Runtime {
		return &maintenance.DockerRuntime{Engine: engine, Record: record, Schema: maintenance.SchemaVerifier(c.store), Physical: c.core.InstallationBinding, Health: c.core.InstallationHealth}
	}}
}
func (c *Controller) installationHealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		installation, maintenanceID, err := controlplane.MaintenanceCertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		document, err := c.store.GetControlDocument(r.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var value bootstrap.Installation
		if json.Unmarshal(document.Payload, &value) != nil || document.Kind != bootstrap.InstallationDocumentKind || document.ParentID != value.ID || bootstrap.ValidateInstallation(value) != nil || value.ID != installation || value.MaintenanceID != maintenanceID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		chain := r.TLS.VerifiedChains[0]
		sum := sha256.Sum256(chain[len(chain)-1].Raw)
		if hex.EncodeToString(sum[:]) != value.TrustSHA256 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if len(r.URL.Query()) > 0 && (len(r.URL.Query()) != 1 || r.URL.Query().Get("scope") != "identity") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err = c.core.InstallationBinding(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Query().Get("scope") == "identity" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err = c.core.InstallationHealth(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
