-- +goose Up
ALTER TABLE certificates ADD COLUMN distribution_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE pool_snapshots (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES gateway_pools(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    public_payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(pool_id, revision)
);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    details TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_audit_events_created ON audit_events(created_at);

-- +goose Down
DROP TABLE audit_events;
DROP TABLE pool_snapshots;
ALTER TABLE certificates DROP COLUMN distribution_revision;
