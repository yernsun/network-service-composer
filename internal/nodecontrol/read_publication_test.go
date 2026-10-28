package nodecontrol

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type advancingReportStore struct {
	documentStore
	advanced atomic.Bool
	advance  func()
}

func (s *advancingReportStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	doc, err := s.documentStore.GetControlDocument(ctx, key)
	if err == nil && key == reportKey("node-a") && !s.advanced.Swap(true) {
		s.advance()
	}
	return doc, err
}

func TestReportReadRetriesConcurrentPublicationWithoutRefreshingEvidenceTime(t *testing.T) {
	s, now, owner := reportsFixture(t)
	session, err := s.Begin(t.Context(), "controller-a", "node-a", "engine-a", "boot", *owner)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
	if err = s.Accept(t.Context(), "controller-a", report); err != nil {
		t.Fatal(err)
	}
	original := s
	s.Store = &advancingReportStore{documentStore: s.Store, advance: func() {
		report.Sequence++
		if err := original.Accept(t.Context(), "controller-a", report); err != nil {
			t.Fatal(err)
		}
	}}
	current, err := s.Read(t.Context(), "node-a")
	if err != nil || current.Sequence != 2 || !current.Observation.At.Equal(*now) {
		t.Fatal("normal publication became stale evidence", current, err)
	}
}
