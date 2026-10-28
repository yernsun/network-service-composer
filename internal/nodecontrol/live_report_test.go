package nodecontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/hoststats"
)

func TestControllerHealthSurvivesIncompleteDockerInventoryButNotStaleOrLostAuthority(t *testing.T) {
	s, now, owner := reportsFixture(t)
	session, err := s.Begin(t.Context(), "controller-a", "node-a", "engine-a", "boot", *owner)
	if err != nil {
		t.Fatal(err)
	}
	sequence := uint64(0)
	publish := func(complete bool, health *hoststats.Health) {
		sequence++
		o := observationFixture(*now)
		o.Complete = complete
		o.Health = health
		if err := s.Accept(t.Context(), "controller-a", Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: sequence, Observation: o}); err != nil {
			t.Fatal(err)
		}
	}
	health := &hoststats.Health{InstanceID: "controller-a", At: *now, Controller: "healthy", Role: "follower", Term: 2}
	publish(false, health)
	if _, err = s.ReadControllerHealth(t.Context(), "node-a"); err != nil {
		t.Fatal("quorum outage hid current forwarding health", err)
	}
	if _, err = s.ReadLive(t.Context(), "node-a"); !errors.Is(err, ErrIncomplete) {
		t.Fatal("partial inventory authorized execution", err)
	}
	for _, at := range []time.Time{{}, now.Add(-6 * time.Second), now.Add(6 * time.Second)} {
		bad := *health
		bad.At = at
		publish(false, &bad)
		if _, err = s.ReadControllerHealth(t.Context(), "node-a"); !errors.Is(err, ErrStale) {
			t.Fatal("invalid health time confirmed readiness", err)
		}
	}
	publish(false, nil)
	if _, err = s.ReadControllerHealth(t.Context(), "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("missing health confirmed readiness", err)
	}
	publish(true, health)
	*owner++
	if _, err = s.ReadControllerHealth(t.Context(), "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("lost collector still confirmed readiness", err)
	}
	*owner--
	s.CheckOwner = nil
	if _, err = s.ReadLive(t.Context(), "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("missing ownership verifier confirmed execution", err)
	}
	s.CheckOwner = func(_ context.Context, _ string, _ int64) error { return nil }
	*now = now.Add(31 * time.Second)
	if _, err = s.ReadControllerHealth(t.Context(), "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("expired report confirmed readiness", err)
	}
}
