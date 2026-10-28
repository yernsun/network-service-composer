package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"

	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Server struct {
	core   *core.Core
	store  storage.Store
	config config.Config
	logger *slog.Logger

	ready   func() bool
	limiter *rate.Limiter
}

func New(application *core.Core, cfg config.Config, logger *slog.Logger, ready func() bool) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if ready == nil {
		ready = func() bool { return true }
	}
	if !cfg.Controller.RecoveryMode {
		if err := application.BootstrapIdentity(context.Background()); err != nil {
			return nil, err
		}
	}
	return &Server{core: application, store: application.Store(), config: cfg, logger: logger, ready: ready, limiter: rate.NewLimiter(rate.Limit(cfg.Server.RateLimitRPS), cfg.Server.RateLimitBurst)}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	router.Use(s.localeMiddleware)
	router.Use(s.securityHeaders)
	router.Use(s.accessLog)
	router.Use(s.recoveryReadOnly)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.config.Controller.RecoveryMode || !s.ready() {
			s.writeProblem(w, r, http.StatusServiceUnavailable, "system.not_ready", nil, "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	router.Get("/.well-known/acme-challenge/{token}", s.challenge)
	router.Get("/login", s.spaPage)
	router.With(s.rateLimit).Post("/login", s.login)
	router.With(s.rateLimit).Post("/locale", s.setLocale)
	router.Get("/static/i18n/catalog.json", s.catalog)
	router.Get("/assets/*", s.frontendAsset)
	router.With(s.rateLimit).Get("/api/v1/auth/session", s.sessionJSON)
	router.With(s.rateLimit).Post("/api/v1/auth/login", s.loginJSON)
	router.With(s.rateLimit, s.sessionAuth).Post("/api/v1/auth/logout", s.logoutJSON)
	router.With(s.rateLimit).Post("/api/v1/locale", s.localeJSON)
	router.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/projects", http.StatusFound) })
	router.Group(func(ui chi.Router) {
		ui.Use(s.rateLimit)
		ui.Use(s.uiAuth)
		ui.Use(s.auditUI)
		ui.Get("/logout", s.spaPage)
		ui.Post("/logout", s.logout)
		for _, root := range []string{"/projects", "/infrastructure", "/services", "/operations", "/market", "/account", "/users"} {
			ui.Get(root, s.spaPage)
			ui.Get(root+"/*", s.spaPage)
		}
	})
	router.With(s.rateLimit, func(next http.Handler) http.Handler { return s.auditMutations("image-webhook", next) }).Post("/api/v1/hooks/image-updates/{hookId}", s.imageWebhook)
	router.Route("/openapi/v1", s.registerOpenAPIDeployments)
	router.Route("/api/v1", func(api chi.Router) {
		api.Use(s.rateLimit)
		api.Use(s.apiAuth)
		api.Use(s.auditAPI)
		api.Use(s.authorizeRole)
		s.registerIdentities(api)
		s.registerOpenAPIKeyManagement(api)
		s.registerAPI(api)
	})
	return router
}

func (s *Server) auditAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.auditMutations(requestActor(r), next).ServeHTTP(w, r) })
}

func (s *Server) auditUI(next http.Handler) http.Handler {
	return s.auditMutations("ui-session", next)
}

func (s *Server) auditMutations(actor string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.Controller.RecoveryMode || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		requestDigest := sha256.New()
		if r.Body != nil {
			r.Body = auditReadCloser{Reader: io.TeeReader(r.Body, requestDigest), Closer: r.Body}
		}
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		resourceType, resourceID := "unknown", ""
		resourceIndex := -1
		if len(parts) >= 3 && parts[0] == "api" && parts[1] == "v1" {
			resourceIndex = 2
		} else if len(parts) >= 2 && parts[0] == "ui" {
			resourceIndex = 1
		}
		if resourceIndex >= 0 && resourceIndex < len(parts) {
			resourceType = parts[resourceIndex]
			if resourceIndex+1 < len(parts) {
				resourceID = parts[resourceIndex+1]
			}
		}
		details, _ := json.Marshal(map[string]any{"status": wrapped.Status(), "request_sha256": hex.EncodeToString(requestDigest.Sum(nil))})
		event := model.AuditEvent{ID: id.New(id.Audit), Actor: actor, Action: r.Method, ResourceType: resourceType, ResourceID: resourceID, RequestID: middleware.GetReqID(r.Context()), Details: string(details), CreatedAt: time.Now().UTC()}
		auditContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		defer cancel()
		if err := s.store.AppendAuditEvent(auditContext, event); err != nil {
			s.logger.ErrorContext(r.Context(), "persist audit event", "error", err, "request_id", event.RequestID)
		}
	})
}

type auditReadCloser struct {
	io.Reader
	io.Closer
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			s.writeProblem(w, r, http.StatusTooManyRequests, "rate_limit.exceeded", nil, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path,
			"status", ww.Status(), "bytes", ww.BytesWritten(), "duration", time.Since(started), "request_id", middleware.GetReqID(r.Context()))
	})
}

func (s *Server) apiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if _, present := r.Header["X-Nsc-Api-Key"]; present {
			s.writeProblem(w, r, http.StatusUnauthorized, "auth.required", nil, "")
			return
		}
		if _, present := r.Header["Authorization"]; !present {
			s.sessionAuth(next).ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		authorization := r.Header.Get("Authorization")
		user, err := s.core.Authenticate(r.Context(), "", "", strings.TrimPrefix(authorization, prefix))
		if !strings.HasPrefix(authorization, prefix) || err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nsc"`)
			s.writeProblem(w, r, http.StatusUnauthorized, "auth.required", nil, "")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, user)))
	})
}

func (s *Server) uiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.readSession(r)
		if !ok {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
			if !secureEqual(r.FormValue("csrf_token"), session.CSRF) {
				s.writeProblem(w, r, http.StatusForbidden, "csrf.invalid", nil, "csrf_token")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
	})
}

type sessionKey struct{}
type session struct {
	UserID       string    `json:"user_id"`
	UserRevision int64     `json:"user_revision"`
	User         core.User `json:"-"`
	Expires      int64     `json:"exp"`
	CSRF         string    `json:"csrf"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		s.writeProblem(w, r, http.StatusBadRequest, "request.invalid_form", nil, "")
		return
	}
	user, err := s.core.Authenticate(r.Context(), r.FormValue("username"), r.FormValue("password"), "")
	if err != nil {
		s.recordLogin(r, "", false)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		s.spaPage(w, r)
		return
	}
	s.recordLogin(r, user.ID, true)
	if _, ok := s.issueSession(w, r, user); !ok {
		return
	}
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "nsc_session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) signSession(value session) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(s.config.Security.SessionKey))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) readSession(r *http.Request) (session, bool) {
	var value session
	cookie, err := r.Cookie("nsc_session")
	if err != nil {
		return value, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return value, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return value, false
	}
	signature, err := hex.DecodeString(parts[1])
	if err != nil {
		return value, false
	}
	mac := hmac.New(sha256.New, []byte(s.config.Security.SessionKey))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return value, false
	}
	if json.Unmarshal(payload, &value) != nil || value.Expires < time.Now().Unix() {
		return value, false
	}
	user, err := s.core.User(r.Context(), value.UserID)
	if err != nil || !user.Enabled || user.Revision != value.UserRevision {
		return value, false
	}
	value.User = user
	return value, true
}

func safeNext(value string) string {
	parsed, err := url.ParseRequestURI(value)
	if err == nil && parsed.Host == "" && parsed.Path != "/logout" && knownSPAPath(parsed.Path) && safeSPAQuery(parsed.RawQuery) {
		return value
	}
	return "/projects"
}

func safeSPAQuery(raw string) bool {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	allowed := map[string]struct{}{
		"application_id": {}, "catalog_version_id": {}, "detail": {}, "domain_id": {},
		"environment_id": {}, "file": {}, "group_key": {}, "health": {}, "history": {},
		"kind": {}, "label": {}, "layout": {}, "managed": {}, "node_id": {}, "page": {},
		"pool_id": {}, "previewEnv": {}, "project_id": {}, "q": {}, "release_id": {},
		"resource_id": {}, "scope": {}, "search": {}, "selected": {}, "selected_node": {},
		"service_id": {}, "state": {}, "status": {}, "tab": {}, "validation_id": {},
		"view": {}, "runtime_id": {}, "perspective": {}, "layer": {}, "app": {}, "editor": {}, "runtime": {},
	}
	pairs := 0
	for key, entries := range values {
		if _, ok := allowed[key]; !ok {
			return false
		}
		for _, value := range entries {
			pairs++
			if pairs > 32 || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
				return false
			}
		}
	}
	return true
}
func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if len(token) < 1 || len(token) > 255 || strings.ContainsAny(token, "/\\\r\n") {
		http.NotFound(w, r)
		return
	}
	challenge, err := s.store.GetChallenge(r.Context(), token, time.Now().UTC())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.ErrorContext(r.Context(), "lookup ACME challenge", "error", err)
		s.writeProblem(w, r, http.StatusInternalServerError, "storage.challenge_lookup_failed", nil, "")
		return
	}
	host := strings.ToLower(strings.TrimSuffix(r.Host, "."))
	if hostname, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
		host = strings.ToLower(strings.TrimSuffix(hostname, "."))
	}
	if host != challenge.Domain {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, challenge.KeyAuthorization)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeData(w http.ResponseWriter, status int, value any) {
	writeJSON(w, status, map[string]any{"data": value})
}

func writeCollection[T any](w http.ResponseWriter, values []T, page storage.Page, identifier func(T) string) {
	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	next := ""
	if len(values) == limit && len(values) > 0 {
		next = identifier(values[len(values)-1])
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": values, "pagination": map[string]any{"next_cursor": next, "limit": limit}})
}

func parsePage(r *http.Request) storage.Page {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return storage.Page{Limit: limit, After: r.URL.Query().Get("cursor")}
}
func setETag(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", fmt.Sprintf(`"rev-%d"`, revision))
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, user core.User) (session, bool) {
	csrfBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, csrfBytes); err != nil {
		s.logger.ErrorContext(r.Context(), "create session nonce", "error", err)
		s.writeProblem(w, r, http.StatusInternalServerError, "session.create_failed", nil, "")
		return session{}, false
	}
	sess := session{UserID: user.ID, UserRevision: user.Revision, User: user, Expires: time.Now().Add(12 * time.Hour).Unix(), CSRF: base64.RawURLEncoding.EncodeToString(csrfBytes)}
	cookie, err := s.signSession(sess)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "sign session", "error", err)
		s.writeProblem(w, r, http.StatusInternalServerError, "session.create_failed", nil, "")
		return session{}, false
	}
	secure := strings.HasPrefix(strings.ToLower(s.config.Server.PublicURL), "https://")
	http.SetCookie(w, &http.Cookie{Name: "nsc_session", Value: cookie, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
	return sess, true
}
