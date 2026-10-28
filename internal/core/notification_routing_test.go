package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func routedFixture(t *testing.T) (*Core, NotificationSettings) {
	t.Helper()
	c, settings := notificationFixture(t, "runtimes")
	for i := 1; i <= 2; i++ {
		project, env := fmt.Sprintf("project-%d", i), fmt.Sprintf("env-%d", i)
		if err := c.saveControl(t.Context(), project, "project", "", Project{Name: project}, 0); err != nil {
			t.Fatal(err)
		}
		if err := c.saveControl(t.Context(), env, "environment", project, Environment{ProjectID: project, RuntimeID: "runtime-shared"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
	return c, settings
}
func routedReceiver(t *testing.T, c *Core, key string) NotificationReceiver {
	t.Helper()
	endpoint, secret := "https://"+key+".example.test/hook?private=credential", "private-signing-secret"
	v, err := c.SaveNotificationReceiver(t.Context(), key, NotificationReceiverInput{Name: key, Enabled: true, Endpoint: &endpoint, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func routedRule(t *testing.T, c *Core, scope, key string, receivers ...string) NotificationRoute {
	t.Helper()
	input := NotificationRouteInput{ScopeType: scope, ScopeID: key, Enabled: true, Events: []string{"runtimes"}, ReceiverIDs: receivers}
	if scope == "global" {
		current, err := c.globalNotificationRoute(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		input.BaseSettingsRevision = current.BaseSettingsRevision
	}
	v, err := c.SaveNotificationRoute(t.Context(), input, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func flushRoutes(t *testing.T, c *Core) {
	t.Helper()
	s, _, err := c.notificationRoutingState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{c.releaseSilencedNotifications(t.Context()), c.routePendingNotifications(t.Context(), s.Generation), c.rerouteCurrentNotifications(t.Context(), s.Generation)} {
		if err != nil {
			t.Fatal(err)
		}
	}
}
func routedProblem(t *testing.T, c *Core, bad bool) {
	t.Helper()
	s, _, err := c.notificationRoutingState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kind := "runtime.problem"
	if !bad {
		kind = "runtime.recovered"
	}
	if err := c.observeNotification(t.Context(), NotificationEvent{ID: s.Since.Format(time.RFC3339Nano), Type: kind, ResourceID: "runtime-shared", Status: "CONFIRMED"}, bad, 0); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationHierarchicalFallbackAndMultipleReceivers(t *testing.T) {
	c, _ := routedFixture(t)
	routedReceiver(t, c, "receiver_team")
	routedReceiver(t, c, "receiver_ops")
	project := routedRule(t, c, "project", "project-1", "receiver_team")
	env := routedRule(t, c, "environment", "env-1", "receiver_team", "receiver_ops")
	e := NotificationEvent{Type: "runtime.problem", Severity: "critical", ResourceID: "runtime-shared", OccurredAt: c.now()}
	if err := c.notificationScopes(t.Context(), &e); err != nil {
		t.Fatal(err)
	}
	matches, err := c.matchNotificationRoutes(t.Context(), e)
	if err != nil || len(matches) != 3 {
		t.Fatal(matches, err)
	}
	for _, m := range matches {
		if len(m.Scopes) != 1 {
			t.Fatal("scope leaked", m)
		}
	}
	env.Enabled = false
	if _, err = c.SaveNotificationRoute(t.Context(), env.NotificationRouteInput, env.Revision); err != nil {
		t.Fatal(err)
	}
	matches, err = c.matchNotificationRoutes(t.Context(), e)
	if err != nil || len(matches) != 2 || matches[0].Routes[0].ID != project.ID {
		t.Fatal(matches, err)
	}
	project.Severities = []string{"warning"}
	if _, err = c.SaveNotificationRoute(t.Context(), project.NotificationRouteInput, project.Revision); err != nil {
		t.Fatal(err)
	}
	matches, err = c.matchNotificationRoutes(t.Context(), e)
	if err != nil || len(matches) != 1 || len(matches[0].Scopes) != 2 || matches[0].ReceiverID != "default" {
		t.Fatal(matches, err)
	}
	// A stale/forged environment assignment never falls through to global.
	e.Scopes = []AlertScope{{ProjectID: "project-2", EnvironmentID: "env-1"}, {ProjectID: "project-1", EnvironmentID: "missing"}}
	if matches, err = c.matchNotificationRoutes(t.Context(), e); err != nil || len(matches) != 0 {
		t.Fatal(matches, err)
	}
}

func TestNotificationSharedDedupeOwnershipAndNewTargets(t *testing.T) {
	c, _ := routedFixture(t)
	routedReceiver(t, c, "receiver_team")
	rule := routedRule(t, c, "project", "project-1", "receiver_team")
	routedProblem(t, c, true)
	flushRoutes(t, c)
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	w := NotificationWorker{Core: c, Sender: sender}
	if err := w.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := notices(t, c)
	if len(rows) != 2 || len(sender.events) != 2 || sender.events[0] == sender.events[1] {
		t.Fatal("fanout identity or dedupe", rows)
	}
	for _, row := range rows {
		if row.Status != "DELIVERED" || len(row.Event.Scopes) != 1 || row.Event.EnvironmentID != row.Event.Scopes[0].EnvironmentID {
			t.Fatal(row)
		}
	}
	routedProblem(t, c, true)
	flushRoutes(t, c)
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 2 {
		t.Fatal("ongoing fault repeated", err)
	}
	routedReceiver(t, c, "receiver_new")
	rule.ReceiverIDs = append(rule.ReceiverIDs, "receiver_new")
	if _, err := c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, rule.Revision); err != nil {
		t.Fatal(err)
	}
	flushRoutes(t, c)
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 3 {
		t.Fatal("new receiver did not get one current occurrence", err)
	}
	flushRoutes(t, c)
	if len(notices(t, c)) != 3 {
		t.Fatal("current occurrence replayed")
	}
	preview, err := c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{IssueID: rows[0].Event.IssueID})
	if err != nil || len(preview.Matches) != 3 {
		t.Fatal(preview, err)
	}
	filtered, err := c.ListNotificationsFiltered(t.Context(), storage.Page{Limit: 1}, NotificationFilter{ReceiverID: "receiver_new", EnvironmentID: "env-1", Status: "DELIVERED", IssueID: rows[0].Event.IssueID})
	if err != nil || len(filtered) != 1 {
		t.Fatal(filtered, err)
	}
}

func TestNotificationSilenceSplitsSharedScopesAndOnlyReleasesActiveOccurrence(t *testing.T) {
	for _, recoverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(recoverFirst), func(t *testing.T) {
			c, _ := routedFixture(t)
			silence, err := c.SaveNotificationSilence(t.Context(), "silence_work", NotificationSilenceInput{ScopeType: "environment", ScopeID: "env-1", Reason: "maintenance"}, 0, "operator")
			if err != nil || silence.ExpiresAt.Sub(c.now()) != time.Hour {
				t.Fatal(silence, err)
			}
			routedProblem(t, c, true)
			flushRoutes(t, c)
			rows := notices(t, c)
			if len(rows) != 1 || len(rows[0].Event.Scopes) != 1 || rows[0].Event.Scopes[0].EnvironmentID != "env-2" {
				t.Fatal("silence hid another environment or disclosed muted scope", rows)
			}
			w := NotificationWorker{Core: c, Sender: &recordingSender{result: notification.Result{Delivered: true}}}
			if err := w.deliveryPass(t.Context()); err != nil {
				t.Fatal(err)
			}
			if recoverFirst {
				routedProblem(t, c, false)
				flushRoutes(t, c)
			}
			now := c.now().Add(2 * time.Hour)
			c.now = func() time.Time { return now }
			flushRoutes(t, c)
			if err := w.deliveryPass(t.Context()); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range notices(t, c) {
				if notificationProblem(row.Event.Type) {
					count++
				}
			}
			want := 2
			if recoverFirst {
				want = 1
			}
			if count != want {
				t.Fatal("expired silence replayed resolved fault or lost active fault", count)
			}
		})
	}
}

func TestNotificationQueuedSilenceCancellationAndReceiverTest(t *testing.T) {
	c, _ := routedFixture(t)
	routedProblem(t, c, true)
	flushRoutes(t, c)
	silence, err := c.SaveNotificationSilence(t.Context(), "silence_global", NotificationSilenceInput{ScopeType: "global", Reason: "maintenance"}, 0, "operator")
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	w := NotificationWorker{Core: c, Sender: sender}
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 0 {
		t.Fatal("queued fault bypassed maintenance", err)
	}
	flushRoutes(t, c)
	v := routedReceiver(t, c, "receiver_test")
	if err := c.TestNotificationReceiver(t.Context(), v.ID, v.Revision); err != nil {
		t.Fatal(err)
	}
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 1 {
		t.Fatal("explicit receiver test blocked by silence", err)
	}
	if _, err := c.CancelNotificationSilence(t.Context(), silence.ID, silence.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	flushRoutes(t, c)
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 2 {
		t.Fatal("cancelled maintenance did not wake active fault", err)
	}
}

type laneSender struct {
	mu        sync.Mutex
	entered   chan string
	release   chan struct{}
	active    map[string]int
	maximum   int
	violation bool
}

func (s *laneSender) Send(ctx context.Context, endpoint, secret, event string, body []byte, now time.Time) notification.Result {
	s.mu.Lock()
	s.active[endpoint]++
	sum := 0
	for _, n := range s.active {
		sum += n
		if n > 1 {
			s.violation = true
		}
	}
	s.maximum = max(s.maximum, sum)
	s.mu.Unlock()
	s.entered <- endpoint
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	s.mu.Lock()
	s.active[endpoint]--
	s.mu.Unlock()
	return notification.Result{Delivered: true}
}
func TestNotificationPersistentLaneLeasesAndBoundedConcurrency(t *testing.T) {
	c, _ := routedFixture(t)
	for i := 0; i < 5; i++ {
		v := routedReceiver(t, c, fmt.Sprintf("receiver_%d", i))
		for j := 0; j < 2; j++ {
			if err := c.TestNotificationReceiver(t.Context(), v.ID, v.Revision); err != nil {
				t.Fatal(err)
			}
		}
	}
	sender := &laneSender{entered: make(chan string, 10), release: make(chan struct{}), active: map[string]int{}}
	w := NotificationWorker{Core: c, Sender: sender}
	done := make(chan error, 2)
	go func() { done <- w.deliveryPass(t.Context()) }()
	for i := 0; i < 4; i++ {
		select {
		case <-sender.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("four channels did not start")
		}
	}
	// A competing pass may submit the fifth receiver, but cannot start another
	// request in any of the four durably leased receiver lanes.
	go func() { done <- w.deliveryPass(t.Context()) }()
	close(sender.release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if sender.violation || sender.maximum != 4 {
		t.Fatal("receiver concurrency violated", sender.maximum, sender.violation)
	}
}

func TestNotificationOwnershipChangeCancelsUnsentSharedPayload(t *testing.T) {
	c, _ := routedFixture(t)
	routedProblem(t, c, true)
	flushRoutes(t, c)
	env, err := c.GetEnvironment(t.Context(), "env-1")
	if err != nil {
		t.Fatal(err)
	}
	env.RuntimeID = "another-runtime"
	if err = c.saveControl(t.Context(), env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err = worker.deliveryPass(t.Context()); err != nil || len(sender.events) != 0 {
		t.Fatal("stale ownership leaked in an outbound request", err)
	}
	flushRoutes(t, c)
	if err = worker.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sender.events) != 1 {
		t.Fatal("safe surviving scope was lost", sender.events)
	}
	for _, row := range notices(t, c) {
		if row.Status == "DELIVERED" && (len(row.Event.Scopes) != 1 || row.Event.Scopes[0].EnvironmentID != "env-2") {
			t.Fatal("removed scope survived replacement", row)
		}
	}
}

func TestNotificationSourceFailureCannotBlockIndexedDelivery(t *testing.T) {
	c, _ := routedFixture(t)
	receiver := routedReceiver(t, c, "receiver_team")
	if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
		t.Fatal(err)
	}
	base := c.store
	c.store = unavailableNotificationSources{Store: base, seen: map[string]bool{}}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err := worker.tick(t.Context()); !errors.Is(err, errNoticeStore) || len(sender.events) != 1 {
		t.Fatal("collection failure blocked another receiver", err)
	}
	health, err := c.NotificationHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(health.Sources) != 2 || !health.Sources[0].Failed || health.Sources[0].SucceededAt != nil || !health.Sources[1].Failed {
		t.Fatal("failed collection looked healthy", health)
	}
	// Delivery selection must not enumerate terminal delivery history.
	c.store = notificationFailStore{Store: base, kind: notificationDeliveryKind}
	if err := worker.deliveryPass(t.Context()); err != nil {
		t.Fatal("indexed dispatch scanned history", err)
	}
}

func TestNotificationAddingRouteDoesNotReplayOldRecoveryActivity(t *testing.T) {
	c, _ := routedFixture(t)
	if err := c.enqueueNotification(t.Context(), NotificationEvent{ID: "old-recovery", Type: "runtime.recovered", ResourceID: "runtime-shared", Severity: "info", OccurredAt: c.now()}, 0); err != nil {
		t.Fatal(err)
	}
	now := c.now().Add(time.Hour)
	c.now = func() time.Time { return now }
	routedReceiver(t, c, "receiver_team")
	routedRule(t, c, "environment", "env-1", "receiver_team")
	flushRoutes(t, c)
	for _, row := range notices(t, c) {
		if row.ReceiverID == "receiver_team" {
			t.Fatal("route activation replayed a historical recovery", row)
		}
	}
	if len(notices(t, c)) != 1 || notices(t, c)[0].Event.Scopes[0].EnvironmentID != "env-2" {
		t.Fatal("old activity ignored first matching route", notices(t, c))
	}
}

type notificationAtomicHook struct {
	storage.Store
	afterClaim func()
}

func (s notificationAtomicHook) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	err := s.Store.WriteControlDocuments(ctx, writes)
	if err != nil {
		return err
	}
	for _, write := range writes {
		if write.Document.Kind == notificationDeliveryKind {
			var row Notification
			if json.Unmarshal(write.Document.Payload, &row) == nil && row.Status == "SENDING" {
				s.afterClaim()
				break
			}
		}
	}
	return nil
}
func TestNotificationRoutedClaimRechecksCredentialsAndRetainsLostReceipt(t *testing.T) {
	for _, failure := range []string{"changed", "unreadable", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			c, _ := routedFixture(t)
			receiver := routedReceiver(t, c, "receiver_team")
			if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
				t.Fatal(err)
			}
			base := c.store
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			if failure == "persistence" {
				sender.block = func() { c.store = notificationFailStore{Store: base, write: true} }
			} else {
				c.store = notificationAtomicHook{Store: base, afterClaim: func() {
					c.store = base
					if failure == "unreadable" {
						c.store = notificationFailStore{Store: base, read: true}
						return
					}
					if _, err := c.SaveNotificationReceiver(t.Context(), receiver.ID, NotificationReceiverInput{Name: "changed", Enabled: true}, receiver.Revision); err != nil {
						t.Fatal(err)
					}
				}}
			}
			err := (NotificationWorker{Core: c, Sender: sender}).deliveryPass(t.Context())
			if failure == "changed" && err != nil || failure != "changed" && !errors.Is(err, errNoticeStore) {
				t.Fatal(err)
			}
			c.store = base
			row := notices(t, c)[0]
			if failure == "changed" {
				if row.Status != "CANCELLED" || len(sender.events) != 0 {
					t.Fatal(row)
				}
			} else if row.Status != "SENDING" || row.DeliveredAt != nil {
				t.Fatal("unknown receipt was fabricated", row)
			}
		})
	}
}

func TestNotificationLegacyMigrationPreservesHistoryAndDoesNotReplaySuccess(t *testing.T) {
	c, settings := notificationFixture(t, "runtimes")
	legacy := NotificationEvent{ID: settingsEventSince(t, c), Type: "runtime.problem", ResourceID: "runtime-legacy", Status: "OFFLINE"}
	if err := c.observeNotification(t.Context(), legacy, true, settings.Revision); err != nil {
		t.Fatal(err)
	}
	old := notices(t, c)[0]
	old.Status = "DELIVERED"
	now := c.now()
	old.DeliveredAt = &now
	if err := c.saveControl(t.Context(), old.ID, notificationDeliveryKind, "", old, old.Revision); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(old.Event)
	if err := c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
	migrated := notices(t, c)[0]
	after, _ := json.Marshal(migrated.Event)
	if migrated.ID != old.ID || migrated.Status != "DELIVERED" || string(before) != string(after) || !migrated.Legacy {
		t.Fatal("migration rewrote history", migrated)
	}
	flushRoutes(t, c)
	if len(notices(t, c)) != 1 {
		t.Fatal("migration replayed a delivered occurrence")
	}
	routedReceiver(t, c, "receiver_new")
	routedRule(t, c, "global", "", "default", "receiver_new")
	flushRoutes(t, c)
	rows := notices(t, c)
	if len(rows) != 2 {
		t.Fatal("new target did not get legacy active fault once", rows)
	}
	flushRoutes(t, c)
	if len(notices(t, c)) != 2 {
		t.Fatal("legacy bootstrap repeated")
	}
}
func settingsEventSince(t *testing.T, c *Core) string {
	t.Helper()
	s, _, err := c.notificationSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s.Since.Format(time.RFC3339Nano)
}

func TestNotificationRoutingValidationSecretsAndCompatibility(t *testing.T) {
	c, settings := routedFixture(t)
	v := routedReceiver(t, c, "receiver_team")
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "https:") {
		t.Fatal("secret leaked", string(raw))
	}
	if _, err := c.ReencryptControlSecrets(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveNotificationReceiver(t.Context(), v.ID, NotificationReceiverInput{Name: "team", Enabled: true}, v.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	for _, input := range []NotificationReceiverInput{{Name: ""}, {Name: " team "}, {Name: "team", Endpoint: ptrNotice("http://127.0.0.1/hook")}, {Name: "team", Secret: ptrNotice("short")}, {Name: "team", Enabled: true}} {
		if _, err := c.SaveNotificationReceiver(t.Context(), "receiver_invalid", input, 0); err == nil {
			t.Fatal("invalid receiver accepted", input)
		}
	}
	global, err := c.globalNotificationRoute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	global.Enabled = true
	global.ReceiverIDs = []string{"default", v.ID}
	if _, err = c.SaveNotificationRoute(t.Context(), global.NotificationRouteInput, 0); err != nil {
		t.Fatal(err)
	}
	current, err := c.GetNotificationSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveNotificationSettings(t.Context(), NotificationSettingsInput{Events: []string{"runtimes"}}, current.Revision); err != nil {
		t.Fatal(err)
	}
	global, err = c.globalNotificationRoute(t.Context())
	if err != nil || !global.Enabled || len(global.ReceiverIDs) != 1 || global.ReceiverIDs[0] != v.ID {
		t.Fatal("legacy API disabled another team's receiver", global, err)
	}
	if err = c.TestNotificationReceiver(t.Context(), v.ID, v.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	if err = c.TestNotificationReceiver(t.Context(), "default", settings.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	for _, input := range []NotificationRouteInput{{ScopeType: "invalid"}, {ScopeType: "global", Enabled: true}, {ScopeType: "global", Events: []string{"unknown"}}, {ScopeType: "global", Severities: []string{"low"}}, {ScopeType: "global", ReceiverIDs: []string{v.ID, v.ID}}} {
		if _, err = c.SaveNotificationRoute(t.Context(), input, global.Revision); err == nil {
			t.Fatal("invalid rule accepted", input)
		}
	}
	for _, in := range []NotificationSilenceInput{{ScopeType: "global"}, {ScopeType: "global", Reason: "work", ExpiresAt: c.now().Add(8 * 24 * time.Hour)}, {ScopeType: "global", Reason: "work", ExpiresAt: c.now()}, {ScopeType: "issue", ScopeID: "missing", Reason: "work"}, {ScopeType: "global", Reason: "work", Events: []string{"unknown"}}} {
		if _, err = c.SaveNotificationSilence(t.Context(), "silence_invalid", in, 0, "operator"); err == nil {
			t.Fatal("invalid silence accepted", in)
		}
	}
	if _, err = c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{}); err == nil {
		t.Fatal("preview accepted arbitrary empty event")
	}
}
func ptrNotice(v string) *string { return &v }

func TestNotificationRoutedRetryAndLostReceipt(t *testing.T) {
	c, _ := routedFixture(t)
	v := routedReceiver(t, c, "receiver_team")
	if err := c.TestNotificationReceiver(t.Context(), v.ID, v.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Retryable: true, Code: "network_error"}}
	w := NotificationWorker{Core: c, Sender: sender}
	if err := w.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	if row.Status != "PENDING" || row.Attempts != 1 {
		t.Fatal(row)
	}
	for i := 0; i < 4; i++ {
		now := c.now().Add(2 * time.Hour)
		c.now = func() time.Time { return now }
		if err := w.deliveryPass(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	row = notices(t, c)[0]
	if row.Status != "FAILED" || row.Attempts != 5 {
		t.Fatal(row)
	}
	if _, err := c.RetryNotification(t.Context(), row.ID, row.Revision); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A sender callback cancels after accepting. The immutable event remains
	// SENDING until its lease expires rather than inventing a delivery receipt.
	ctx, cancel = context.WithCancel(context.Background())
	sender.result = notification.Result{Delivered: true}
	sender.block = cancel
	if err := w.deliveryPass(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	uncertain := notices(t, c)[0]
	if uncertain.Status != "SENDING" {
		t.Fatal(uncertain)
	}
	sender.block = nil
	now := c.now().Add(2 * time.Minute)
	c.now = func() time.Time { return now }
	if err := w.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if notices(t, c)[0].Status != "DELIVERED" {
		t.Fatal(notices(t, c))
	}
	for _, event := range sender.events {
		if event != row.Event.ID {
			t.Fatal("retry changed payload identity")
		}
	}
}
