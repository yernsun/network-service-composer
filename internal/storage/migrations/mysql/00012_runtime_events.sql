-- +goose Up
ALTER TABLE deployment_operations ADD COLUMN project_id VARCHAR(255) NOT NULL DEFAULT '', ADD COLUMN environment_id VARCHAR(255) NOT NULL DEFAULT '', ADD COLUMN scope_snapshot BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE runtime_event_sources (
    id VARCHAR(255) PRIMARY KEY, runtime_id VARCHAR(255) NOT NULL DEFAULT '', cluster_id VARCHAR(255) NOT NULL DEFAULT '',
    node_id VARCHAR(255) NOT NULL DEFAULT '', engine_id VARCHAR(255) NOT NULL DEFAULT '', state VARCHAR(32) NOT NULL,
    first_seen_at TIMESTAMP(6) NOT NULL, last_heartbeat_at TIMESTAMP(6) NOT NULL,
    last_error VARCHAR(255) NOT NULL DEFAULT ''
);
CREATE TABLE runtime_events (
    id VARCHAR(255) PRIMARY KEY, event_key VARCHAR(255) NOT NULL UNIQUE, source_id VARCHAR(255) NOT NULL,
    runtime_id VARCHAR(255) NOT NULL, service_id VARCHAR(255) NOT NULL DEFAULT '', application_id VARCHAR(255) NOT NULL DEFAULT '',
    project_id VARCHAR(255) NOT NULL DEFAULT '', environment_id VARCHAR(255) NOT NULL DEFAULT '',
    node_id VARCHAR(255) NOT NULL DEFAULT '', instance_id VARCHAR(255) NOT NULL DEFAULT '',
    actor_type VARCHAR(32) NOT NULL, actor_id VARCHAR(255) NOT NULL DEFAULT '', action VARCHAR(80) NOT NULL,
    category VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL DEFAULT '', exit_code INT NULL,
    oom_killed BOOLEAN NOT NULL DEFAULT FALSE, occurred_at TIMESTAMP(6) NOT NULL,
    observed_at TIMESTAMP(6) NOT NULL, gap_end_at TIMESTAMP(6) NULL,
    INDEX idx_runtime_events_time (occurred_at DESC,id DESC),
    INDEX idx_runtime_events_scope (project_id,environment_id,occurred_at DESC),
    INDEX idx_runtime_events_service (service_id,occurred_at DESC)
);

-- +goose Down
DROP TABLE runtime_events;
DROP TABLE runtime_event_sources;
ALTER TABLE deployment_operations DROP COLUMN environment_id, DROP COLUMN project_id, DROP COLUMN scope_snapshot;
