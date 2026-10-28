package storage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationWorkAcrossDatabases(t *testing.T) {
	for _, fixture := range []struct{ driver, dsn string }{{"sqlite", "file:" + filepath.Join(t.TempDir(), "queue.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"}, {"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")}} {
		t.Run(fixture.driver, func(t *testing.T) {
			if fixture.dsn == "" {
				t.Skip("isolated external store not configured")
			}
			s, err := storage.Open(fixture.driver, fixture.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			if err = s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			prefix := id.New("q")
			now := time.Now().UTC().Truncate(time.Microsecond)
			keys := []string{}
			document := func(name, kind, key, receiver, status string, due, lease time.Time) storage.ControlDocument {
				payload, err := json.Marshal(map[string]any{"event_key": prefix + key, "receiver_id": prefix + receiver, "status": status, "next_attempt_at": due, "lease_until": lease})
				if err != nil {
					t.Fatal(err)
				}
				identity := prefix + name
				keys = append(keys, identity)
				return storage.ControlDocument{ID: identity, Kind: kind, Payload: payload, CreatedAt: now, UpdatedAt: now}
			}
			t.Cleanup(func() {
				for _, key := range keys {
					d, err := s.GetControlDocument(context.Background(), key)
					if err == nil {
						_ = s.WriteControlDocuments(context.Background(), []storage.ControlWrite{{Document: d, Expected: d.Revision, Remove: true}})
					}
				}
			})
			a := document("a", "notification-delivery", "one", "slow", "PENDING", now, time.Time{})
			b := document("b", "notification-delivery", "two", "slow", "PENDING", now, time.Time{})
			c := document("c", "notification-delivery", "three", "fast", "SENDING", now, now.Add(time.Hour))
			d := document("d", "notification-delivery", "four", "other", "PENDING", now, time.Time{})
			if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: a}, {Document: b}, {Document: c}, {Document: d}}); err != nil {
				t.Fatal(err)
			}
			due, err := s.ListNotificationWork(t.Context(), "notification-delivery", now, nil, 200)
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, v := range due {
				found[v.ID] = true
			}
			if !found[a.ID] || !found[d.ID] || found[b.ID] || found[c.ID] {
				t.Fatal("per-receiver due selection or lease broken", found)
			}
			due, err = s.ListNotificationWork(t.Context(), "notification-delivery", now, []string{prefix + "slow"}, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range due {
				if v.ID == a.ID || v.ID == b.ID {
					t.Fatal("excluded active lane selected")
				}
			}
			got, err := s.GetNotificationWork(t.Context(), "notification-delivery", prefix+"one", prefix+"slow")
			if err != nil || got.ID != a.ID {
				t.Fatal(got, err)
			}
			duplicate := document("duplicate", "notification-delivery", "one", "slow", "PENDING", now, time.Time{})
			if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: duplicate}}); err == nil {
				t.Fatal("dedupe key accepted twice")
			}
			if _, err = s.GetControlDocument(t.Context(), duplicate.ID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("duplicate admission left a document", err)
			}
			got, err = s.GetNotificationWork(t.Context(), "notification-delivery", prefix+"one", prefix+"slow")
			if err != nil || got.ID != a.ID {
				t.Fatal("duplicate mutated original index", got, err)
			}
			a = got
			a.Payload = json.RawMessage(`{"status":"DELIVERED","event_key":"` + prefix + `one","receiver_id":"` + prefix + `slow"}`)
			if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: a, Expected: a.Revision}}); err != nil {
				t.Fatal(err)
			}
			counts, err := s.NotificationWorkCounts(t.Context())
			if err != nil || len(counts) == 0 {
				t.Fatal(counts, err)
			}
			supp := document("supp", "notification-suppression", "scope", "", "PENDING", now.Add(time.Hour), time.Time{})
			if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: supp}}); err != nil {
				t.Fatal(err)
			}
			wake := storage.ControlDocument{ID: prefix + "wake", Kind: "notification-routing", Payload: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
			keys = append(keys, wake.ID)
			if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: wake}}); err != nil {
				t.Fatal(err)
			}
			due, err = s.ListNotificationWork(t.Context(), "notification-suppression", now, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, v := range due {
				if v.ID == supp.ID {
					seen = true
				}
			}
			if !seen {
				t.Fatal("routing mutation did not wake pending silence")
			}
			for _, kind := range []string{"notification-event", "notification-suppression"} {
				v := document(kind, kind, kind, "", "PENDING", now, time.Time{})
				if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: v}}); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ListNotificationWork(t.Context(), kind, now, nil, 201); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNotificationQueueOutageDoesNotReportEmptyHealthyQueue(t *testing.T) {
	s, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "unavailable.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListNotificationWork(t.Context(), "notification-delivery", time.Now(), nil, 4); err == nil {
		t.Fatal("unavailable queue looked empty")
	}
	if _, err = s.NotificationWorkCounts(t.Context()); err == nil {
		t.Fatal("unavailable queue looked healthy")
	}
	if _, err = s.GetNotificationWork(t.Context(), "notification-delivery", "event", "receiver"); err == nil {
		t.Fatal("unavailable dedupe authority was accepted")
	}
}

func TestNotificationWorkTenThousandHistoryRecords(t *testing.T) {
	for _, fixture := range []struct{ driver, dsn string }{{"sqlite", "file:" + filepath.Join(t.TempDir(), "scale.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"}, {"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")}} {
		t.Run(fixture.driver, func(t *testing.T) {
			if fixture.dsn == "" {
				t.Skip("isolated external store not configured")
			}
			notificationWorkTenThousandHistory(t, fixture.driver, fixture.dsn)
		})
	}
}

func notificationWorkTenThousandHistory(t *testing.T, driver, dsn string) {
	t.Helper()
	s, err := storage.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	prefix := id.New("scale") + "-"
	writes := make([]storage.ControlWrite, 0, 10002)
	for i := 0; i < 10002; i++ {
		status, receiver := "DELIVERED", "slow"
		if i == 10000 {
			status = "PENDING"
		}
		if i == 10001 {
			status, receiver = "PENDING", "fast"
		}
		payload, _ := json.Marshal(map[string]any{"event_key": fmt.Sprintf("event-%d", i), "receiver_id": receiver, "status": status, "next_attempt_at": now})
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: prefix + fmt.Sprintf("notice-%05d", i), Kind: "notification-delivery", Payload: payload, CreatedAt: now, UpdatedAt: now}})
	}
	if err = s.WriteControlDocuments(t.Context(), writes); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		removals := make([]storage.ControlWrite, 0, len(writes))
		for _, write := range writes {
			removals = append(removals, storage.ControlWrite{Document: storage.ControlDocument{ID: write.Document.ID, Kind: "notification-delivery"}, Expected: 1, Remove: true})
		}
		if err := s.WriteControlDocuments(context.Background(), removals); err != nil {
			t.Error("cleanup owned scale receipts", err)
		}
	})
	start := time.Now()
	due, err := s.ListNotificationWork(t.Context(), "notification-delivery", now, []string{"slow"}, 4)
	t.Logf("Indexed due query with 10,000 terminal receipts: %s", time.Since(start))
	if err != nil || len(due) != 1 || due[0].ID != prefix+"notice-10001" {
		t.Fatal("slow receiver or history blocked fast lane", due, err)
	}
	counts, err := s.NotificationWorkCounts(t.Context())
	if err != nil || len(counts) != 2 {
		t.Fatal(counts, err)
	}
	malformed := storage.ControlDocument{ID: prefix + "invalid", Kind: "notification-delivery", Payload: json.RawMessage(`{`), CreatedAt: now, UpdatedAt: now}
	if err = s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: malformed}}); err == nil {
		t.Fatal("malformed queue record admitted")
	}
	if _, err = s.GetControlDocument(t.Context(), malformed.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("failed projection was not rolled back", err)
	}
}
