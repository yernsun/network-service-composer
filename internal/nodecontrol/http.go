package nodecontrol

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type BeginRequest struct {
	NodeID   string `json:"node_id"`
	EngineID string `json:"engine_id"`
	BootID   string `json:"boot_id"`
	Owner    int64  `json:"owner"`
}

// Handler is mounted only on the Controller's mutual-TLS listener. Reports are
// per-node writes, not scheduling decisions, and are accepted by every instance.
func (s Reports) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		instance, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if _, ok := s.Bindings[instance]; !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		decode := func(v any) bool {
			if err := decoder.Decode(v); err != nil {
				return false
			}
			var extra any
			return errors.Is(decoder.Decode(&extra), io.EOF)
		}
		switch r.URL.Path {
		case "/internal/v1/node-sessions":
			var v BeginRequest
			if !decode(&v) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			result, err := s.Begin(r.Context(), instance, v.NodeID, v.EngineID, v.BootID, v.Owner)
			if err != nil {
				reportError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		case "/internal/v1/node-observations":
			var v Report
			if !decode(&v) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := s.Accept(r.Context(), instance, v); err != nil {
				reportError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/internal/v1/node-events":
			var v EventBatch
			if !decode(&v) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := s.AcceptEvents(r.Context(), instance, v); err != nil {
				reportError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
}

func reportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrIdentity):
		w.WriteHeader(http.StatusForbidden)
	case errors.Is(err, ErrStale), errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrPrecondition):
		w.WriteHeader(http.StatusConflict)
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}
