package api

import (
	"github.com/yernsun/network-service-composer/internal/cluster"
	"net/http"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.ResourceCounts(r.Context())
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	summary, rows := s.core.Cluster.Summary()
	if missing := int(counts["runtimes"]) - len(rows); missing > 0 {
		summary.UnknownRuntimes += missing
		summary.Complete = false
	}
	writeData(w, 200, struct {
		Resources map[string]int64    `json:"resources"`
		Cluster   cluster.NodeSummary `json:"cluster"`
	}{counts, summary})
}
func (s *Server) overviewRuntimes(w http.ResponseWriter, r *http.Request) {
	_, rows := s.core.Cluster.Summary()
	writeClusterPage(s, w, r, rows, func(v cluster.RuntimeSummary) string { return v.ID })
}
