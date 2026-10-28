package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/i18n"
)

const localeCookieName = "nsc_locale"

func (s *Server) localeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieValue := ""
		if cookie, err := r.Cookie(localeCookieName); err == nil {
			cookieValue = cookie.Value
		}
		locale := i18n.Resolve(cookieValue, r.Header.Get("Accept-Language"), s.config.I18N.DefaultLanguage)
		w.Header().Set("Content-Language", string(locale))
		addVary(w.Header(), "Accept-Language")
		addVary(w.Header(), "Cookie")
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), locale)))
	})
}

func addVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for _, current := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(current), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func (s *Server) setLocale(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.writeProblem(w, r, http.StatusBadRequest, "request.invalid_form", nil, "")
		return
	}
	selected := r.FormValue("lang")
	if !i18n.IsSupportedCode(selected) {
		s.writeProblem(w, r, http.StatusBadRequest, "request.invalid_form", nil, "lang")
		return
	}
	s.localeCookie(w, selected)
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	locale := i18n.FromContext(r.Context())
	data, ok := i18n.CatalogJSON(locale)
	if !ok {
		s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func requestedLocale(r *http.Request) i18n.Locale {
	return i18n.FromContext(r.Context())
}

func currentPath(r *http.Request) string {
	if r.URL.Path == "" {
		return "/projects"
	}
	query := r.URL.Query()
	query.Del("error")
	value := r.URL.Path
	if encoded := query.Encode(); encoded != "" {
		value += "?" + encoded
	}
	return safeNext(value)
}

func (s *Server) localeCookie(w http.ResponseWriter, selected string) {
	secure := strings.HasPrefix(strings.ToLower(s.config.Server.PublicURL), "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     localeCookieName,
		Value:    selected,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		Expires:  time.Now().UTC().Add(365 * 24 * time.Hour),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
