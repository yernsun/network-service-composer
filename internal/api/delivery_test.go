package api

import (
	"context"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectVariableCookieCSRFETagAndAudit(t *testing.T) {
	server, handler := newTestServerWithConfig(t, func(cfg *config.Config) { cfg.Management.Enabled = true })
	cookie, csrf := browserLogin(t, handler)
	create := browserRequest(handler, "POST", "/api/v1/projects", `{"name":"API project","slug":"api-project"}`, cookie, csrf)
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body)
	}
	var project struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e := json.Unmarshal(create.Body.Bytes(), &project); e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/projects/" + project.Data.ID + "/variables"
	put := func(token, auth, etag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, strings.NewReader(`{"values":[{"key":"TOKEN","value":"must-not-return","secret":true}]}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", token)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		r.Header.Set("If-Match", etag)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if r := put("", "", `"rev-0"`); r.Code != 403 {
		t.Fatal(r.Code, r.Body)
	}
	if r := put(csrf, "Bearer invalid", `"rev-0"`); r.Code != 401 {
		t.Fatal(r.Code, r.Body)
	}
	if r := put(csrf, "", `"rev-0"`); r.Code != 200 || strings.Contains(r.Body.String(), "must-not-return") {
		t.Fatal(r.Code, r.Body)
	}
	if r := put(csrf, "", `"rev-0"`); r.Code != 412 {
		t.Fatal(r.Code, r.Body)
	}
	var count int
	if e := server.store.(*storage.SQLStore).DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE details NOT LIKE '%must-not-return%'`).Scan(&count); e != nil || count < 2 {
		t.Fatal(count, e)
	}
}
