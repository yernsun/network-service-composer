package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (s *Server) consoleFilter(w http.ResponseWriter, r *http.Request) (storage.ConsoleFilter, bool) {
	q := r.URL.Query()
	f := storage.ConsoleFilter{ServiceFilter: storage.ServiceFilter{RuntimeID: q.Get("runtime_id"), ApplicationID: q.Get("application_id"), GroupKey: q.Get("group_key"), Search: q.Get("search"), Status: model.ServiceStatus(q.Get("status"))}, DomainID: q.Get("domain_id"), ServiceID: q.Get("service_id")}
	field := ""
	includeRemoved := false
	if q.Has("include_removed") {
		var err error
		includeRemoved, err = strconv.ParseBool(q.Get("include_removed"))
		if err != nil {
			field = "include_removed"
		}
	}
	f.ExcludeRemoved = !includeRemoved && f.Status != model.ServiceRemoved
	if f.GroupKey != "" {
		if _, err := storage.ParseGroupKey(f.GroupKey); err != nil {
			field = "group_key"
		}
	}
	if q.Get("managed") != "" {
		b, err := strconv.ParseBool(q.Get("managed"))
		if err != nil {
			field = "managed"
		}
		f.Managed = &b
	}
	if field != "" {
		s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: field, Message: "invalid filter"})
		return f, false
	}
	return f, true
}
func (s *Server) listServiceGroups(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		resource = "services"
	}
	if resource != "services" && resource != "routes" {
		s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "resource", Message: "resource must be services or routes"})
		return
	}
	f, ok := s.consoleFilter(w, r)
	if !ok {
		return
	}
	page := parsePage(r)
	if page.After != "" {
		if _, err := storage.ParseGroupKey(page.After); err != nil {
			s.handleError(w, r, core.ValidationError{Code: "validation.filter_invalid", Field: "cursor", Message: "invalid group cursor"})
			return
		}
	}
	values, err := s.store.ListServiceGroups(r.Context(), resource, f, page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(v storage.ServiceGroup) string { return v.Key })
}
func (s *Server) writeRoutes(w http.ResponseWriter, r *http.Request, values []model.Route, page storage.Page) {
	if r.URL.Query().Get("include") != "references" {
		writeCollection(w, values, page, func(v model.Route) string { return v.ID })
		return
	}
	services, domains := []string{}, []string{}
	for _, v := range values {
		services = append(services, v.ServiceID)
		domains = append(domains, v.DomainID)
	}
	sr, err := s.store.ConsoleReferences(r.Context(), "services", services)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	dr, err := s.store.ConsoleReferences(r.Context(), "domains", domains)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	result := make([]map[string]any, 0, len(values))
	for _, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var row map[string]any
		if err = json.Unmarshal(b, &row); err != nil {
			s.handleError(w, r, err)
			return
		}
		row["service"] = sr[v.ServiceID]
		row["domain"] = dr[v.DomainID]
		result = append(result, row)
	}
	writeCollection(w, result, page, func(v map[string]any) string { return v["id"].(string) })
}
