-- +goose Up
CREATE TABLE notification_work (
    document_id VARCHAR(180) PRIMARY KEY REFERENCES control_documents(id) ON DELETE CASCADE,
    kind VARCHAR(48) NOT NULL,
    event_key VARCHAR(64) NOT NULL,
    receiver_id VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL,
    eligible_at TIMESTAMP NULL,
    UNIQUE(kind,event_key,receiver_id)
);
CREATE INDEX idx_notification_due ON notification_work(kind,eligible_at,receiver_id,document_id);
CREATE INDEX idx_notification_status ON notification_work(kind,status);

-- +goose Down
DROP TABLE notification_work;
