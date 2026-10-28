-- +goose Up
ALTER TABLE deployment_operations ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_operations ADD COLUMN environment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_operations ADD COLUMN scope_snapshot BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE runtime_event_sources (
    id TEXT PRIMARY KEY, runtime_id TEXT NOT NULL DEFAULT '', cluster_id TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '', engine_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL, last_heartbeat_at TIMESTAMPTZ NOT NULL,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE runtime_events (
    id TEXT PRIMARY KEY, event_key TEXT NOT NULL UNIQUE, source_id TEXT NOT NULL,
    runtime_id TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', application_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '', environment_id TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '', instance_id TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL, actor_id TEXT NOT NULL DEFAULT '', action TEXT NOT NULL,
    category TEXT NOT NULL, status TEXT NOT NULL DEFAULT '', exit_code INTEGER NULL,
    oom_killed BOOLEAN NOT NULL DEFAULT FALSE, occurred_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL, gap_end_at TIMESTAMPTZ NULL
);
CREATE INDEX idx_runtime_events_time ON runtime_events(occurred_at DESC,id DESC);
CREATE INDEX idx_runtime_events_scope ON runtime_events(project_id,environment_id,occurred_at DESC);
CREATE INDEX idx_runtime_events_service ON runtime_events(service_id,occurred_at DESC);

-- +goose Down
DROP TABLE runtime_events, runtime_event_sources;
ALTER TABLE deployment_operations DROP COLUMN environment_id, DROP COLUMN project_id, DROP COLUMN scope_snapshot;
