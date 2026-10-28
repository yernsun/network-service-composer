package storage

import (
	"context"

	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *SQLStore) AppendAuditEvent(ctx context.Context, value model.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO audit_events(id,actor,action,resource_type,resource_id,request_id,details,created_at)
        VALUES(:id,:actor,:action,:resource_type,:resource_id,:request_id,:details,:created_at)`), value.ID, value.Actor, value.Action, value.ResourceType, value.ResourceID, value.RequestID, value.Details, value.CreatedAt)
	return mapError(err)
}

func (s *SQLStore) ListAuditEvents(ctx context.Context, page Page) ([]model.AuditEvent, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,actor,action,resource_type,resource_id,request_id,details,created_at FROM audit_events WHERE (:after_empty='' OR id<:after_id) ORDER BY id DESC LIMIT :limit`), page.After, page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []model.AuditEvent{}
	for rows.Next() {
		var v model.AuditEvent
		if err = rows.Scan(&v.ID, &v.Actor, &v.Action, &v.ResourceType, &v.ResourceID, &v.RequestID, &v.Details, &v.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
