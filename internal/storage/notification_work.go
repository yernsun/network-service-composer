package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// The projection contains scheduling and dedupe metadata, never receiver URLs,
// credentials or decrypted configuration. It commits with its source document.
func (s *SQLStore) projectNotificationWork(ctx context.Context, tx *sql.Tx, w ControlWrite) error {
	d := w.Document
	if d.Kind == "notification-routing" && !w.Remove && !w.Archive {
		_, err := tx.ExecContext(ctx, s.bind(`UPDATE notification_work SET eligible_at=:now WHERE kind='notification-suppression' AND status='PENDING'`), d.UpdatedAt)
		return err
	}
	if d.Kind != "notification-delivery" && d.Kind != "notification-event" && d.Kind != "notification-suppression" {
		return nil
	}
	if w.Remove || w.Archive {
		_, err := tx.ExecContext(ctx, s.bind(`DELETE FROM notification_work WHERE document_id=:id`), d.ID)
		return err
	}
	var v struct {
		EventKey    string    `json:"event_key"`
		ReceiverID  string    `json:"receiver_id"`
		Status      string    `json:"status"`
		NextAttempt time.Time `json:"next_attempt_at"`
		LeaseUntil  time.Time `json:"lease_until"`
		Event       struct {
			ID string `json:"id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(d.Payload, &v); err != nil {
		return err
	}
	if v.EventKey == "" {
		key := v.Event.ID
		if key == "" {
			key = d.ID
		}
		hash := sha256.Sum256([]byte(key))
		v.EventKey = hex.EncodeToString(hash[:])
	}
	if v.ReceiverID == "" {
		v.ReceiverID = "_routing"
		if d.Kind == "notification-delivery" {
			v.ReceiverID = "default"
		}
		if d.Kind == "notification-suppression" {
			v.ReceiverID = "_silence"
		}
	}
	var eligible any
	if v.Status == "PENDING" || v.Status == "SENDING" {
		at := v.NextAttempt
		if at.IsZero() {
			at = d.CreatedAt
		}
		if v.LeaseUntil.After(at) {
			at = v.LeaseUntil
		}
		eligible = at
	}
	// Delete/reinsert by the document's identity in the same transaction. MySQL's
	// generic duplicate-key upsert could otherwise update a different document
	// that owns the event/receiver dedupe key instead of rejecting the duplicate.
	if _, err := tx.ExecContext(ctx, s.bind(`DELETE FROM notification_work WHERE document_id=:id`), d.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, s.bind(`INSERT INTO notification_work(document_id,kind,event_key,receiver_id,status,eligible_at) VALUES(:id,:kind,:event,:receiver,:status,:eligible)`), d.ID, d.Kind, v.EventKey, v.ReceiverID, v.Status, eligible)
	return err
}

func (s *SQLStore) GetNotificationWork(ctx context.Context, kind, event, receiver string) (ControlDocument, error) {
	return scanNotificationDocument(s.db.QueryRowContext(ctx, s.bind(`SELECT d.id,d.kind,d.parent_id,d.payload,d.revision,d.created_at,d.updated_at FROM notification_work w JOIN control_documents d ON d.id=w.document_id WHERE w.kind=:kind AND w.event_key=:event AND w.receiver_id=:receiver`), kind, event, receiver))
}

type notificationDocumentScanner interface{ Scan(...any) error }

func scanNotificationDocument(row notificationDocumentScanner) (ControlDocument, error) {
	var d ControlDocument
	var payload string
	err := row.Scan(&d.ID, &d.Kind, &d.ParentID, &payload, &d.Revision, &d.CreatedAt, &d.UpdatedAt)
	d.Payload = json.RawMessage(payload)
	return d, mapError(err)
}

// Pick one due head per receiver. A large or slow recipient cannot hide another
// receiver beyond a history page, and callers can exclude their active lanes.
func (s *SQLStore) ListNotificationWork(ctx context.Context, kind string, now time.Time, exclude []string, limit int) ([]ControlDocument, error) {
	if limit < 1 || limit > 200 {
		limit = 200
	}
	args := []any{kind, now}
	filter := `w.kind=:kind AND w.eligible_at<=:now`
	filter += ` AND NOT EXISTS(SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier') AND NOT EXISTS(SELECT 1 FROM control_documents h WHERE h.id=` + s.concat("'recovery-hold:notification:'", "w.document_id") + `)`
	if len(exclude) > 0 {
		marks := make([]string, len(exclude))
		for i, id := range exclude {
			marks[i] = ":receiver"
			args = append(args, id)
		}
		filter += ` AND w.receiver_id NOT IN (` + strings.Join(marks, ",") + `)`
	}
	query := `SELECT d.id,d.kind,d.parent_id,d.payload,d.revision,d.created_at,d.updated_at FROM notification_work w JOIN control_documents d ON d.id=w.document_id WHERE ` + filter + ` ORDER BY w.eligible_at,w.document_id LIMIT :limit`
	if kind == "notification-delivery" {
		query = `SELECT d.id,d.kind,d.parent_id,d.payload,d.revision,d.created_at,d.updated_at FROM (SELECT w.document_id,w.eligible_at,ROW_NUMBER() OVER(PARTITION BY w.receiver_id ORDER BY w.eligible_at,w.document_id) AS lane_position FROM notification_work w WHERE ` + filter + `) q JOIN control_documents d ON d.id=q.document_id WHERE q.lane_position=1 ORDER BY q.eligible_at,q.document_id LIMIT :limit`
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ControlDocument{}
	for rows.Next() {
		d, err := scanNotificationDocument(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

type NotificationWorkCount struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

func (s *SQLStore) NotificationWorkCounts(ctx context.Context) ([]NotificationWorkCount, error) {
	query := `SELECT kind,work_status,COUNT(*) FROM (SELECT w.kind,CASE WHEN w.status IN ('PENDING','SENDING') AND (EXISTS(SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier') OR EXISTS(SELECT 1 FROM control_documents h WHERE h.id=` + s.concat("'recovery-hold:notification:'", "w.document_id") + `)) THEN 'HELD' ELSE w.status END AS work_status FROM notification_work w) q GROUP BY kind,work_status`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NotificationWorkCount{}
	for rows.Next() {
		var v NotificationWorkCount
		if err := rows.Scan(&v.Kind, &v.Status, &v.Count); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
