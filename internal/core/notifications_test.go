package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type recordingSender struct {
	mu     sync.Mutex
	events []string
	result notification.Result
	block  func()
}

func (s *recordingSender) Send(_ context.Context, endpoint, secret, event string, body []byte, _ time.Time) notification.Result {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	if s.block != nil {
		s.block()
	}
	return s.result
}
func notificationFixture(t *testing.T, groups ...string) (*Core, NotificationSettings) {
	t.Helper()
	c, _, _ := controlFixture(t)
	now := time.Now().UTC()
	c.now = func() time.Time { return now }
	endpoint, secret := "https://receiver.example.test/hook?token=private-endpoint-token", "private-signing-secret"
	settings, err := c.SaveNotificationSettings(context.Background(), NotificationSettingsInput{Enabled: true, Events: groups, Endpoint: &endpoint, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, settings
}
func notices(t *testing.T, c *Core) []Notification {
	t.Helper()
	rows, err := c.ListNotifications(context.Background(), storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestNotificationSettingsValidationEncryptionAndRecovery(t *testing.T) {
	c, p := notificationFixture(t, "images", "deployments")
	ctx := context.Background()
	if !p.Enabled || !p.SecretConfigured || !p.EndpointConfigured || p.EndpointHost != "receiver.example.test" {
		t.Fatal(p)
	}
	doc, err := c.store.GetControlDocument(ctx, notificationSettingsKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc.Payload), "private-") || strings.Contains(string(doc.Payload), "https:") {
		t.Fatal("notification credentials stored in plaintext")
	}
	if _, err = c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, 0); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	for _, input := range []NotificationSettingsInput{{Enabled: true, Events: []string{"unknown"}}, {Events: []string{"images", "images"}}, {Enabled: true}} {
		if _, err = c.SaveNotificationSettings(ctx, input, p.Revision); err == nil {
			t.Fatal("invalid policy accepted", input)
		}
	}
	for _, secret := range []string{"short", strings.Repeat("a", 257)} {
		if _, err = c.SaveNotificationSettings(ctx, NotificationSettingsInput{Secret: &secret}, p.Revision); err == nil {
			t.Fatal("invalid secret accepted")
		}
	}
	endpoint := "http://private.invalid"
	if _, err = c.SaveNotificationSettings(ctx, NotificationSettingsInput{Endpoint: &endpoint}, p.Revision); err == nil {
		t.Fatal("unsafe endpoint accepted")
	}
	if _, err = c.ReencryptControlSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	var restored notificationSettings
	if _, err = c.readEncrypted(ctx, notificationSettingsKey, notificationSettingsKind, &restored); err != nil || restored.Secret != "private-signing-secret" {
		t.Fatal("rotated credentials were lost", err)
	}
	updated, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"images"}}, p.Revision)
	if err != nil || !updated.SecretConfigured || !updated.EndpointConfigured {
		t.Fatal("blank update lost credentials", updated, err)
	}
	raw, _ := json.Marshal(updated)
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "token=") {
		t.Fatal("credentials leaked from read")
	}
}
func TestNotificationRetriesPersistIdentityAndRequireFreshRevision(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	if err := c.TestNotification(ctx, p.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Retryable: true, Code: "http_429", RetryAfter: time.Hour}}
	w := NotificationWorker{Core: c, Sender: sender}
	if err := w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	first := notices(t, c)[0]
	if first.Status != "PENDING" || first.Attempts != 1 || first.NextAttempt.Sub(c.now()) != time.Hour {
		t.Fatal(first)
	}
	if err := w.tick(ctx); err != nil || len(sender.events) != 1 {
		t.Fatal("backoff ignored", err)
	}
	if _, err := c.RetryNotification(ctx, first.ID, first.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		now := c.now().Add(time.Hour)
		c.now = func() time.Time { return now }
		if err := w.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	failed := notices(t, c)[0]
	if failed.Status != "FAILED" || failed.Attempts != 5 {
		t.Fatal(failed)
	}
	if _, err := c.RetryNotification(ctx, failed.ID, failed.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	retried, err := c.RetryNotification(ctx, failed.ID, failed.Revision)
	if err != nil || retried.Attempts != 0 {
		t.Fatal(retried, err)
	}
	sender.result = notification.Result{Delivered: true}
	if err := w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	delivered := notices(t, c)[0]
	if delivered.Status != "DELIVERED" || delivered.DeliveredAt == nil || len(sender.events) != 6 {
		t.Fatal(delivered, sender.events)
	}
	for _, event := range sender.events {
		if event != first.Event.ID {
			t.Fatal("retry changed event identity")
		}
	}
	if _, err := c.RetryNotification(ctx, delivered.ID, delivered.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
}
func TestNotificationLeaseCrashRecoveryAndSettingsChange(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	row.Status = "SENDING"
	row.Attempts = 1
	row.LeaseUntil = c.now().Add(time.Minute)
	if err := c.saveControl(ctx, row.ID, notificationDeliveryKind, "", row, row.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	w := NotificationWorker{Core: c, Sender: sender}
	if err := w.tick(ctx); err != nil || len(sender.events) != 0 {
		t.Fatal("active lease stolen", err)
	}
	now := c.now().Add(2 * time.Minute)
	c.now = func() time.Time { return now }
	if err := w.tick(ctx); err != nil || len(sender.events) != 1 {
		t.Fatal("expired lease not reclaimed", err)
	}
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	_, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	var cancelled Notification
	for _, row := range notices(t, c) {
		if row.Status == "CANCELLED" {
			cancelled = row
		}
	}
	if cancelled.ID == "" || len(sender.events) != 1 {
		t.Fatal("disabled receiver still received pending event")
	}
	if _, err := c.RetryNotification(ctx, cancelled.ID, cancelled.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if err := c.TestNotification(ctx, p.Revision+1); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	c.config.Controller.RecoveryMode = true
	if err := w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNotificationConcurrentWorkersOnlyOneSubmission(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	sender := &recordingSender{result: notification.Result{Delivered: true}, block: func() { close(entered); <-release }}
	worker := NotificationWorker{Core: c, Sender: sender}
	done := make(chan error, 1)
	go func() { done <- worker.tick(ctx) }()
	<-entered
	if err := worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(sender.events) != 1 || notices(t, c)[0].Status != "DELIVERED" {
		t.Fatal("concurrent workers duplicated delivery")
	}
}
func TestNotificationImagesAndOperationResultsDeduplicateAcrossRestart(t *testing.T) {
	c, p := notificationFixture(t, "images", "deployments")
	ctx := context.Background()
	// Durable candidates remain observable even after automatic submission.
	u := ImageUpdate{EnvironmentID: "env-example", Status: "FAILED"}
	if err := c.saveControl(ctx, "image-candidate", "image-update", u.EnvironmentID, u, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.collectNotifications(ctx, notificationSettings{Events: []string{"images"}, Since: c.now()}, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err := c.collectNotifications(ctx, notificationSettings{Events: []string{"images"}, Since: c.now()}, p.Revision); err != nil {
		t.Fatal(err)
	}
	if rows := notices(t, c); len(rows) != 2 {
		t.Fatal("duplicate or missing available/submission failure", rows)
	}
	// Old results are not replayed when a later policy activation starts.
	if err := c.imageNotifications(ctx, c.now().Add(time.Second), p.Revision); err != nil {
		t.Fatal(err)
	}
	if len(notices(t, c)) != 2 {
		t.Fatal("historical candidate replayed")
	}
	// The real operation list, not log parsing, supplies terminal results.
	base, env := imageEnvironmentFixture(t)
	base.now = c.now
	ep, secret := "https://receiver.example.test/hook", "operation-signing-secret"
	settings, err := base.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"deployments"}, Endpoint: &ep, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.store.GetApplication(ctx, env.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := base.store.ListDeploymentOperations(ctx, app.ID, storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	op := ops[0]
	for _, status := range []model.DeploymentOperationStatus{model.OperationSucceeded, model.OperationFailed, model.OperationRolledBack, model.OperationRollbackFailed} {
		op.Status = status
		now := base.now()
		op.FinishedAt = &now
		if _, err = base.store.(*storage.SQLStore).DB().ExecContext(ctx, "UPDATE deployment_operations SET status=?,finished_at=? WHERE id=?", op.Status, *op.FinishedAt, op.ID); err != nil {
			t.Fatal(err)
		}
		if err = base.deploymentNotifications(ctx, now, settings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices(t, base)) != 4 {
		t.Fatal("terminal deployment result missing")
	}
	if err = base.deploymentNotifications(ctx, base.now(), settings.Revision); err != nil || len(notices(t, base)) != 4 {
		t.Fatal("restart duplicated terminal events", err)
	}
}

func TestNotificationCertificateAndGatewayTransitions(t *testing.T) {
	c, p := notificationFixture(t, "certificates", "gateways")
	ctx := context.Background()
	pools, err := c.store.ListPools(ctx, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	cert := model.Certificate{ID: "cert-notice", PoolID: pools[0].ID, Provider: "acme", Status: model.CertificateError, PrimaryDomain: "app.example.test", LastError: "private diagnostic with secret", Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
	if err = c.store.CreateCertificate(ctx, &cert); err != nil {
		t.Fatal(err)
	}
	g := model.Gateway{ID: "gateway-notice", PoolID: pools[0].ID, Name: "notice", Enabled: true, Status: model.GatewayOffline, Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
	if err = c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	s, _, err := c.notificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil {
		t.Fatal(err)
	}
	if len(notices(t, c)) != 2 {
		t.Fatal("ongoing outage repeated")
	}
	// Unknown/disabled evidence does not claim recovery.
	g.Status = model.GatewayStale
	if err = c.store.UpdateGateway(ctx, &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	cert.Status = model.CertificatePending
	cert.LastError = ""
	if err = c.store.UpdateCertificate(ctx, &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	cert.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("unknown state resolved incident", err)
	}
	g.Status = model.GatewayReady
	g.Enabled = false
	if err = c.store.UpdateGateway(ctx, &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("disabled Gateway resolved incident", err)
	}
	g.Enabled = true
	if err = c.store.UpdateGateway(ctx, &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("missing heartbeat resolved an incident", err)
	}
	stale := c.now().Add(-time.Hour)
	if err = c.store.HeartbeatGateway(ctx, g.ID, g.DesiredRevision, g.AppliedProbeRevision, stale); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("stale heartbeat resolved an incident", err)
	}
	future := c.now().Add(time.Hour)
	if err = c.store.HeartbeatGateway(ctx, g.ID, g.DesiredRevision, g.AppliedProbeRevision, future); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("future heartbeat resolved an incident", err)
	}
	if err = c.store.HeartbeatGateway(ctx, g.ID, g.DesiredRevision, g.AppliedProbeRevision, c.now()); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	expires := c.now().Add(90 * 24 * time.Hour)
	cert.Status = model.CertificateActive
	cert.NotAfter = &expires
	validFrom := c.now().Add(-time.Hour)
	cert.NotBefore = &validFrom
	if err = c.store.UpdateCertificate(ctx, &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	cert.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 4 {
		t.Fatal("recovery event missing", err)
	}
	cert.Status = model.CertificateRenewing
	cert.LastError = "renewal failed private credentials"
	if err = c.store.UpdateCertificate(ctx, &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	cert.Revision++
	g, err = c.store.GetGateway(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	g.Status = model.GatewayOffline
	if err = c.store.UpdateGateway(ctx, &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	g.Revision++
	if err = c.collectNotifications(ctx, s, p.Revision); err != nil || len(notices(t, c)) != 6 {
		t.Fatal("recurrence event missing", err)
	}
	raw, _ := json.Marshal(notices(t, c))
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "credentials") {
		t.Fatal("failure diagnostics leaked")
	}
}

type notificationFailStore struct {
	storage.Store
	read  bool
	write bool
	kind  string
}

var errNoticeStore = errors.New("notification store unavailable")

func (s notificationFailStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.read {
		return storage.ControlDocument{}, errNoticeStore
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s notificationFailStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if s.write {
		return errNoticeStore
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}
func (s notificationFailStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if s.kind == kind {
		return nil, errNoticeStore
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func TestNotificationStorageFailureDoesNotLoseQueuedEvents(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	original := c.store
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	// Source enumeration failure cannot block a previously queued delivery.
	c.store = notificationFailStore{Store: original, kind: "image-update"}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	if err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx); !errors.Is(err, errNoticeStore) || len(sender.events) != 1 {
		t.Fatal("source failure hid send result", err)
	}
	c.store = notificationFailStore{Store: original, read: true}
	if _, err := c.GetNotificationSettings(ctx); !errors.Is(err, errNoticeStore) {
		t.Fatal("read error hidden", err)
	}
	if err := (NotificationWorker{Core: c}).tick(ctx); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = notificationFailStore{Store: original, write: true}
	before := len(notices(t, c))
	e := NotificationEvent{ID: "activation", Type: "gateway.offline", ResourceID: "gateway-atomic"}
	if err := c.observeNotification(ctx, e, true, p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = original
	if _, err := c.store.GetControlDocument(ctx, "notice-state:"+digestString(e.ID+":"+e.ResourceID)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("failed enqueue advanced observation")
	}
	if err := c.observeNotification(ctx, e, true, p.Revision); err != nil {
		t.Fatal("retry lost incident", err)
	}
	if len(notices(t, c)) != before {
		t.Fatal("unselected event group was delivered")
	}
	key := digestString("notice-state:" + digestString(e.ID+":"+e.ResourceID) + ":1")
	if _, err := c.store.GetNotificationWork(ctx, notificationEventKind, key, "_routing"); err != nil {
		t.Fatal("atomic retry lost its durable source event", err)
	}
}
