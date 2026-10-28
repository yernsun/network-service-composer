package api

import (
	"net/http"
	"strings"
)

// Recovery authentication changes cookies only. Resource and webhook writes
// are rejected before handlers and audit persistence can touch the restore.
func (s *Server) recoveryReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Controller.RecoveryMode {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-NSC-Recovery-Mode", "read-only")
		read := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		authentication := r.Method == http.MethodPost && (r.URL.Path == "/login" || r.URL.Path == "/logout" || r.URL.Path == "/locale" || r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/logout" || r.URL.Path == "/api/v1/locale")
		if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") || !read && !authentication {
			s.writeProblem(w, r, http.StatusServiceUnavailable, "recovery.read_only", nil, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}
