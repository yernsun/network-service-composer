package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func setRuntimeNoticeState(t *testing.T, c *Core, enabled bool, status model.RuntimeStatus, seen *time.Time, diagnostic string) model.Runtime {
	t.Helper()
	rows, err := c.store.ListRuntimes(context.Background(), storage.Page{Limit: 200})
	if err != nil || len(rows) != 1 {
		t.Fatal("runtime fixture", err, len(rows))
	}
	var at any
	if seen != nil {
		at = *seen
	}
	if _, err := c.store.(*storage.SQLStore).DB().ExecContext(context.Background(), `UPDATE runtimes SET enabled=?,status=?,last_seen_at=?,last_error=? WHERE id=?`, enabled, status, at, diagnostic, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	value, err := c.store.GetRuntime(context.Background(), rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestRuntimeNotificationsClassifyEvidenceWithoutFalseRecovery(t *testing.T) {
	for _, scenario := range []string{"offline", "error", "stale", "missing heartbeat", "future heartbeat", "degraded", "initializing", "initializing error", "initializing expired", "disabled", "healthy"} {
		t.Run(scenario, func(t *testing.T) {
			c, p := notificationFixture(t, "runtimes")
			ctx := context.Background()
			now := c.now()
			status, enabled, diagnostic, seen := model.RuntimeOnline, true, "", &now
			switch scenario {
			case "offline":
				status = model.RuntimeOffline
			case "error":
				diagnostic = "private-runtime-diagnostic"
			case "stale":
				old := now.Add(-3 * c.config.Controller.Discovery.FullReconcile.Interval.Std())
				seen = &old
			case "missing heartbeat":
				seen = nil
			case "future heartbeat":
				future := now.Add(time.Minute)
				seen = &future
			case "degraded":
				status = model.RuntimeDegraded
			case "initializing", "initializing error", "initializing expired":
				status = model.RuntimeInitializing
			case "disabled":
				enabled = false
			}
			if scenario == "initializing error" {
				diagnostic = "initial discovery failed"
			}
			runtime := setRuntimeNoticeState(t, c, enabled, status, seen, diagnostic)
			if scenario == "initializing expired" {
				if _, err := c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE runtimes SET created_at=? WHERE id=?`, now.Add(-time.Hour), runtime.ID); err != nil {
					t.Fatal(err)
				}
			}
			settings, _, err := c.notificationSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.collectNotifications(ctx, settings, p.Revision); err != nil {
				t.Fatal(err)
			}
			rows := notices(t, c)
			if scenario == "healthy" || scenario == "disabled" || scenario == "initializing" {
				if len(rows) != 0 {
					t.Fatal("healthy or disabled runtime raised an incident")
				}
				return
			}
			if len(rows) != 1 || rows[0].Event.Type != "runtime.problem" || rows[0].Event.ResourceID != runtime.ID {
				t.Fatalf("problem classification: %+v", rows)
			}
			raw, _ := json.Marshal(rows[0])
			if strings.Contains(string(raw), runtime.Endpoint) || strings.Contains(string(raw), "private-runtime-diagnostic") {
				t.Fatal("runtime diagnostics leaked into notification")
			}
			if err := c.collectNotifications(ctx, settings, p.Revision); err != nil || len(notices(t, c)) != 1 {
				t.Fatal("repeated observation duplicated an incident", err)
			}
			setRuntimeNoticeState(t, c, true, model.RuntimeOnline, &now, "")
			if err := c.collectNotifications(ctx, settings, p.Revision); err != nil {
				t.Fatal(err)
			}
			types := map[string]int{}
			for _, row := range notices(t, c) {
				types[row.Event.Type]++
			}
			if types["runtime.problem"] != 1 || types["runtime.recovered"] != 1 {
				t.Fatal("recovery needs a fresh healthy observation", types)
			}
		})
	}
}
func TestRuntimeNotificationsKeepIncidentOpenAcrossMissingEvidenceAndDisable(t *testing.T) {
	c, p := notificationFixture(t, "runtimes")
	ctx := context.Background()
	now := c.now()
	setRuntimeNoticeState(t, c, true, model.RuntimeOffline, nil, "")
	if err := c.runtimeNotifications(ctx, now, p.Revision); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing", "future", "disabled", "error"} {
		enabled, diagnostic, seen := true, "", (*time.Time)(nil)
		switch scenario {
		case "future":
			future := now.Add(time.Hour)
			seen = &future
		case "disabled":
			enabled = false
			seen = &now
		case "error":
			diagnostic = "unavailable"
			seen = &now
		}
		setRuntimeNoticeState(t, c, enabled, model.RuntimeOnline, seen, diagnostic)
		if err := c.runtimeNotifications(ctx, now, p.Revision); err != nil || len(notices(t, c)) != 1 {
			t.Fatal("unverified recovery", scenario, err)
		}
	}
	setRuntimeNoticeState(t, c, true, model.RuntimeOnline, &now, "")
	if err := c.runtimeNotifications(ctx, now, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("fresh recovery not emitted", err)
	}
}

type runtimeNoticePages struct {
	storage.Store
	calls   []string
	failure error
	runtime model.Runtime
}

func (s *runtimeNoticePages) ListRuntimes(_ context.Context, page storage.Page) ([]model.Runtime, error) {
	s.calls = append(s.calls, page.After)
	if s.failure != nil {
		return nil, s.failure
	}
	if page.After != "" {
		return []model.Runtime{s.runtime}, nil
	}
	rows := make([]model.Runtime, 200)
	for i := range rows {
		rows[i].ID = fmt.Sprintf("disabled-%03d", i)
	}
	return rows, nil
}
func TestRuntimeNotificationsContinuePastDisabledPagesAndKeepDeliveryOnSourceFailure(t *testing.T) {
	c, p := notificationFixture(t, "runtimes")
	ctx := context.Background()
	runtime := setRuntimeNoticeState(t, c, true, model.RuntimeOffline, nil, "")
	base := c.store
	pages := &runtimeNoticePages{Store: base, runtime: runtime}
	c.store = pages
	if err := c.runtimeNotifications(ctx, c.now(), p.Revision); err != nil {
		t.Fatal(err)
	}
	if len(pages.calls) != 2 || pages.calls[1] != "disabled-199" || len(notices(t, c)) != 1 {
		t.Fatal("skipped the remaining enabled runtime", pages.calls)
	}
	// Source failure must not stop delivery of an already durable record.
	pages.failure = errors.New("runtime source unavailable")
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	if err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx); !errors.Is(err, pages.failure) {
		t.Fatal(err)
	}
	if len(sender.events) != 1 || notices(t, c)[0].Status != "DELIVERED" {
		t.Fatal("source failure blocked durable delivery")
	}
}
func TestRuntimeNotificationPersistenceFailureIsVisible(t *testing.T) {
	c, p := notificationFixture(t, "runtimes")
	setRuntimeNoticeState(t, c, true, model.RuntimeOffline, nil, "")
	c.store = notificationFailStore{Store: c.store, write: true}
	if err := c.runtimeNotifications(context.Background(), c.now(), p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
}

func TestRuntimeNotificationsAllowConfiguredIdleDiscoveryBudget(t *testing.T) {
	c, p := notificationFixture(t, "runtimes")
	ctx := context.Background()
	now := c.now()
	c.config.Controller.Discovery.FullReconcile.Interval = config.Duration(time.Second)
	recent := now.Add(-5 * time.Second)
	setRuntimeNoticeState(t, c, true, model.RuntimeOnline, &recent, "")
	if err := c.runtimeNotifications(ctx, now, p.Revision); err != nil || len(notices(t, c)) != 0 {
		t.Fatal("ignored stale-endpoint allowance", err)
	}
	old := now.Add(-c.config.Controller.Discovery.StaleEndpointTimeout.Std())
	setRuntimeNoticeState(t, c, true, model.RuntimeOnline, &old, "")
	if err := c.runtimeNotifications(ctx, now, p.Revision); err != nil || len(notices(t, c)) != 1 {
		t.Fatal("expired observation not reported", err)
	}
}
