-- +goose Up
ALTER TABLE certificates ADD COLUMN distribution_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE pool_snapshots (
    id VARCHAR(64) PRIMARY KEY,
    pool_id VARCHAR(64) NOT NULL,
    revision BIGINT NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    public_payload LONGBLOB NOT NULL,
    created_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_pool_snapshot(pool_id, revision),
    CONSTRAINT fk_snapshots_pool FOREIGN KEY(pool_id) REFERENCES gateway_pools(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE audit_events (
    id VARCHAR(64) PRIMARY KEY,
    actor VARCHAR(255) NOT NULL,
    action VARCHAR(128) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    resource_id VARCHAR(128) NOT NULL,
    request_id VARCHAR(128) NOT NULL,
    details TEXT NOT NULL,
    created_at TIMESTAMP(6) NOT NULL,
    INDEX idx_audit_events_created(created_at)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE audit_events;
DROP TABLE pool_snapshots;
ALTER TABLE certificates DROP COLUMN distribution_revision;
