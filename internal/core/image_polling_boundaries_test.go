package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestImagePollingRespectsLeasesRequestsAndFailureBackoff(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		policy                   ImagePolicy
		state                    ImageCheck
		mutate                   func(*Core, *Environment)
		expectRun, expectFailure bool
	}{
		{"disabled", ImagePolicy{}, ImageCheck{}, nil, false, false},
		{"webhook only idle", ImagePolicy{Enabled: true, Webhook: true, Services: []string{"web"}}, ImageCheck{}, nil, false, false},
		{"active lease", ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, ImageCheck{Requested: true, LeaseUntil: time.Now().Add(time.Hour)}, nil, false, false},
		{"future scheduled check", ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, ImageCheck{NextCheck: time.Now().Add(time.Hour)}, nil, false, false},
		{"explicit request overrides schedule", ImagePolicy{Enabled: true, Webhook: true, Services: []string{"web"}}, ImageCheck{Requested: true, NextCheck: time.Now().Add(time.Hour)}, nil, true, false},
		{"pinned images do not fetch", ImagePolicy{Enabled: true, Poll: true, Services: []string{"api", "web"}}, ImageCheck{}, nil, true, false},
		{"untracked service after baseline changes", ImagePolicy{Enabled: true, Poll: true, Services: []string{"missing"}}, ImageCheck{}, nil, true, true},
		{"unknown registry", ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, ImageCheck{}, func(_ *Core, v *Environment) { v.RegistryCredentialRef = "missing" }, true, true},
		{"disabled management", ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, ImageCheck{}, func(c *Core, _ *Environment) { c.config.Management.Enabled = false }, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, env := imageEnvironmentFixture(t)
			ctx := context.Background()
			now := c.now()
			c.now = func() time.Time { return now }
			tc.policy.IntervalSeconds = 60
			if err := c.saveControl(ctx, env.ID+":image-policy", "image-policy", env.ID, tc.policy, 0); err != nil {
				t.Fatal(err)
			}
			if err := c.saveControl(ctx, env.ID+":image-check", "image-check", env.ID, tc.state, 0); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(c, &env)
			}
			c.checkEnvironmentImages(ctx, env)
			state, err := c.GetImageCheck(ctx, env.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.expectRun {
				if state.Revision != 1 || state.LastChecked != nil {
					t.Fatalf("unscheduled check ran: %#v", state)
				}
				return
			}
			if state.LastChecked == nil || !state.LastChecked.Equal(now) || state.Worker != "" || !state.LeaseUntil.IsZero() || state.Requested {
				t.Fatalf("finished check retained lease or request: %#v", state)
			}
			if tc.expectFailure {
				if state.Error == "" || state.Failures != 1 || state.LastSuccess != nil || state.NextCheck.Before(now.Add(2*time.Minute)) {
					t.Fatalf("failure reported as success or retry too soon: %#v", state)
				}
			} else if state.Error != "" || state.Failures != 0 || state.LastSuccess == nil || state.NextCheck.Before(now.Add(time.Minute)) {
				t.Fatalf("successful pinned-image check lost evidence: %#v", state)
			}
			updates, err := c.ListImageUpdates(ctx, env.ID, storage.Page{Limit: 10})
			if err != nil || len(updates) != 0 {
				t.Fatalf("unchanged/failed check created candidate: %#v %v", updates, err)
			}
		})
	}
}

type imageControlStore struct {
	storage.Store
	read  func(context.Context, string) (storage.ControlDocument, error)
	write func(context.Context, []storage.ControlWrite) error
}

func (s *imageControlStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.read != nil {
		return s.read(ctx, key)
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s *imageControlStore) WriteControlDocuments(ctx context.Context, w []storage.ControlWrite) error {
	if s.write != nil {
		return s.write(ctx, w)
	}
	return s.Store.WriteControlDocuments(ctx, w)
}

func TestImageCheckRetriesConflictsButPropagatesOtherFailures(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	base := c.store
	for _, tc := range []struct {
		name          string
		failure       error
		expectedCalls int
		eventually    bool
	}{{"revision conflicts", storage.ErrPrecondition, 4, false}, {"creation conflicts", storage.ErrConflict, 4, false}, {"one concurrent update", storage.ErrPrecondition, 2, true}, {"unavailable store", errors.New("store unavailable"), 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c.store = &imageControlStore{Store: base, write: func(ctx context.Context, w []storage.ControlWrite) error {
				calls++
				if tc.eventually && calls > 1 {
					return base.WriteControlDocuments(ctx, w)
				}
				return tc.failure
			}}
			state, err := c.RequestImageCheck(ctx, env.ID)
			if calls != tc.expectedCalls {
				t.Fatalf("retry count=%d, want %d", calls, tc.expectedCalls)
			}
			if tc.eventually {
				if err != nil || !state.Requested {
					t.Fatalf("concurrent request lost: %#v %v", state, err)
				}
			} else if tc.failure == storage.ErrPrecondition || tc.failure == storage.ErrConflict {
				if !errors.Is(err, storage.ErrConflict) {
					t.Fatalf("unbounded conflict not reported: %v", err)
				}
			} else if !errors.Is(err, tc.failure) {
				t.Fatalf("storage failure lost: %v", err)
			}
		})
	}
	c.store = base
	for _, read := range []struct {
		name, key string
		run       func() error
	}{
		{"policy", env.ID + ":image-policy", func() error { _, e := c.GetImagePolicy(ctx, env.ID); return e }},
		{"check", env.ID + ":image-check", func() error { _, e := c.GetImageCheck(ctx, env.ID); return e }},
		{"hook", env.ID + ":hook", func() error { _, e := c.GetHook(ctx, env.ID); return e }},
	} {
		t.Run("corrupt "+read.name, func(t *testing.T) {
			c.store = &imageControlStore{Store: base, read: func(ctx context.Context, key string) (storage.ControlDocument, error) {
				if key == read.key {
					return storage.ControlDocument{ID: key, Kind: map[string]string{"policy": "image-policy", "check": "image-check", "hook": "hook"}[read.name], Payload: json.RawMessage(`{`)}, nil
				}
				return base.GetControlDocument(ctx, key)
			}}
			if err := read.run(); err == nil {
				t.Fatal("corrupt control record reported as a valid default")
			}
		})
	}
}

func TestImagePollingPreservesWebhookRequestArrivingDuringCheck(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	base := c.store
	if err := c.saveControl(ctx, env.ID+":image-policy", "image-policy", env.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}, IntervalSeconds: 300}, 0); err != nil {
		t.Fatal(err)
	}
	inserted := false
	c.store = &imageControlStore{Store: base, write: func(ctx context.Context, writes []storage.ControlWrite) error {
		if err := base.WriteControlDocuments(ctx, writes); err != nil {
			return err
		}
		for _, w := range writes {
			if w.Document.Kind != "image-check" || inserted {
				continue
			}
			var state ImageCheck
			if err := json.Unmarshal(w.Document.Payload, &state); err != nil {
				return err
			}
			if state.Worker == "" {
				continue
			}
			inserted = true
			current, err := base.GetControlDocument(ctx, w.Document.ID)
			if err != nil {
				return err
			}
			state.Requested = true
			payload, err := json.Marshal(state)
			if err != nil {
				return err
			}
			current.Payload = payload
			return base.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: current, Expected: current.Revision}})
		}
		return nil
	}}
	c.checkEnvironmentImages(ctx, env)
	state, err := c.GetImageCheck(ctx, env.ID)
	if err != nil || !inserted || !state.Requested || state.LastSuccess == nil || state.Worker != "" {
		t.Fatalf("concurrent webhook request overwritten: %#v %v", state, err)
	}
	if strings.Contains(state.Error, "conflict") {
		t.Fatal("successful merge left a false failure")
	}
}
