package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func (s *Server) volumeRuntime(w http.ResponseWriter, r *http.Request) (model.Runtime, bool) {
	user, ok := requestUser(r)
	if !ok || user.Role != "administrator" {
		s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "")
		return model.Runtime{}, false
	}
	rt, err := s.store.GetRuntime(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return rt, false
	}
	if !rt.Enabled || rt.Type != model.RuntimeDocker && rt.Type != model.RuntimeSwarm {
		s.writeProblem(w, r, 422, "validation.runtime_type_invalid", nil, "")
		return rt, false
	}
	if rt.Type == model.RuntimeSwarm && r.URL.Query().Get("node_id") == "" {
		s.writeProblem(w, r, 422, "validation.filter_invalid", nil, "")
		return rt, false
	}
	return rt, true
}

func (s *Server) volumeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, volumemanager.ErrIdentity):
		s.writeProblem(w, r, 503, "system.not_ready", nil, "")
	case errors.Is(err, volumemanager.ErrBlocked):
		s.writeProblem(w, r, 409, "volume.in_use", nil, "")
	default:
		s.handleError(w, r, err)
	}
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.volumeRuntime(w, r)
	if !ok {
		return
	}
	result, err := s.core.Volumes().Do(r.Context(), volumemanager.Request{RuntimeID: rt.ID, NodeID: r.URL.Query().Get("node_id"), Action: "list"})
	if err != nil {
		s.volumeError(w, r, err)
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	usage := q.Get("usage")
	if kind != "" && kind != "named" && kind != "anonymous" && kind != "unknown" || usage != "" && usage != "used" && usage != "unused" {
		s.writeProblem(w, r, 400, "validation.filter_invalid", nil, "")
		return
	}
	page := parsePage(r)
	if page.Limit < 1 || page.Limit > 200 {
		page.Limit = 50
	}
	items := []volumemanager.Item{}
	for _, item := range result.Items {
		if item.Name <= page.After || kind != "" && item.Kind != kind || q.Get("search") != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(q.Get("search"))) || usage == "used" && len(item.UsedBy) == 0 || usage == "unused" && (len(item.UsedBy) > 0 || !result.Complete) {
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
		next = items[len(items)-1].Name
	}
	result.Items = items
	writeJSON(w, 200, map[string]any{"data": result.Items, "node_id": result.NodeID, "engine_id": result.EngineID, "node_name": result.NodeName, "observed_at": result.ObservedAt, "complete": result.Complete, "pagination": map[string]any{"next_cursor": next, "limit": page.Limit}})
}

func (s *Server) getVolume(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.volumeRuntime(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !validDockerResourceName(name) {
		s.writeProblem(w, r, 400, "validation.filter_invalid", nil, "")
		return
	}
	result, err := s.core.Volumes().Do(r.Context(), volumemanager.Request{RuntimeID: rt.ID, NodeID: r.URL.Query().Get("node_id"), Action: "inspect", Name: name})
	if err != nil {
		s.volumeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"volume-`+result.Items[0].Version+`"`)
	writeJSON(w, 200, map[string]any{"data": result.Items[0], "observed_at": result.ObservedAt, "complete": result.Complete})
}

func volumeDigest(req volumemanager.Request) string {
	// The user request is stable across retries; EngineID is frozen after resolving its target.
	req.EngineID = ""
	req.OperationID = ""
	raw, _ := json.Marshal(req)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (s *Server) volumeReplay(w http.ResponseWriter, r *http.Request, rt model.Runtime, req volumemanager.Request) bool {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		s.writeProblem(w, r, 400, "validation.filter_invalid", nil, "")
		return true
	}
	previous, err := s.store.GetDeploymentOperationByIdempotency(r.Context(), rt.ID, key)
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}
	if err != nil {
		s.handleError(w, r, err)
		return true
	}
	if previous.RequestSHA256 != volumeDigest(req) {
		s.handleError(w, r, storage.ErrConflict)
		return true
	}
	w.Header().Set("Location", "/api/v1/operations/"+previous.ID)
	writeData(w, 202, s.operationResponse(r.Context(), previous))
	return true
}
func (s *Server) queueVolume(w http.ResponseWriter, r *http.Request, rt model.Runtime, req volumemanager.Request, digest string) {
	raw, err := json.Marshal(req)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	defer clear(raw)
	now := time.Now().UTC()
	kind := "VOLUME_CREATE"
	if req.Action == "delete" {
		kind = "VOLUME_DELETE"
	}
	op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: rt.ID, Type: kind, Status: model.OperationQueued, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), RequestSHA256: digest, EncryptionKeyID: s.config.Security.PrimaryKeyID, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	op.EncryptedInput, err = s.core.EncryptDockerResourceInput(op.ID, raw)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	created, err := s.store.CreateDeploymentOperation(r.Context(), &op)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+created.ID)
	writeData(w, 202, s.operationResponse(r.Context(), created))
}
func (s *Server) volumeWritesEnabled(w http.ResponseWriter, r *http.Request, rt model.Runtime) bool {
	if !rt.ManagementEnabled || !s.config.Management.Enabled {
		s.writeProblem(w, r, 403, "management.disabled", nil, "")
		return false
	}
	return true
}
func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.volumeRuntime(w, r)
	if !ok || !s.volumeWritesEnabled(w, r, rt) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !s.decodeJSON(w, r, &input, 1024) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name != "" && !validDockerResourceName(input.Name) {
		s.writeProblem(w, r, 422, "validation.filter_invalid", nil, "")
		return
	}
	req := volumemanager.Request{RuntimeID: rt.ID, NodeID: r.URL.Query().Get("node_id"), Action: "create", Name: input.Name}
	if s.volumeReplay(w, r, rt, req) {
		return
	}
	digest := volumeDigest(req)
	target, err := s.core.Volumes().Do(r.Context(), volumemanager.Request{RuntimeID: rt.ID, NodeID: req.NodeID, Action: "target"})
	if err != nil {
		s.volumeError(w, r, err)
		return
	}
	req.NodeID = target.NodeID
	req.EngineID = target.EngineID
	s.queueVolume(w, r, rt, req, digest)
}
func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.volumeRuntime(w, r)
	if !ok || !s.volumeWritesEnabled(w, r, rt) {
		return
	}
	name := chi.URLParam(r, "name")
	if !validDockerResourceName(name) {
		s.writeProblem(w, r, 400, "validation.filter_invalid", nil, "")
		return
	}
	var input struct {
		AcknowledgeDataLoss bool `json:"acknowledge_data_loss"`
	}
	if !s.decodeJSON(w, r, &input, 1024) {
		return
	}
	etag := r.Header.Get("If-Match")
	if etag == "" || !input.AcknowledgeDataLoss {
		s.writeProblem(w, r, 428, "precondition.if_match_required", nil, "")
		return
	}
	if len(etag) != 73 || !strings.HasPrefix(etag, `"volume-`) || !strings.HasSuffix(etag, `"`) {
		s.writeProblem(w, r, 400, "precondition.if_match_invalid", nil, "")
		return
	}
	version := strings.TrimSuffix(strings.TrimPrefix(etag, `"volume-`), `"`)
	if _, err := hex.DecodeString(version); err != nil {
		s.writeProblem(w, r, 400, "precondition.if_match_invalid", nil, "")
		return
	}
	req := volumemanager.Request{RuntimeID: rt.ID, NodeID: r.URL.Query().Get("node_id"), Action: "delete", Name: name, Version: version, AcknowledgeDataLoss: true}
	if s.volumeReplay(w, r, rt, req) {
		return
	}
	digest := volumeDigest(req)
	result, err := s.core.Volumes().Do(r.Context(), volumemanager.Request{RuntimeID: rt.ID, NodeID: req.NodeID, Action: "inspect", Name: name})
	if err != nil {
		s.volumeError(w, r, err)
		return
	}
	item := result.Items[0]
	if item.Version != req.Version {
		s.handleError(w, r, storage.ErrPrecondition)
		return
	}
	if len(item.BlockedReasons) > 0 {
		s.volumeError(w, r, volumemanager.ErrBlocked)
		return
	}
	req.NodeID = result.NodeID
	req.EngineID = result.EngineID
	s.queueVolume(w, r, rt, req, digest)
}
