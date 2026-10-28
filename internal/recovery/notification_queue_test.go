package recovery

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryNotificationQueuesRequireExplicitReview(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "hold", true: "resume"}[resume], func(t *testing.T) {
			s, _, _ := recoveryFixture(t)
			now := time.Now().UTC()
			for _, kind := range []string{"notification-event", "notification-delivery", "notification-suppression"} {
				payload, _ := json.Marshal(map[string]any{"event_key": kind, "status": "PENDING", "next_attempt_at": now})
				doc := storage.ControlDocument{ID: kind, Kind: kind, Payload: payload, CreatedAt: now, UpdatedAt: now}
				if err := s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc}}); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := Prepare(t.Context(), s)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Items) != 3 {
				t.Fatal("restored notifications omitted", plan.Items)
			}
			for _, item := range plan.Items {
				if item.Kind != "notification" || item.Decision != "hold" {
					t.Fatal(item)
				}
			}
			counts, err := s.NotificationWorkCounts(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range counts {
				if count.Status != "HELD" {
					t.Fatal(count)
				}
			}
			plan.ActivationReviewed = true
			if resume {
				for i := range plan.Items {
					plan.Items[i].Decision = "resume"
				}
			}
			if err = Release(t.Context(), s, plan); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"notification-event", "notification-delivery", "notification-suppression"} {
				due, err := s.ListNotificationWork(t.Context(), kind, now.Add(time.Minute), nil, 4)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if resume {
					want = 1
				}
				if len(due) != want {
					t.Fatal("restored notification replay boundary", kind, len(due), want)
				}
			}
		})
	}
}

func TestRecoveryNotificationHistoryIsNotReplayedAndCorruptionBlocksReview(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "corrupt"}[corrupt], func(t *testing.T) {
			s, _, _ := recoveryFixture(t)
			now := time.Now().UTC()
			for _, state := range []string{"PENDING", "SENDING", "DELIVERED", "FAILED", "CANCELLED"} {
				payload, _ := json.Marshal(map[string]any{"status": state, "event_key": state, "receiver_id": "fixture"})
				if err := s.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: "history-" + state, Kind: "notification-delivery", Payload: payload, CreatedAt: now, UpdatedAt: now}}}); err != nil {
					t.Fatal(err)
				}
			}
			if corrupt {
				if _, err := s.DB().ExecContext(t.Context(), `UPDATE control_documents SET payload='{"status":12}' WHERE id='history-PENDING'`); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := Prepare(t.Context(), s)
			if corrupt {
				if err == nil {
					t.Fatal("corrupt restored task acquired a recovery decision")
				}
				return
			}
			if err != nil || len(plan.Items) != 2 {
				t.Fatal("terminal history was included for replay", plan.Items, err)
			}
			for _, item := range plan.Items {
				if item.State != "PENDING" && item.State != "SENDING" {
					t.Fatal(item)
				}
			}
		})
	}
}
