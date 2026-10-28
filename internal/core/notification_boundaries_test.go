package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type notificationWriteHook struct {
	storage.Store
	afterClaim func()
}

func (s notificationWriteHook) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	err := s.Store.WriteControlDocuments(ctx, writes)
	for _, write := range writes {
		if err == nil && write.Document.Kind == notificationDeliveryKind {
			var row Notification
			if json.Unmarshal(write.Document.Payload, &row) == nil && row.Status == "SENDING" && s.afterClaim != nil {
				s.afterClaim()
			}
		}
	}
	return err
}
func TestNotificationReceiverEditAndCredentialReadFailureAfterClaim(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			c, p := notificationFixture(t, "images")
			ctx := context.Background()
			base := c.store
			if err := c.TestNotification(ctx, p.Revision); err != nil {
				t.Fatal(err)
			}
			c.store = notificationWriteHook{Store: base, afterClaim: func() {
				c.store = base
				if unreadable {
					c.store = notificationFailStore{Store: base, read: true}
					return
				}
				if _, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, p.Revision); err != nil {
					t.Fatal(err)
				}
			}}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx)
			if unreadable && !errors.Is(err, errNoticeStore) || !unreadable && err != nil {
				t.Fatal(err)
			}
			if len(sender.events) != 0 {
				t.Fatal("submitted with changed or unreadable receiver")
			}
			c.store = base
			row := notices(t, c)[0]
			if !unreadable && row.Status != "CANCELLED" || unreadable && row.Status != "SENDING" {
				t.Fatal(row)
			}
		})
	}
}
func TestNotificationReceiverAcceptanceWithLostPersistenceRemainsUnconfirmed(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	base := c.store
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}, block: func() { c.store = notificationFailStore{Store: base, write: true} }}
	if err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = base
	row := notices(t, c)[0]
	if row.Status != "SENDING" || row.DeliveredAt != nil {
		t.Fatal("unpersisted acceptance reported delivered")
	}
	now := c.now().Add(2 * time.Minute)
	c.now = func() time.Time { return now }
	sender.block = nil
	if err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.events) != 2 || sender.events[0] != sender.events[1] || notices(t, c)[0].Status != "DELIVERED" {
		t.Fatal("lost acknowledgement did not recover with stable identity")
	}
}
func TestNotificationAbandonedFinalAttemptRequiresExplicitRetry(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	row.Status = "SENDING"
	row.Attempts = 5
	if err := c.saveControl(ctx, row.ID, notificationDeliveryKind, "", row, row.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Code: "http_400"}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err := worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	row = notices(t, c)[0]
	if row.Status != "FAILED" || row.ErrorCode != "delivery_unconfirmed" || len(sender.events) != 0 {
		t.Fatal(row)
	}
	if _, err := c.RetryNotification(ctx, row.ID, row.Revision); err != nil {
		t.Fatal(err)
	}
	if err := worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if row = notices(t, c)[0]; row.Status != "FAILED" || row.ErrorCode != "http_400" || len(sender.events) != 1 {
		t.Fatal("permanent failure automatically repeated", row)
	}
}
func TestNotificationUnavailableConfigurationRefusesAdministrativeActions(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	base := c.store
	c.store = notificationFailStore{Store: base, read: true}
	if _, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	if err := c.TestNotification(ctx, p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	if _, err := c.RetryNotification(ctx, "missing", 1); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = notificationFailStore{Store: base, write: true}
	if _, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	if err := c.TestNotification(ctx, p.Revision); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = base
	if _, err := c.RetryNotification(ctx, "missing", 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestNotificationCorruptReceiptsAndSourceRecordsFailClosed(t *testing.T) {
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	bad := storage.ControlWrite{Document: storage.ControlDocument{ID: "bad-notification", Kind: notificationDeliveryKind, Payload: json.RawMessage(`{"status":"FAILED"}`), CreatedAt: c.now(), UpdatedAt: c.now()}}
	if err := c.store.WriteControlDocuments(ctx, []storage.ControlWrite{bad}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE control_documents SET payload='{' WHERE id='bad-notification'`); err != nil {
		t.Fatal(err)
	}
	bad.Document.Payload = json.RawMessage(`{`)
	if _, err := c.ListNotifications(ctx, storage.Page{}); err == nil {
		t.Fatal("corrupt delivery receipt accepted")
	}
	if err := (NotificationWorker{Core: c}).tick(ctx); err == nil {
		t.Fatal("worker ignored corrupt receipt")
	}
	bad.Document.ID = "bad-image"
	bad.Document.Kind = "image-update"
	if err := c.store.WriteControlDocuments(ctx, []storage.ControlWrite{bad}); err != nil {
		t.Fatal(err)
	}
	if err := c.imageNotifications(ctx, c.now(), p.Revision); err == nil {
		t.Fatal("malformed source became an event")
	}
	bad.Document.ID = "bad-state"
	bad.Document.Kind = "notification-state"
	e := NotificationEvent{ID: "state", ResourceID: "corrupt"}
	bad.Document.ID = "notice-state:" + digestString(e.ID+":"+e.ResourceID)
	if err := c.store.WriteControlDocuments(ctx, []storage.ControlWrite{bad}); err != nil {
		t.Fatal(err)
	}
	if err := c.observeNotification(ctx, e, true, p.Revision); err == nil {
		t.Fatal("corrupt incident state overwritten")
	}
}

type unavailableNotificationSources struct {
	storage.Store
	seen map[string]bool
}

func (s unavailableNotificationSources) ListCertificates(context.Context, storage.Page) ([]model.Certificate, error) {
	s.seen["certificates"] = true
	return nil, errNoticeStore
}
func (s unavailableNotificationSources) ListGateways(context.Context, storage.Page) ([]model.Gateway, error) {
	s.seen["gateways"] = true
	return nil, errNoticeStore
}
func (s unavailableNotificationSources) ListRuntimes(context.Context, storage.Page) ([]model.Runtime, error) {
	s.seen["runtimes"] = true
	return nil, errNoticeStore
}
func (s unavailableNotificationSources) ListDeploymentOperations(context.Context, string, storage.Page) ([]model.DeploymentOperation, error) {
	s.seen["deployments"] = true
	return nil, errNoticeStore
}
func (s unavailableNotificationSources) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if kind == "image-update" {
		s.seen["images"] = true
		return nil, errNoticeStore
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func TestNotificationCollectorsKeepOtherCategoriesAndQueuedDeliveriesIndependent(t *testing.T) {
	c, p := notificationFixture(t, notificationGroups...)
	ctx := context.Background()
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	c.store = unavailableNotificationSources{Store: c.store, seen: seen}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	if err := (NotificationWorker{Core: c, Sender: sender}).tick(ctx); !errors.Is(err, errNoticeStore) || len(seen) != len(notificationGroups) || len(sender.events) != 1 {
		t.Fatal("a source failure blocked independent processing", err, seen)
	}
	for _, group := range notificationGroups {
		if !seen[group] {
			t.Fatal("event source was not attempted", group)
		}
	}
}
func TestNotificationImageAndOutboxPaginationPreserveAllEvents(t *testing.T) {
	c, _ := notificationFixture(t, "images")
	ctx := context.Background()
	writes := []storage.ControlWrite{}
	for i := 0; i < 201; i++ {
		key := fmt.Sprintf("image-candidate-%03d", i)
		w, err := c.controlWrite(key, "image-update", "env-example", ImageUpdate{EnvironmentID: "env-example", Status: "AVAILABLE"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, w)
	}
	if err := c.store.WriteControlDocuments(ctx, writes); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err := worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.routeTick(ctx); err != nil {
		t.Fatal(err)
	}
	for len(sender.events) < 201 {
		before := len(sender.events)
		if err := worker.deliveryPass(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sender.events) == before {
			t.Fatal("indexed queue stopped before all candidates were delivered", before)
		}
	}
	if len(sender.events) != 201 {
		t.Fatal("pagination dropped events", len(sender.events))
	}
	if err := worker.tick(ctx); err != nil || len(sender.events) != 201 {
		t.Fatal("restart duplicated old events", err)
	}
	first, err := c.ListNotifications(ctx, storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ListNotifications(ctx, storage.Page{Limit: 200, After: first[len(first)-1].ID})
	if err != nil || len(second) != 1 || second[0].Status != "DELIVERED" {
		t.Fatal("delivery history lost page", err)
	}
}

func TestNotificationHistoryStartsWithRecentEventsAndExcludesChildTasks(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	endpoint, secret := "https://receiver.example.test/webhook", "notification-history-secret"
	p, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"deployments"}, Endpoint: &endpoint, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := c.now()
	earlier := NotificationEvent{ID: "earlier", Type: "image.available", ResourceID: "earlier", OccurredAt: now.Add(-time.Minute)}
	later := NotificationEvent{ID: "later", Type: "image.available", ResourceID: "later", OccurredAt: now}
	for _, event := range []NotificationEvent{earlier, later} {
		if err = c.enqueueNotification(ctx, event, p.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if rows := notices(t, c); rows[0].Event.ResourceID != "later" {
		t.Fatal("history hid recent notifications")
	}
	operations, err := c.store.ListDeploymentOperations(ctx, env.ApplicationID, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	op := operations[0]
	if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED',operation_type='DOMAIN_ATTACH',finished_at=? WHERE id=?", now, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.deploymentNotifications(ctx, now, p.Revision); err != nil || len(notices(t, c)) != 2 {
		t.Fatal("child task created a duplicate deployment success", err)
	}
}

func TestNotificationIncompleteReceiversAndExplicitCancelledRetry(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	if _, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"images"}}, 0); err == nil {
		t.Fatal("enabled without receiver")
	}
	endpoint := "https://receiver.example.test/hook"
	if _, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"images"}, Endpoint: &endpoint}, 0); err == nil {
		t.Fatal("enabled without signing secret")
	}
	disabled, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{}, 0)
	if err != nil || disabled.Enabled {
		t.Fatal(disabled, err)
	}
	secret := "notification-signing-secret"
	p, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"images"}, Endpoint: &endpoint, Secret: &secret}, disabled.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	original := notices(t, c)[0]
	p, err = c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"images"}}, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err = worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	if row.Status != "CANCELLED" || len(sender.events) != 0 {
		t.Fatal("old receiver revision submitted")
	}
	if _, err = c.RetryNotification(ctx, row.ID, row.Revision); err != nil {
		t.Fatal(err)
	}
	if err = worker.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.events) != 1 || sender.events[0] != original.Event.ID {
		t.Fatal("explicit retry lost event identity")
	}
}
func TestNotificationCertificateRiskClassificationAndInitialHealthyObservation(t *testing.T) {
	for _, tc := range []struct {
		status  model.CertificateStatus
		days    int
		problem bool
	}{{model.CertificateError, 0, true}, {model.CertificateExpired, 0, true}, {model.CertificateRevoked, 0, true}, {model.CertificateActive, 10, true}, {model.CertificateIssued, 90, false}, {model.CertificateActive, 0, false}} {
		t.Run(string(tc.status)+fmt.Sprint(tc.days), func(t *testing.T) {
			c, p := notificationFixture(t, "certificates")
			ctx := context.Background()
			pools, err := c.store.ListPools(ctx, storage.Page{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			cert := model.Certificate{ID: "certificate-risk", PoolID: pools[0].ID, Provider: "acme", Status: tc.status, PrimaryDomain: "app.example.test", Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
			if tc.days != 0 {
				expires := c.now().Add(time.Duration(tc.days) * 24 * time.Hour)
				cert.NotAfter = &expires
			}
			if err = c.store.CreateCertificate(ctx, &cert); err != nil {
				t.Fatal(err)
			}
			if err = c.certificateNotifications(ctx, c.now(), p.Revision); err != nil {
				t.Fatal(err)
			}
			rows := notices(t, c)
			if tc.problem && (len(rows) != 1 || rows[0].Event.Type != "certificate.problem") || !tc.problem && len(rows) != 0 {
				t.Fatal("incorrect certificate risk classification", rows)
			}
		})
	}
}

type notificationReadBarrier struct {
	storage.Store
	key     string
	mutex   sync.Mutex
	count   int
	ready   chan struct{}
	listing bool
}

func (s *notificationReadBarrier) wait() {
	s.mutex.Lock()
	s.count++
	if s.count == 2 {
		close(s.ready)
	}
	s.mutex.Unlock()
	<-s.ready
}
func (s *notificationReadBarrier) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	d, err := s.Store.GetControlDocument(ctx, key)
	if !s.listing && key == s.key {
		s.wait()
	}
	return d, err
}
func (s *notificationReadBarrier) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func (s *notificationReadBarrier) ListNotificationWork(ctx context.Context, kind string, now time.Time, exclude []string, limit int) ([]storage.ControlDocument, error) {
	rows, err := s.Store.ListNotificationWork(ctx, kind, now, exclude, limit)
	if s.listing && kind == notificationDeliveryKind {
		s.wait()
	}
	return rows, err
}
func TestNotificationConcurrentIncidentObservationAndClaims(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run("observation-"+fmt.Sprint(existing), func(t *testing.T) {
			c, p := notificationFixture(t, "gateways")
			ctx := context.Background()
			base := c.store
			e := NotificationEvent{ID: "activation", Type: "gateway.offline", ResourceID: "gateway-concurrent"}
			if existing {
				if err := c.observeNotification(ctx, e, false, p.Revision); err != nil {
					t.Fatal(err)
				}
			}
			c.store = &notificationReadBarrier{Store: base, key: "notice-state:" + digestString(e.ID+":"+e.ResourceID), ready: make(chan struct{})}
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() { results <- c.observeNotification(ctx, e, true, p.Revision) }()
			}
			for i := 0; i < 2; i++ {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			c.store = base
			if rows := notices(t, c); len(rows) != 1 {
				t.Fatal("concurrent observations duplicated incident", rows)
			}
		})
	}
	c, p := notificationFixture(t, "images")
	ctx := context.Background()
	base := c.store
	if err := c.TestNotification(ctx, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err := c.initializeNotificationRouting(ctx); err != nil {
		t.Fatal(err)
	}
	c.store = &notificationReadBarrier{Store: base, ready: make(chan struct{}), listing: true}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- worker.tick(ctx) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	c.store = base
	if len(sender.events) != 1 || notices(t, c)[0].Status != "DELIVERED" {
		t.Fatal("competing claims both sent an event")
	}
}

type notificationEventWriteFailure struct {
	storage.Store
	event string
}

func (s notificationEventWriteFailure) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	for _, w := range writes {
		if w.Document.Kind == notificationDeliveryKind {
			var v Notification
			if json.Unmarshal(w.Document.Payload, &v) == nil && v.Event.Type == s.event {
				return errNoticeStore
			}
		}
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}
func TestNotificationSourceEnqueueFailuresRemainRetryableWithoutStateLoss(t *testing.T) {
	for _, event := range []string{"image.available", "image.failed", "deployment.succeeded", "certificate.problem", "gateway.offline"} {
		t.Run(event, func(t *testing.T) {
			c, env := imageEnvironmentFixture(t)
			ctx := context.Background()
			pools, err := c.store.ListPools(ctx, storage.Page{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			endpoint, secret := "https://receiver.example.test/hook", "notification-enqueue-secret"
			p, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: notificationGroups, Endpoint: &endpoint, Secret: &secret}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.saveControl(ctx, "candidate-for-retry", "image-update", env.ID, ImageUpdate{EnvironmentID: env.ID, Status: "FAILED"}, 0); err != nil {
				t.Fatal(err)
			}
			operations, err := c.store.ListDeploymentOperations(ctx, env.ApplicationID, storage.Page{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			now := c.now()
			if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, "UPDATE deployment_operations SET finished_at=? WHERE id=?", now, operations[0].ID); err != nil {
				t.Fatal(err)
			}
			cert := model.Certificate{ID: "certificate-enqueue", PoolID: pools[0].ID, Provider: "acme", Status: model.CertificateError, PrimaryDomain: "app.example.test", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = c.store.CreateCertificate(ctx, &cert); err != nil {
				t.Fatal(err)
			}
			g := model.Gateway{ID: "gateway-enqueue", PoolID: pools[0].ID, Name: "enqueue", Enabled: true, Status: model.GatewayOffline, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = c.store.UpsertGateway(ctx, &g); err != nil {
				t.Fatal(err)
			}
			base := c.store
			c.store = notificationEventWriteFailure{Store: base, event: event}
			s, _, err := c.notificationSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.collectNotifications(ctx, s, p.Revision); !errors.Is(err, errNoticeStore) {
				t.Fatal("enqueue failure hidden", err)
			}
			c.store = base
			if err = c.collectNotifications(ctx, s, p.Revision); err != nil {
				t.Fatal(err)
			}
			types := map[string]bool{}
			for _, row := range notices(t, c) {
				types[row.Event.Type] = true
			}
			if !types[event] {
				t.Fatal("enqueue failure permanently lost event")
			}
		})
	}
}
func TestNotificationWorkerKeepsScanningAfterTransientFailure(t *testing.T) {
	c, _ := notificationFixture(t, "images")
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	base := c.store
	c.store = notificationFailStore{Store: base, read: true}
	// Cancellation after a failed first scan still exits cleanly, rather than
	// terminating the background service on a transient database failure.
	done := make(chan error, 1)
	go func() { done <- (NotificationWorker{Core: c}).Run(ctx) }()
	time.AfterFunc(20*time.Millisecond, cancel)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
