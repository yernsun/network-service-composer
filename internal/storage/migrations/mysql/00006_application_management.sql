-- +goose Up
ALTER TABLE runtimes ADD COLUMN management_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE services ADD COLUMN service_key VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN runtime_object_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN application_id VARCHAR(64) NULL;
ALTER TABLE services ADD COLUMN managed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_eligible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE services ADD COLUMN routing_issues TEXT NULL;
UPDATE services SET service_key=external_id, runtime_object_id=external_id WHERE service_key='';
UPDATE services SET routing_issues='[]' WHERE routing_issues IS NULL;
ALTER TABLE services MODIFY COLUMN routing_issues TEXT NOT NULL;
CREATE UNIQUE INDEX uq_services_stable_key ON services(runtime_id, service_key);
CREATE INDEX idx_services_application ON services(application_id);

ALTER TABLE routes ADD COLUMN route_status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE';

CREATE TABLE catalog_sources (
    id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE, source_type VARCHAR(32) NOT NULL,
    url TEXT NOT NULL, pinned_ref VARCHAR(255) NOT NULL DEFAULT '', trusted BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, last_synced_at TIMESTAMP(6) NULL, last_error TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL
) ENGINE=InnoDB;
CREATE TABLE catalog_app_versions (
    id VARCHAR(64) PRIMARY KEY, source_id VARCHAR(64) NOT NULL, app_key VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL, description TEXT NOT NULL, version VARCHAR(128) NOT NULL, commit_ref VARCHAR(255) NOT NULL DEFAULT '',
    artifact_digest VARCHAR(128) NOT NULL, compose_sha256 VARCHAR(64) NOT NULL, manifest_json LONGTEXT NOT NULL, compose_yaml LONGTEXT NOT NULL,
    env_example LONGTEXT NOT NULL, readme LONGTEXT NOT NULL, icon_url TEXT NOT NULL, services_json LONGTEXT NOT NULL,
    compose_compatible BOOLEAN NOT NULL DEFAULT TRUE, swarm_compatible BOOLEAN NOT NULL DEFAULT TRUE, compatibility_issues LONGTEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_catalog_version(source_id, app_key, version), INDEX idx_catalog_versions_app(app_key, version),
    CONSTRAINT fk_catalog_version_source FOREIGN KEY(source_id) REFERENCES catalog_sources(id)
) ENGINE=InnoDB;
CREATE TABLE applications (
    id VARCHAR(64) PRIMARY KEY, runtime_id VARCHAR(64) NOT NULL, name VARCHAR(255) NOT NULL, project_name VARCHAR(255) NOT NULL,
    registry_credential_ref VARCHAR(255) NOT NULL DEFAULT '',
    catalog_app_version_id VARCHAR(64) NULL, active_revision_id VARCHAR(64) NOT NULL DEFAULT '', desired_revision_id VARCHAR(64) NOT NULL DEFAULT '',
    replica_overrides LONGTEXT NOT NULL, status VARCHAR(32) NOT NULL, managed BOOLEAN NOT NULL DEFAULT TRUE, last_error TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_application_project(runtime_id, project_name), INDEX idx_applications_runtime(runtime_id),
    CONSTRAINT fk_application_runtime FOREIGN KEY(runtime_id) REFERENCES runtimes(id),
    CONSTRAINT fk_application_catalog_version FOREIGN KEY(catalog_app_version_id) REFERENCES catalog_app_versions(id)
) ENGINE=InnoDB;
CREATE TABLE application_revisions (
    id VARCHAR(64) PRIMARY KEY, application_id VARCHAR(64) NOT NULL, sequence BIGINT NOT NULL,
    parent_revision_id VARCHAR(64) NOT NULL DEFAULT '', catalog_app_version_id VARCHAR(64) NOT NULL DEFAULT '',
    compose_sha256 VARCHAR(64) NOT NULL, environment_sha256 VARCHAR(64) NOT NULL, resolved_sha256 VARCHAR(64) NOT NULL,
    environment_keys LONGTEXT NOT NULL, secret_keys LONGTEXT NOT NULL, encrypted_compose LONGBLOB NOT NULL,
    encrypted_environment LONGBLOB NOT NULL, encrypted_resolved LONGBLOB NOT NULL, encryption_key_id VARCHAR(255) NOT NULL,
    created_by VARCHAR(255) NOT NULL DEFAULT '', created_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_application_revision(application_id, sequence), INDEX idx_application_revisions_app(application_id, sequence),
    CONSTRAINT fk_revision_application FOREIGN KEY(application_id) REFERENCES applications(id)
) ENGINE=InnoDB;
CREATE TABLE deployment_operations (
    id VARCHAR(64) PRIMARY KEY, application_id VARCHAR(64) NOT NULL DEFAULT '', runtime_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(64) NOT NULL DEFAULT '', revision_id VARCHAR(64) NOT NULL DEFAULT '', operation_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL, idempotency_key VARCHAR(255) NULL, request_sha256 VARCHAR(64) NOT NULL DEFAULT '', encrypted_input LONGBLOB NULL, encryption_key_id VARCHAR(255) NOT NULL DEFAULT '',
    redacted_log LONGTEXT NOT NULL, error_code VARCHAR(128) NOT NULL DEFAULT '', error_message TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0, worker_id VARCHAR(255) NOT NULL DEFAULT '', heartbeat_at TIMESTAMP(6) NULL,
    started_at TIMESTAMP(6) NULL, finished_at TIMESTAMP(6) NULL, duration_ms BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_operations_idempotency(runtime_id, idempotency_key), INDEX idx_operations_queue(status, created_at),
    INDEX idx_operations_application(application_id, created_at), CONSTRAINT fk_operation_runtime FOREIGN KEY(runtime_id) REFERENCES runtimes(id)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE deployment_operations;
DROP TABLE application_revisions;
DROP TABLE applications;
DROP TABLE catalog_app_versions;
DROP TABLE catalog_sources;
DROP INDEX idx_services_application ON services;
DROP INDEX uq_services_stable_key ON services;
ALTER TABLE routes DROP COLUMN route_status;
ALTER TABLE services DROP COLUMN routing_issues, DROP COLUMN routing_eligible, DROP COLUMN managed, DROP COLUMN application_id, DROP COLUMN runtime_object_id, DROP COLUMN service_key;
ALTER TABLE runtimes DROP COLUMN management_enabled;
