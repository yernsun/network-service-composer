package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type timelineCoverage struct {
	State        string                     `json:"state"`
	Sources      []model.RuntimeEventSource `json:"sources"`
	RetainedFrom time.Time                  `json:"retained_from"`
}

func timelineCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "\n" + id))
}

func parseTimelineCursor(value string) (time.Time, string, error) {
	if value == "" {
		return time.Time{}, "", nil
	}
	if len(value) > 1024 {
		return time.Time{}, "", fmt.Errorf("cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	stamp, id, ok := strings.Cut(string(raw), "\n")
	if !ok || id == "" || len(id) > 255 {
		return time.Time{}, "", fmt.Errorf("invalid timeline cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	return at, id, err
}

func (s *Server) listTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if q.Get("limit") != "" {
		parsed, err := strconv.Atoi(q.Get("limit"))
		if err != nil || parsed < 1 || parsed > 199 {
			s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "limit"})
			return
		}
		limit = parsed
	}
	at, id, err := parseTimelineCursor(q.Get("cursor"))
	if err != nil {
		s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "cursor"})
		return
	}
	filter := storage.TimelineFilter{ProjectID: q.Get("project_id"), EnvironmentID: q.Get("environment_id"), RuntimeID: q.Get("runtime_id"),
		ApplicationID: q.Get("application_id"), ServiceID: q.Get("service_id"), Category: q.Get("category"), BeforeAt: at, BeforeID: id}
	if filter.Category != "" && filter.Category != "fault" && filter.Category != "health" && filter.Category != "lifecycle" &&
		filter.Category != "planned" && filter.Category != "gap" && filter.Category != "restart" && filter.Category != "completed" {
		s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "category"})
		return
	}
	filter.GapRuntimeIDs, err = s.timelineScopeRuntimes(r.Context(), filter)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	items, err := s.store.ListTimeline(r.Context(), filter, storage.Page{Limit: limit + 1})
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	coverage, err := s.timelineCoverageForScope(r, filter.GapRuntimeIDs)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = timelineCursor(last.OccurredAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": map[string]any{"next_cursor": next, "limit": limit}, "coverage": coverage})
}

func (s *Server) timelineCoverageForScope(r *http.Request, runtimeIDs []string) (timelineCoverage, error) {
	if runtimeIDs == nil {
		return s.timelineCoverage(r, "")
	}
	if len(runtimeIDs) == 0 {
		return timelineCoverage{State: "unavailable", Sources: []model.RuntimeEventSource{},
			RetainedFrom: time.Now().UTC().Add(-s.config.Controller.RuntimeEvents.Retention.Std())}, nil
	}
	if len(runtimeIDs) == 1 {
		return s.timelineCoverage(r, runtimeIDs[0])
	}
	var combined timelineCoverage
	seen := map[string]bool{}
	for _, runtimeID := range runtimeIDs {
		part, err := s.timelineCoverage(r, runtimeID)
		if err != nil {
			return timelineCoverage{}, err
		}
		combined.RetainedFrom = part.RetainedFrom
		for _, source := range part.Sources {
			if seen[source.ID] {
				continue
			}
			seen[source.ID] = true
			combined.Sources = append(combined.Sources, source)
		}
	}
	collecting := 0
	for _, source := range combined.Sources {
		if source.State == "collecting" {
			collecting++
		}
	}
	combined.State = "unavailable"
	if collecting == len(combined.Sources) && collecting > 0 {
		combined.State = "collecting"
	} else if collecting > 0 {
		combined.State = "partial"
	}
	return combined, nil
}

// A gap belongs to its collector's Runtime. Scope filters may reference an
// Environment, Application, or Service without explicitly naming that Runtime.
func (s *Server) timelineScopeRuntimes(ctx context.Context, filter storage.TimelineFilter) ([]string, error) {
	var allowed map[string]bool
	projectApps := map[string]bool{}
	var selectedEnvironment *core.Environment
	restrict := func(ids ...string) {
		next := map[string]bool{}
		for _, id := range ids {
			if id != "" && (allowed == nil || allowed[id]) {
				next[id] = true
			}
		}
		allowed = next
	}
	if filter.ProjectID != "" {
		ids := []string{}
		after := ""
		for {
			page, err := s.core.ListEnvironments(ctx, filter.ProjectID, storage.Page{After: after, Limit: 200})
			if err != nil {
				return nil, err
			}
			for _, environment := range page {
				ids = append(ids, environment.RuntimeID)
				if environment.ApplicationID != "" {
					projectApps[environment.ApplicationID] = true
				}
			}
			if len(page) < 200 {
				break
			}
			after = page[len(page)-1].ID
		}
		restrict(ids...)
	}
	if filter.EnvironmentID != "" {
		environment, err := s.core.GetEnvironment(ctx, filter.EnvironmentID)
		if errors.Is(err, storage.ErrNotFound) {
			restrict()
		} else if err != nil {
			return nil, err
		} else if filter.ProjectID != "" && environment.ProjectID != filter.ProjectID {
			restrict()
		} else {
			selectedEnvironment = &environment
			restrict(environment.RuntimeID)
		}
	}
	if filter.ApplicationID != "" {
		application, err := s.store.GetApplication(ctx, filter.ApplicationID)
		if errors.Is(err, storage.ErrNotFound) {
			restrict()
		} else if err != nil {
			return nil, err
		} else if selectedEnvironment != nil && selectedEnvironment.ApplicationID != filter.ApplicationID ||
			filter.ProjectID != "" && !projectApps[filter.ApplicationID] {
			restrict()
		} else {
			restrict(application.RuntimeID)
		}
	}
	if filter.ServiceID != "" {
		service, err := s.store.GetService(ctx, filter.ServiceID)
		if errors.Is(err, storage.ErrNotFound) {
			restrict()
		} else if err != nil {
			return nil, err
		} else if filter.ApplicationID != "" && service.ApplicationID != filter.ApplicationID ||
			selectedEnvironment != nil && selectedEnvironment.ApplicationID != service.ApplicationID ||
			filter.ProjectID != "" && !projectApps[service.ApplicationID] {
			restrict()
		} else {
			restrict(service.RuntimeID)
		}
	}
	if filter.RuntimeID != "" {
		restrict(filter.RuntimeID)
	}
	if allowed == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(allowed))
	for id := range allowed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Server) timelineCoverage(r *http.Request, runtimeID string) (timelineCoverage, error) {
	now := time.Now().UTC()
	coverage := timelineCoverage{State: "unavailable", Sources: []model.RuntimeEventSource{}, RetainedFrom: now.Add(-s.config.Controller.RuntimeEvents.Retention.Std())}
	if !s.config.Controller.RuntimeEvents.Enabled {
		return coverage, nil
	}
	sources, err := s.store.ListRuntimeEventSources(r.Context())
	if err != nil {
		return coverage, err
	}
	runtimes := []model.Runtime{}
	if runtimeID == "" {
		after := ""
		for {
			page, listErr := s.store.ListRuntimes(r.Context(), storage.Page{After: after, Limit: 200})
			if listErr != nil {
				return coverage, listErr
			}
			runtimes = append(runtimes, page...)
			if len(page) < 200 {
				break
			}
			after = page[len(page)-1].ID
		}
	}
	clusterID := ""
	var selected model.Runtime
	if runtimeID != "" {
		selected, err = s.store.GetRuntime(r.Context(), runtimeID)
		if err != nil {
			return coverage, err
		}
		runtimes = append(runtimes, selected)
		clusterID = selected.ClusterID
	}
	seenNodes := map[string]bool{}
	seenRuntimes := map[string]bool{}
	seenClusters := map[string]bool{}
	configuredClusters := map[string]bool{}
	for _, node := range s.config.Controller.Nodes {
		configuredClusters[node.ClusterID] = true
	}
	for _, source := range sources {
		if runtimeID != "" && source.RuntimeID != runtimeID && (clusterID == "" || source.ClusterID != clusterID) {
			continue
		}
		if now.Sub(source.LastHeartbeatAt) > 30*time.Second {
			source.State = "unavailable"
			source.LastError = "heartbeat_missing"
		}
		coverage.Sources = append(coverage.Sources, source)
		if source.RuntimeID != "" {
			seenRuntimes[source.RuntimeID] = true
		}
		if source.ClusterID != "" {
			seenClusters[source.ClusterID] = true
		}
		if source.NodeID != "" {
			seenNodes[source.NodeID] = true
		}
	}
	if runtimeID != "" && selected.Type != model.RuntimeSwarm && !seenRuntimes[runtimeID] {
		coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "runtime:" + runtimeID, RuntimeID: runtimeID,
			State: "unavailable", LastError: "collector_not_started"})
	}
	for _, runtime := range runtimes {
		if !runtime.Enabled {
			continue
		}
		if runtime.Type == model.RuntimeSwarm {
			if runtime.ClusterID == "" {
				coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "nodes:" + runtime.ID,
					RuntimeID: runtime.ID, State: "unavailable", LastError: "cluster_identity_missing"})
				continue
			}
			if seenClusters[runtime.ClusterID] || runtime.ClusterID != "" && configuredClusters[runtime.ClusterID] {
				continue
			}
			coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "nodes:" + runtime.ID,
				RuntimeID: runtime.ID, State: "unavailable", LastError: "collector_not_configured"})
		} else if !seenRuntimes[runtime.ID] {
			coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "runtime:" + runtime.ID,
				RuntimeID: runtime.ID, State: "unavailable", LastError: "collector_not_started"})
		}
	}
	if clusterID != "" || runtimeID == "" {
		for _, node := range s.config.Controller.Nodes {
			if clusterID != "" && node.ClusterID != clusterID {
				continue
			}
			if seenNodes[node.NodeID] {
				continue
			}
			coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "node:" + node.NodeID, ClusterID: node.ClusterID,
				NodeID: node.NodeID, EngineID: node.EngineID, State: "unavailable", LastError: "collector_not_started"})
			seenNodes[node.NodeID] = true
		}
	}
	if runtimeID != "" && selected.Type == model.RuntimeSwarm && selected.ClusterID != "" {
		inventory, inventoryErr := s.core.Cluster.Inventory(r.Context(), runtimeID, false)
		if inventoryErr == nil {
			for _, node := range inventory.Nodes {
				if seenNodes[node.ID] {
					continue
				}
				coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "node:" + node.ID, ClusterID: clusterID,
					NodeID: node.ID, State: "unavailable", LastError: "collector_not_configured"})
			}
		} else {
			// Coverage remains partial when node inventory itself cannot be established.
			coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "inventory:" + runtimeID, RuntimeID: runtimeID,
				State: "unavailable", LastError: "node_inventory_unavailable"})
		}
		if len(inventory.Nodes) == 0 && len(s.config.Controller.Nodes) == 0 {
			coverage.Sources = append(coverage.Sources, model.RuntimeEventSource{ID: "nodes:" + runtimeID, RuntimeID: runtimeID,
				State: "unavailable", LastError: "collector_not_configured"})
		}
	}
	collecting := 0
	for _, source := range coverage.Sources {
		if source.State == "collecting" {
			collecting++
		}
	}
	if collecting == len(coverage.Sources) && collecting > 0 {
		coverage.State = "collecting"
	} else if collecting > 0 {
		coverage.State = "partial"
	}
	return coverage, nil
}
