-- +goose Up
CREATE TABLE gateway_pools (
    id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE,
    backend_network VARCHAR(255) NOT NULL, challenge_upstream TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, desired_revision BIGINT NOT NULL DEFAULT 0,
    probe_gateway_id VARCHAR(64) NOT NULL DEFAULT '', probe_epoch BIGINT NOT NULL DEFAULT 0,
    last_snapshot_sha256 VARCHAR(64) NOT NULL DEFAULT '', last_snapshot_built_at TIMESTAMP(6) NULL,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL
) ENGINE=InnoDB;
CREATE TABLE runtimes (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, name VARCHAR(255) NOT NULL UNIQUE,
    runtime_type VARCHAR(32) NOT NULL, endpoint TEXT NOT NULL, credential_ref VARCHAR(255) NOT NULL DEFAULT '', cluster_id VARCHAR(255) NOT NULL DEFAULT '',
    backend_mode VARCHAR(32) NOT NULL DEFAULT 'direct', enabled BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    observation_epoch BIGINT NOT NULL DEFAULT 0, applied_observation BIGINT NOT NULL DEFAULT 0, last_seen_at TIMESTAMP(6) NULL,
    last_error TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    INDEX idx_runtimes_pool(pool_id), CONSTRAINT fk_runtimes_pool FOREIGN KEY(pool_id) REFERENCES gateway_pools(id)
) ENGINE=InnoDB;
CREATE TABLE services (
    id VARCHAR(64) PRIMARY KEY, runtime_id VARCHAR(64) NOT NULL, external_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL, namespace VARCHAR(255) NOT NULL DEFAULT '', discovered_protocol VARCHAR(16) NOT NULL DEFAULT 'http', protocol_override VARCHAR(16) NULL,
    discovered_port INTEGER NOT NULL DEFAULT 0, target_port_override INTEGER NULL, discovered_enabled BOOLEAN NOT NULL DEFAULT TRUE, enabled_override BOOLEAN NULL,
    discovery_mode VARCHAR(32) NOT NULL DEFAULT 'direct', readiness_mode VARCHAR(16) NOT NULL DEFAULT 'auto', readiness_path TEXT NOT NULL,
    readiness_scheme VARCHAR(16) NOT NULL DEFAULT 'http', readiness_interval_ns BIGINT NOT NULL, readiness_timeout_ns BIGINT NOT NULL,
    readiness_success_threshold INTEGER NOT NULL, readiness_failure_threshold INTEGER NOT NULL, readiness_startup_grace_ns BIGINT NOT NULL,
    readiness_startup_timeout_ns BIGINT NOT NULL, readiness_tcp_fallback BOOLEAN NOT NULL, desired_instances INTEGER NOT NULL DEFAULT 0,
    running_instances INTEGER NOT NULL DEFAULT 0, ready_instances INTEGER NOT NULL DEFAULT 0, status VARCHAR(32) NOT NULL,
    configuration_error TEXT NOT NULL, last_seen_at TIMESTAMP(6) NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_services_external(runtime_id, external_id), INDEX idx_services_runtime(runtime_id),
    CONSTRAINT fk_services_runtime FOREIGN KEY(runtime_id) REFERENCES runtimes(id)
) ENGINE=InnoDB;
CREATE TABLE service_endpoints (
    id VARCHAR(64) PRIMARY KEY, service_id VARCHAR(64) NOT NULL, external_id VARCHAR(255) NOT NULL,
    address VARCHAR(255) NOT NULL, port INTEGER NOT NULL, node_id VARCHAR(255) NOT NULL DEFAULT '', runtime_state VARCHAR(32) NOT NULL,
    health_state VARCHAR(32) NOT NULL, ready_state VARCHAR(32) NOT NULL, readiness_source VARCHAR(16) NOT NULL DEFAULT 'auto', weight INTEGER NOT NULL DEFAULT 1,
    success_count INTEGER NOT NULL DEFAULT 0, failure_count INTEGER NOT NULL DEFAULT 0, last_probe_sequence BIGINT NOT NULL DEFAULT 0,
    last_probe_error TEXT NOT NULL, first_seen_at TIMESTAMP(6) NOT NULL, ready_since TIMESTAMP(6) NULL, drain_since TIMESTAMP(6) NULL,
    stale_since TIMESTAMP(6) NULL, last_seen_at TIMESTAMP(6) NOT NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_endpoints_external(service_id, external_id), INDEX idx_endpoints_service(service_id),
    INDEX idx_endpoints_ready(ready_state, runtime_state, health_state),
    CONSTRAINT fk_endpoints_service FOREIGN KEY(service_id) REFERENCES services(id)
) ENGINE=InnoDB;
CREATE TABLE certificates (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, provider VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL,
    primary_domain VARCHAR(255) NOT NULL, certificate_pem LONGTEXT NOT NULL, encrypted_private_key LONGBLOB NULL,
    encryption_key_id VARCHAR(255) NOT NULL DEFAULT '', fingerprint VARCHAR(128) NOT NULL DEFAULT '', serial_number VARCHAR(255) NOT NULL DEFAULT '',
    not_before TIMESTAMP(6) NULL, not_after TIMESTAMP(6) NULL, attempt_count INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMP(6) NULL,
    last_error TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    INDEX idx_certificates_pool(pool_id), INDEX idx_certificates_status_next(status, next_attempt_at),
    CONSTRAINT fk_certificates_pool FOREIGN KEY(pool_id) REFERENCES gateway_pools(id)
) ENGINE=InnoDB;
CREATE TABLE domains (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, host VARCHAR(255) NOT NULL,
    tls_mode VARCHAR(16) NOT NULL, https_redirect BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    certificate_id VARCHAR(64) NULL, source VARCHAR(16) NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_domains_host(pool_id, host), INDEX idx_domains_pool(pool_id),
    CONSTRAINT fk_domains_pool FOREIGN KEY(pool_id) REFERENCES gateway_pools(id),
    CONSTRAINT fk_domains_certificate FOREIGN KEY(certificate_id) REFERENCES certificates(id)
) ENGINE=InnoDB;
CREATE TABLE routes (
    id VARCHAR(64) PRIMARY KEY, domain_id VARCHAR(64) NOT NULL, service_id VARCHAR(64) NOT NULL,
    external_key VARCHAR(255) NOT NULL DEFAULT '', path VARCHAR(2048) NOT NULL, priority INTEGER NOT NULL DEFAULT 0, strip_path BOOLEAN NOT NULL DEFAULT FALSE,
    target_port INTEGER NOT NULL DEFAULT 0, upstream_protocol VARCHAR(16) NOT NULL DEFAULT 'http', upstream_server_name VARCHAR(255) NOT NULL DEFAULT '',
    websocket_enabled BOOLEAN NOT NULL DEFAULT FALSE, enabled BOOLEAN NOT NULL DEFAULT TRUE, source VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    INDEX idx_routes_precedence(domain_id, path(512), priority, source), INDEX idx_routes_domain(domain_id), INDEX idx_routes_service(service_id),
    CONSTRAINT fk_routes_domain FOREIGN KEY(domain_id) REFERENCES domains(id), CONSTRAINT fk_routes_service FOREIGN KEY(service_id) REFERENCES services(id)
) ENGINE=InnoDB;
CREATE TABLE certificate_domains (
    certificate_id VARCHAR(64) NOT NULL, domain_id VARCHAR(64) NOT NULL,
    PRIMARY KEY(certificate_id, domain_id),
    CONSTRAINT fk_cd_certificate FOREIGN KEY(certificate_id) REFERENCES certificates(id) ON DELETE CASCADE,
    CONSTRAINT fk_cd_domain FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE acme_accounts (
    id VARCHAR(64) PRIMARY KEY, directory_url VARCHAR(2048) NOT NULL, email VARCHAR(255) NOT NULL, registration LONGTEXT NOT NULL,
    encrypted_private_key LONGBLOB NOT NULL, encryption_key_id VARCHAR(255) NOT NULL, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    UNIQUE KEY uq_acme_account(directory_url(512), email)
) ENGINE=InnoDB;
CREATE TABLE acme_challenges (
    id VARCHAR(64) PRIMARY KEY, certificate_id VARCHAR(64) NOT NULL, domain VARCHAR(255) NOT NULL,
    challenge_type VARCHAR(32) NOT NULL, token VARCHAR(255) NOT NULL UNIQUE, key_authorization TEXT NOT NULL, status VARCHAR(32) NOT NULL,
    expires_at TIMESTAMP(6) NOT NULL, created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    INDEX idx_acme_challenges_expiry(expires_at), CONSTRAINT fk_challenge_certificate FOREIGN KEY(certificate_id) REFERENCES certificates(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE gateways (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, name VARCHAR(255) NOT NULL,
    address TEXT NOT NULL, certificate_hash VARCHAR(128) NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    desired_revision BIGINT NOT NULL DEFAULT 0, applied_revision BIGINT NOT NULL DEFAULT 0, agent_version VARCHAR(64) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL, last_seen_at TIMESTAMP(6) NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP(6) NOT NULL,
    INDEX idx_gateways_pool(pool_id), CONSTRAINT fk_gateways_pool FOREIGN KEY(pool_id) REFERENCES gateway_pools(id)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE gateways;
DROP TABLE acme_challenges;
DROP TABLE acme_accounts;
DROP TABLE certificate_domains;
DROP TABLE routes;
DROP TABLE domains;
DROP TABLE certificates;
DROP TABLE service_endpoints;
DROP TABLE services;
DROP TABLE runtimes;
DROP TABLE gateway_pools;
