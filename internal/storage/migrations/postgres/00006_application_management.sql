-- +goose Up
ALTER TABLE runtimes ADD COLUMN management_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE services ADD COLUMN service_key VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN runtime_object_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN application_id VARCHAR(64) NULL;
ALTER TABLE services ADD COLUMN managed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_eligible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_issues TEXT NOT NULL DEFAULT '[]';
UPDATE services SET service_key=external_id, runtime_object_id=external_id WHERE service_key='';
CREATE UNIQUE INDEX uq_services_stable_key ON services(runtime_id, service_key);
CREATE INDEX idx_services_application ON services(application_id);

ALTER TABLE routes ADD COLUMN route_status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE';

CREATE TABLE catalog_sources (
    id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE, source_type VARCHAR(32) NOT NULL,
    url TEXT NOT NULL DEFAULT '', pinned_ref VARCHAR(255) NOT NULL DEFAULT '', trusted BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, last_synced_at TIMESTAMPTZ NULL, last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE catalog_app_versions (
    id VARCHAR(64) PRIMARY KEY, source_id VARCHAR(64) NOT NULL REFERENCES catalog_sources(id), app_key VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL, description TEXT NOT NULL DEFAULT '', version VARCHAR(128) NOT NULL, commit_ref VARCHAR(255) NOT NULL DEFAULT '',
    artifact_digest VARCHAR(128) NOT NULL, compose_sha256 VARCHAR(64) NOT NULL, manifest_json TEXT NOT NULL DEFAULT '', compose_yaml TEXT NOT NULL,
    env_example TEXT NOT NULL DEFAULT '', readme TEXT NOT NULL DEFAULT '', icon_url TEXT NOT NULL DEFAULT '', services_json TEXT NOT NULL DEFAULT '[]',
    compose_compatible BOOLEAN NOT NULL DEFAULT TRUE, swarm_compatible BOOLEAN NOT NULL DEFAULT TRUE,
    compatibility_issues TEXT NOT NULL DEFAULT '[]', revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, UNIQUE(source_id, app_key, version)
);
CREATE INDEX idx_catalog_versions_app ON catalog_app_versions(app_key, version);
CREATE TABLE applications (
    id VARCHAR(64) PRIMARY KEY, runtime_id VARCHAR(64) NOT NULL REFERENCES runtimes(id), name VARCHAR(255) NOT NULL,
    project_name VARCHAR(255) NOT NULL, registry_credential_ref VARCHAR(255) NOT NULL DEFAULT '', catalog_app_version_id VARCHAR(64) NULL REFERENCES catalog_app_versions(id),
    active_revision_id VARCHAR(64) NOT NULL DEFAULT '', desired_revision_id VARCHAR(64) NOT NULL DEFAULT '', replica_overrides TEXT NOT NULL DEFAULT '{}', status VARCHAR(32) NOT NULL,
    managed BOOLEAN NOT NULL DEFAULT TRUE, last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, UNIQUE(runtime_id, project_name)
);
CREATE INDEX idx_applications_runtime ON applications(runtime_id);
CREATE TABLE application_revisions (
    id VARCHAR(64) PRIMARY KEY, application_id VARCHAR(64) NOT NULL REFERENCES applications(id), sequence BIGINT NOT NULL,
    parent_revision_id VARCHAR(64) NOT NULL DEFAULT '', catalog_app_version_id VARCHAR(64) NOT NULL DEFAULT '',
    compose_sha256 VARCHAR(64) NOT NULL, environment_sha256 VARCHAR(64) NOT NULL, resolved_sha256 VARCHAR(64) NOT NULL,
    environment_keys TEXT NOT NULL DEFAULT '[]', secret_keys TEXT NOT NULL DEFAULT '[]', encrypted_compose BYTEA NOT NULL,
    encrypted_environment BYTEA NOT NULL, encrypted_resolved BYTEA NOT NULL, encryption_key_id VARCHAR(255) NOT NULL,
    created_by VARCHAR(255) NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL, UNIQUE(application_id, sequence)
);
CREATE INDEX idx_application_revisions_app ON application_revisions(application_id, sequence);
CREATE TABLE deployment_operations (
    id VARCHAR(64) PRIMARY KEY, application_id VARCHAR(64) NOT NULL DEFAULT '', runtime_id VARCHAR(64) NOT NULL REFERENCES runtimes(id),
    service_id VARCHAR(64) NOT NULL DEFAULT '', revision_id VARCHAR(64) NOT NULL DEFAULT '', operation_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL, idempotency_key VARCHAR(255) NULL, request_sha256 VARCHAR(64) NOT NULL DEFAULT '', encrypted_input BYTEA NULL, encryption_key_id VARCHAR(255) NOT NULL DEFAULT '',
    redacted_log TEXT NOT NULL DEFAULT '', error_code VARCHAR(128) NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0, worker_id VARCHAR(255) NOT NULL DEFAULT '', heartbeat_at TIMESTAMPTZ NULL,
    started_at TIMESTAMPTZ NULL, finished_at TIMESTAMPTZ NULL, duration_ms BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_operations_queue ON deployment_operations(status, created_at);
CREATE INDEX idx_operations_application ON deployment_operations(application_id, created_at);
CREATE UNIQUE INDEX uq_operations_idempotency ON deployment_operations(runtime_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP TABLE deployment_operations, application_revisions, applications, catalog_app_versions, catalog_sources;
DROP INDEX idx_services_application;
DROP INDEX uq_services_stable_key;
ALTER TABLE routes DROP COLUMN route_status;
ALTER TABLE services DROP COLUMN routing_issues, DROP COLUMN routing_eligible, DROP COLUMN managed, DROP COLUMN application_id, DROP COLUMN runtime_object_id, DROP COLUMN service_key;
ALTER TABLE runtimes DROP COLUMN management_enabled;
