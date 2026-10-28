package cluster

import (
	"fmt"
	"testing"
	"time"
)

func TestMetricCapacityEvictsOldestAttemptOnly(t *testing.T) {
	now := time.Now().UTC()
	m := &Monitor{metrics: map[string]*series{}}
	m.makeRoom()
	if len(m.metrics) != 0 {
		t.Fatal("empty collection gained observations")
	}
	for i := 0; i < maxSeries-1; i++ {
		m.metrics[fmt.Sprint(i)] = &series{LastAttempt: now.Add(time.Duration(i) * time.Second)}
	}
	m.makeRoom()
	if len(m.metrics) != maxSeries-1 {
		t.Fatal("capacity evicted an observation prematurely")
	}
	m.metrics["oldest"] = &series{LastAttempt: now.Add(-time.Hour)}
	m.makeRoom()
	if _, ok := m.metrics["oldest"]; ok {
		t.Fatal("oldest attempt retained at capacity")
	}
	if len(m.metrics) != maxSeries-1 {
		t.Fatal("more than one observation discarded")
	}
	for i := 0; i < maxSeries-1; i++ {
		if m.metrics[fmt.Sprint(i)] == nil {
			t.Fatal("recent observation discarded", i)
		}
	}
}
