package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (s *Server) registerCluster(router chi.Router) {
	router.Get("/overview", s.overview)
	router.Get("/overview/runtimes", s.overviewRuntimes)
	const p = "/runtimes/{id}/cluster"
	router.Get(p, s.clusterOverview)
	router.Get(p+"/", s.clusterOverview)
	router.Get(p+"/nodes", s.clusterNodes)
	router.Get(p+"/nodes/{nodeId}", s.clusterNode)
	router.Get(p+"/nodes/{nodeId}/workloads", s.clusterWorkloads)
	router.Get(p+"/nodes/{nodeId}/instances", s.clusterInstances)
	router.Get(p+"/metrics", s.clusterMetrics)
	router.Put(p+"/nodes/{nodeId}/labels", s.clusterLabels)
}
func (s *Server) clusterError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, cluster.ErrUnavailable) {
		s.writeProblem(w, r, 503, "system.not_ready", nil, "")
		return
	}
	if errors.Is(err, cluster.ErrReadOnly) {
		s.writeProblem(w, r, 403, "cluster.read_only", nil, "")
		return
	}
	s.handleError(w, r, err)
}
func (s *Server) clusterInventory(w http.ResponseWriter, r *http.Request) (cluster.Inventory, bool) {
	history := false
	if v := r.URL.Query().Get("history"); v != "" {
		var err error
		history, err = strconv.ParseBool(v)
		if err != nil {
			s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "history"})
			return cluster.Inventory{}, false
		}
	}
	v, err := s.core.Cluster.Inventory(r.Context(), chi.URLParam(r, "id"), history)
	if err != nil {
		s.clusterError(w, r, err)
		return v, false
	}
	return v, true
}
func (s *Server) clusterOverview(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if ok {
		writeData(w, 200, v.Overview)
	}
}
func (s *Server) clusterNodes(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	rows := []cluster.Node{}
	for _, n := range v.Nodes {
		if q.Get("node_id") != "" && q.Get("node_id") != n.ID {
			continue
		}
		if search := strings.ToLower(q.Get("search")); search != "" && !strings.Contains(strings.ToLower(n.Name+" "+n.ID), search) {
			continue
		}
		if state := q.Get("state"); state != "" && state != n.State {
			continue
		}
		if label := q.Get("label"); label != "" {
			k, value, withValue := strings.Cut(label, "=")
			actual, exists := n.Labels[k]
			if !exists || withValue && actual != value {
				continue
			}
		}
		rows = append(rows, n)
	}
	writeClusterPage(s, w, r, rows, func(v cluster.Node) string { return v.ID })
}
func (s *Server) clusterNode(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if !ok {
		return
	}
	n, err := v.Node(chi.URLParam(r, "nodeId"))
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	if n.Version > 0 {
		w.Header().Set("ETag", fmt.Sprintf(`"rev-%d"`, n.Version))
	}
	writeData(w, 200, n)
}
func (s *Server) clusterWorkloads(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "nodeId")
	if _, err := v.Node(id); err != nil {
		s.clusterError(w, r, err)
		return
	}
	rows := []cluster.Workload{}
	for _, w := range v.Workloads {
		if w.NodeID == id {
			rows = append(rows, w)
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.ServiceID != "" && !seen[row.ServiceID] {
			ids = append(ids, row.ServiceID)
			seen[row.ServiceID] = true
		}
	}
	refs := map[string]model.EnvironmentReference{}
	for start := 0; start < len(ids); start += 200 {
		part, err := s.store.ServiceEnvironments(r.Context(), ids[start:min(start+200, len(ids))])
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		for key, value := range part {
			refs[key] = value
		}
	}
	for i := range rows {
		if ref, ok := refs[rows[i].ServiceID]; ok {
			rows[i].Environment = &ref
		}
	}
	writeClusterPage(s, w, r, rows, func(v cluster.Workload) string { return v.ID })
}
func (s *Server) clusterInstances(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "nodeId")
	if _, err := v.Node(id); err != nil {
		s.clusterError(w, r, err)
		return
	}
	q := r.URL.Query()
	rows := []cluster.Instance{}
	for _, i := range v.Instances {
		if i.NodeID == id && (q.Get("workload_id") == "" || q.Get("workload_id") == i.WorkloadID) && (q.Get("instance_id") == "" || q.Get("instance_id") == i.ID) {
			rows = append(rows, i)
		}
	}
	writeClusterPage(s, w, r, rows, func(v cluster.Instance) string { return v.ID })
}
func (s *Server) clusterMetrics(w http.ResponseWriter, r *http.Request) {
	v, ok := s.clusterInventory(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("range") != "" && q.Get("range") != "1h" {
		s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "range"})
		return
	}
	node, err := v.Node(q.Get("node_id"))
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	if q.Get("container_id") == "" {
		writeData(w, 200, cluster.Metrics{Source: "unavailable", UnavailableReason: "host_metrics_unavailable", Samples: []cluster.Metric{}, RetentionSeconds: 3600})
		return
	}
	for _, i := range v.Instances {
		if i.NodeID == node.ID && i.ContainerID == q.Get("container_id") {
			metrics := s.core.Cluster.Metrics(i.EngineID, i.ContainerID)
			if node.Stale && metrics.Current != nil {
				metrics.Current.Stale = true
				metrics.UnavailableReason = "stale"
			}
			if i.EngineID != "" && i.State != "running" {
				metrics.UnavailableReason = "not_running"
				if metrics.Current != nil {
					metrics.Current.Stale = true
				}
			}
			writeData(w, 200, metrics)
			return
		}
	}
	s.clusterError(w, r, storage.ErrNotFound)
}
func (s *Server) clusterLabels(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var input struct {
		Labels []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"labels"`
	}
	if !s.decodeJSON(w, r, &input, 65536) {
		return
	}
	labels := map[string]string{}
	if input.Labels == nil {
		s.handleError(w, r, core.ValidationError{Code: "validation.cluster_labels", Field: "labels"})
		return
	}
	for index, l := range input.Labels {
		_, duplicate := labels[l.Key]
		if strings.TrimSpace(l.Key) == "" || strings.ContainsAny(l.Key, "\x00\r\n") || duplicate {
			s.handleError(w, r, core.ValidationError{Code: "validation.cluster_labels", Field: fmt.Sprintf("labels.%d.key", index)})
			return
		}
		labels[l.Key] = l.Value
	}
	v, err := s.core.Cluster.UpdateLabels(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeId"), uint64(expected), labels)
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"rev-%d"`, v.Version))
	writeData(w, 200, v)
}

// Cursors bind the resource path and filters. Docker itself has no list cursor;
// pagination is over the shared, stable-ID-sorted inventory on the server.
func writeClusterPage[T any](s *Server, w http.ResponseWriter, r *http.Request, rows []T, id func(T) string) {
	q := r.URL.Query()
	limit := 50
	if q.Has("limit") {
		v, err := strconv.Atoi(q.Get("limit"))
		if err != nil || v < 1 || v > 200 {
			s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "limit"})
			return
		}
		limit = v
	}
	cursor := q.Get("cursor")
	q.Del("cursor")
	q.Del("limit")
	digest := sha256.Sum256([]byte(r.URL.Path + "?" + q.Encode()))
	scope := base64.RawURLEncoding.EncodeToString(digest[:])
	after := ""
	if cursor != "" {
		var parts []string
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err == nil {
			err = json.Unmarshal(b, &parts)
		}
		if err != nil || len(parts) != 2 || parts[0] != scope {
			s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "cursor"})
			return
		}
		after = parts[1]
	}
	start := 0
	for start < len(rows) && id(rows[start]) <= after {
		start++
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	next := ""
	if end < len(rows) {
		b, _ := json.Marshal([]string{scope, id(rows[end-1])})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	writeJSON(w, 200, map[string]any{"data": rows[start:end], "pagination": map[string]any{"next_cursor": next, "limit": limit}})
}
