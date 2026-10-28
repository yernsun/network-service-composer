-- +goose Up
ALTER TABLE runtimes ADD COLUMN management_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE services ADD COLUMN service_key TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN runtime_object_id TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN application_id TEXT NULL;
ALTER TABLE services ADD COLUMN managed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_eligible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_issues TEXT NOT NULL DEFAULT '[]';
UPDATE services SET service_key=external_id, runtime_object_id=external_id WHERE service_key='';
CREATE UNIQUE INDEX uq_services_stable_key ON services(runtime_id, service_key);
CREATE INDEX idx_services_application ON services(application_id);

ALTER TABLE routes ADD COLUMN route_status TEXT NOT NULL DEFAULT 'ACTIVE';

CREATE TABLE catalog_sources (
    id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, source_type TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '', pinned_ref TEXT NOT NULL DEFAULT '', trusted BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, last_synced_at TIMESTAMP NULL, last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);

CREATE TABLE catalog_app_versions (
    id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES catalog_sources(id), app_key TEXT NOT NULL,
    name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', version TEXT NOT NULL, commit_ref TEXT NOT NULL DEFAULT '',
    artifact_digest TEXT NOT NULL, compose_sha256 TEXT NOT NULL, manifest_json TEXT NOT NULL DEFAULT '',
    compose_yaml TEXT NOT NULL, env_example TEXT NOT NULL DEFAULT '', readme TEXT NOT NULL DEFAULT '', icon_url TEXT NOT NULL DEFAULT '',
    services_json TEXT NOT NULL DEFAULT '[]', compose_compatible BOOLEAN NOT NULL DEFAULT TRUE,
    swarm_compatible BOOLEAN NOT NULL DEFAULT TRUE, compatibility_issues TEXT NOT NULL DEFAULT '[]',
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
    UNIQUE(source_id, app_key, version)
);
CREATE INDEX idx_catalog_versions_app ON catalog_app_versions(app_key, version);

CREATE TABLE applications (
    id TEXT PRIMARY KEY, runtime_id TEXT NOT NULL REFERENCES runtimes(id), name TEXT NOT NULL,
    project_name TEXT NOT NULL, registry_credential_ref TEXT NOT NULL DEFAULT '', catalog_app_version_id TEXT NULL REFERENCES catalog_app_versions(id),
    active_revision_id TEXT NOT NULL DEFAULT '', desired_revision_id TEXT NOT NULL DEFAULT '', replica_overrides TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL,
    managed BOOLEAN NOT NULL DEFAULT TRUE, last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL, UNIQUE(runtime_id, project_name)
);
CREATE INDEX idx_applications_runtime ON applications(runtime_id);

CREATE TABLE application_revisions (
    id TEXT PRIMARY KEY, application_id TEXT NOT NULL REFERENCES applications(id), sequence BIGINT NOT NULL,
    parent_revision_id TEXT NOT NULL DEFAULT '', catalog_app_version_id TEXT NOT NULL DEFAULT '',
    compose_sha256 TEXT NOT NULL, environment_sha256 TEXT NOT NULL, resolved_sha256 TEXT NOT NULL,
    environment_keys TEXT NOT NULL DEFAULT '[]', secret_keys TEXT NOT NULL DEFAULT '[]',
    encrypted_compose BLOB NOT NULL, encrypted_environment BLOB NOT NULL, encrypted_resolved BLOB NOT NULL,
    encryption_key_id TEXT NOT NULL, created_by TEXT NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL,
    UNIQUE(application_id, sequence)
);
CREATE INDEX idx_application_revisions_app ON application_revisions(application_id, sequence);

CREATE TABLE deployment_operations (
    id TEXT PRIMARY KEY, application_id TEXT NOT NULL DEFAULT '', runtime_id TEXT NOT NULL REFERENCES runtimes(id),
    service_id TEXT NOT NULL DEFAULT '', revision_id TEXT NOT NULL DEFAULT '', operation_type TEXT NOT NULL,
    status TEXT NOT NULL, idempotency_key TEXT NULL, request_sha256 TEXT NOT NULL DEFAULT '', encrypted_input BLOB NULL, encryption_key_id TEXT NOT NULL DEFAULT '',
    redacted_log TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0, worker_id TEXT NOT NULL DEFAULT '', heartbeat_at TIMESTAMP NULL,
    started_at TIMESTAMP NULL, finished_at TIMESTAMP NULL, duration_ms BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_operations_queue ON deployment_operations(status, created_at);
CREATE INDEX idx_operations_application ON deployment_operations(application_id, created_at);
CREATE UNIQUE INDEX uq_operations_idempotency ON deployment_operations(runtime_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP TABLE deployment_operations;
DROP TABLE application_revisions;
DROP TABLE applications;
DROP TABLE catalog_app_versions;
DROP TABLE catalog_sources;
DROP INDEX idx_services_application;
DROP INDEX uq_services_stable_key;
ALTER TABLE routes DROP COLUMN route_status;
ALTER TABLE services DROP COLUMN routing_issues;
ALTER TABLE services DROP COLUMN routing_eligible;
ALTER TABLE services DROP COLUMN managed;
ALTER TABLE services DROP COLUMN application_id;
ALTER TABLE services DROP COLUMN runtime_object_id;
ALTER TABLE services DROP COLUMN service_key;
ALTER TABLE runtimes DROP COLUMN management_enabled;
