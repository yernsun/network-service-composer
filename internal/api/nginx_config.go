package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
)

func (s *Server) registerNginx(r chi.Router) {
	base := "/gateway-pools/{id}/nginx"
	r.Post(base+"/releases/{releaseID}/verify", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		v, err := s.core.VerifyNginxRelease(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "releaseID"), expected)
		s.controlResponse(w, r, v, v.Revision, err, 200)
	})
	r.Post(base+"/releases/{releaseID}/stop", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		v, err := s.core.StopNginxRelease(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "releaseID"), expected)
		s.controlResponse(w, r, v, v.Revision, err, 200)
	})
	r.Get(base, func(w http.ResponseWriter, r *http.Request) {
		pool := chi.URLParam(r, "id")
		active, e := s.core.NginxConfiguration(r.Context(), pool, "nginx-active")
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		draft, e := s.core.NginxConfiguration(r.Context(), pool, "nginx-draft")
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		sandbox, e := s.core.GetSandbox(r.Context(), pool)
		s.controlResponse(w, r, map[string]any{"active": active, "draft": draft, "sandbox": sandbox}, draft.Revision, e, 200)
	})
	r.Get(base+"/draft", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.NginxConfiguration(r.Context(), chi.URLParam(r, "id"), "nginx-draft")
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Put(base+"/draft", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.NginxConfiguration
		if !s.decodeJSON(w, r, &in, 2<<20) {
			return
		}
		v, e := s.core.SaveNginxDraft(r.Context(), chi.URLParam(r, "id"), in, requestActor(r), expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Get(base+"/draft-versions", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		v, e := s.core.ListNginxDraftVersions(r.Context(), chi.URLParam(r, "id"), page)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		writeCollection(w, v, page, func(v core.NginxConfiguration) string { return v.ID })
	})
	r.Get(base+"/versions", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		v, e := s.core.ListNginxVersions(r.Context(), chi.URLParam(r, "id"), page)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		writeCollection(w, v, page, func(v core.NginxConfiguration) string { return v.ID })
	})
	r.Post(base+"/versions/{versionID}/restore", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		v, e := s.core.RestoreNginxVersion(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "versionID"), requestActor(r), expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Get(base+"/sandbox", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetSandbox(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Put(base+"/sandbox", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.SandboxSettings
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.SaveSandbox(r.Context(), chi.URLParam(r, "id"), in, expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post(base+"/validations", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		v, e := s.core.CreateNginxValidation(r.Context(), chi.URLParam(r, "id"), expected)
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get(base+"/validations/{validationID}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetNginxValidation(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "validationID"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post(base+"/validations/{validationID}/cancel", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		e = s.core.CancelNginxValidation(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "validationID"), expected)
		s.controlResponse(w, r, map[string]any{"status": "CANCELLED"}, 0, e, 200)
	})
	r.Post(base+"/releases", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in struct {
			ValidationID string `json:"validation_id"`
		}
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.PublishNginx(r.Context(), chi.URLParam(r, "id"), in.ValidationID, requestActor(r), r.Header.Get("Idempotency-Key"), expected)
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get(base+"/releases/{releaseID}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetNginxRelease(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "releaseID"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Get(base+"/preview", s.nginxPreview)
	r.Post(base+"/preview", func(w http.ResponseWriter, r *http.Request) {
		var in core.NginxPreviewInput
		if !s.decodeJSON(w, r, &in, 2<<20) {
			return
		}
		v, e := s.core.PreviewNginx(r.Context(), chi.URLParam(r, "id"), in, func(snap model.Snapshot) (nginxx.PreviewDocument, error) {
			return nginxx.NewRenderer(s.config.NGINX).Preview(gateway.SnapshotToProto(snap))
		})
		w.Header().Set("Cache-Control", "no-store")
		s.controlResponse(w, r, v, v.ConfigRevision, e, 200)
	})
}
func (s *Server) nginxPreview(w http.ResponseWriter, r *http.Request) {
	pool := chi.URLParam(r, "id")
	snapshot, e := s.store.BuildSnapshot(r.Context(), pool)
	if e != nil {
		s.handleError(w, r, e)
		return
	}
	if r.URL.Query().Get("source") == "draft" {
		snapshot, e = s.core.NginxDraftSnapshot(r.Context(), pool)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		snapshot.Revision++
	}
	directory, e := os.MkdirTemp("", "nsc-preview-")
	if e != nil {
		s.handleError(w, r, e)
		return
	}
	defer os.RemoveAll(directory)
	renderer := nginxx.NewRenderer(s.config.NGINX)
	if e = renderer.Render(gateway.SnapshotToProto(snapshot), directory); e != nil {
		s.handleError(w, r, e)
		return
	}
	files := map[string]string{"nginx.conf": string(renderer.MainConfig("/run/nginx.pid"))}
	e = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".conf") {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err == nil {
			files[filepath.ToSlash(relative)] = string(content)
		}
		return err
	})
	s.controlResponse(w, r, map[string]any{"files": files, "config_revision": snapshot.Revision, "renderer_version": nginxx.RendererVersion}, snapshot.Revision, e, 200)
}
