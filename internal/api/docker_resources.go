package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *Server) dockerResourceRuntime(w http.ResponseWriter, r *http.Request) (model.Runtime, bool) {
	user, ok := requestUser(r)
	if !ok || user.Role != "administrator" {
		s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "")
		return model.Runtime{}, false
	}
	runtime, err := s.store.GetRuntime(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return model.Runtime{}, false
	}
	if runtime.Type != model.RuntimeSwarm || !runtime.Enabled || !runtime.ManagementEnabled || runtime.ClusterID == "" {
		s.writeProblem(w, r, http.StatusUnprocessableEntity, "validation.runtime_type_invalid", nil, "")
		return model.Runtime{}, false
	}
	return runtime, true
}

func validDockerResourceKind(value string) bool { return value == "config" || value == "secret" }

func validDockerResourceName(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && (character == '_' || character == '-' || character == '.') {
			continue
		}
		return false
	}
	return true
}

func (s *Server) listDockerResources(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.dockerResourceRuntime(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validDockerResourceKind(kind) {
		s.writeProblem(w, r, http.StatusBadRequest, "validation.filter_invalid", nil, "")
		return
	}
	listed, err := (dockerresource.Manager{Store: s.store, Config: s.config, Reports: s.core.HAReports}).Do(r.Context(), dockerresource.Request{RuntimeID: runtime.ID, Action: "list", Kind: kind})
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	sort.Slice(listed.Items, func(i, j int) bool { return listed.Items[i].ID < listed.Items[j].ID })
	page := parsePage(r)
	if page.Limit < 1 || page.Limit > 200 {
		page.Limit = 50
	}
	items := make([]dockerresource.Item, 0, page.Limit)
	for _, item := range listed.Items {
		if item.ID <= page.After {
			continue
		}
		items = append(items, item)
		if len(items) > page.Limit {
			break
		}
	}
	next := ""
	if len(items) > page.Limit {
		items = items[:page.Limit]
		next = items[len(items)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": map[string]any{"next_cursor": next, "limit": page.Limit}})
}

func (s *Server) queueDockerResource(w http.ResponseWriter, r *http.Request, runtime model.Runtime, action string, input any) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		s.writeProblem(w, r, http.StatusBadRequest, "validation.filter_invalid", nil, "")
		return
	}
	raw, err := json.Marshal(input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	digest := sha256.Sum256(append([]byte(action+"\x00"), raw...))
	now := time.Now().UTC()
	typeName := "DOCKER_RESOURCE_CREATE"
	if action == "delete" {
		typeName = "DOCKER_RESOURCE_DELETE"
	}
	operation := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: typeName, Status: model.OperationQueued,
		IdempotencyKey: key, RequestSHA256: hex.EncodeToString(digest[:]), EncryptionKeyID: s.config.Security.PrimaryKeyID,
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	operation.EncryptedInput, err = s.core.EncryptDockerResourceInput(operation.ID, raw)
	for i := range raw {
		raw[i] = 0
	}
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	created, err := s.store.CreateDeploymentOperation(r.Context(), &operation)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+created.ID)
	writeData(w, http.StatusAccepted, s.operationResponse(r.Context(), created))
}

func (s *Server) createDockerResource(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.dockerResourceRuntime(w, r)
	if !ok {
		return
	}
	var input struct {
		Kind          string `json:"kind"`
		Name          string `json:"name"`
		ContentBase64 string `json:"content_base64"`
	}
	if !s.decodeJSON(w, r, &input, 800<<10) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validDockerResourceKind(input.Kind) || !validDockerResourceName(input.Name) || len(input.ContentBase64) > base64.StdEncoding.EncodedLen(500<<10) {
		s.writeProblem(w, r, http.StatusUnprocessableEntity, "validation.filter_invalid", nil, "")
		return
	}
	content, err := base64.StdEncoding.Strict().DecodeString(input.ContentBase64)
	if err != nil || len(content) == 0 || len(content) > 500<<10 {
		s.writeProblem(w, r, http.StatusUnprocessableEntity, "validation.filter_invalid", nil, "")
		return
	}
	defer func() {
		for i := range content {
			content[i] = 0
		}
	}()
	s.queueDockerResource(w, r, runtime, "create", struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Content []byte `json:"content"`
	}{input.Kind, input.Name, content})
}

func (s *Server) deleteDockerResource(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.dockerResourceRuntime(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	resourceID := chi.URLParam(r, "resourceID")
	if !validDockerResourceKind(kind) || resourceID == "" || len(resourceID) > 255 {
		s.writeProblem(w, r, http.StatusBadRequest, "validation.filter_invalid", nil, "")
		return
	}
	var input struct {
		AcknowledgeRestoreRisk bool `json:"acknowledge_restore_risk"`
	}
	if !s.decodeJSON(w, r, &input, 1024) {
		return
	}
	if !input.AcknowledgeRestoreRisk {
		s.writeProblem(w, r, http.StatusPreconditionRequired, "precondition.if_match_required", nil, "")
		return
	}
	if _, err := s.store.GetDockerResource(r.Context(), runtime.ClusterID, kind, resourceID); err != nil {
		s.handleError(w, r, err)
		return
	}
	s.queueDockerResource(w, r, runtime, "delete", struct {
		Kind                   string `json:"kind"`
		ID                     string `json:"id"`
		AcknowledgeRestoreRisk bool   `json:"acknowledge_restore_risk"`
	}{kind, resourceID, true})
}
