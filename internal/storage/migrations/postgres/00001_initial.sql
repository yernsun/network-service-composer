-- +goose Up
CREATE TABLE gateway_pools (
    id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE,
    backend_network VARCHAR(255) NOT NULL, challenge_upstream TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, desired_revision BIGINT NOT NULL DEFAULT 0,
    probe_gateway_id VARCHAR(64) NOT NULL DEFAULT '', probe_epoch BIGINT NOT NULL DEFAULT 0,
    last_snapshot_sha256 VARCHAR(64) NOT NULL DEFAULT '', last_snapshot_built_at TIMESTAMPTZ NULL,
    revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE runtimes (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL REFERENCES gateway_pools(id), name VARCHAR(255) NOT NULL UNIQUE,
    runtime_type VARCHAR(32) NOT NULL, endpoint TEXT NOT NULL, credential_ref VARCHAR(255) NOT NULL DEFAULT '', cluster_id VARCHAR(255) NOT NULL DEFAULT '',
    backend_mode VARCHAR(32) NOT NULL DEFAULT 'direct', enabled BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    observation_epoch BIGINT NOT NULL DEFAULT 0, applied_observation BIGINT NOT NULL DEFAULT 0, last_seen_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_runtimes_pool ON runtimes(pool_id);
CREATE TABLE services (
    id VARCHAR(64) PRIMARY KEY, runtime_id VARCHAR(64) NOT NULL REFERENCES runtimes(id), external_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL, namespace VARCHAR(255) NOT NULL DEFAULT '', discovered_protocol VARCHAR(16) NOT NULL DEFAULT 'http', protocol_override VARCHAR(16) NULL,
    discovered_port INTEGER NOT NULL DEFAULT 0, target_port_override INTEGER NULL, discovered_enabled BOOLEAN NOT NULL DEFAULT TRUE, enabled_override BOOLEAN NULL,
    discovery_mode VARCHAR(32) NOT NULL DEFAULT 'direct', readiness_mode VARCHAR(16) NOT NULL DEFAULT 'auto', readiness_path TEXT NOT NULL DEFAULT '',
    readiness_scheme VARCHAR(16) NOT NULL DEFAULT 'http', readiness_interval_ns BIGINT NOT NULL, readiness_timeout_ns BIGINT NOT NULL,
    readiness_success_threshold INTEGER NOT NULL, readiness_failure_threshold INTEGER NOT NULL, readiness_startup_grace_ns BIGINT NOT NULL,
    readiness_startup_timeout_ns BIGINT NOT NULL, readiness_tcp_fallback BOOLEAN NOT NULL, desired_instances INTEGER NOT NULL DEFAULT 0,
    running_instances INTEGER NOT NULL DEFAULT 0, ready_instances INTEGER NOT NULL DEFAULT 0, status VARCHAR(32) NOT NULL,
    configuration_error TEXT NOT NULL DEFAULT '', last_seen_at TIMESTAMPTZ NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, UNIQUE(runtime_id, external_id)
);
CREATE INDEX idx_services_runtime ON services(runtime_id);
CREATE TABLE service_endpoints (
    id VARCHAR(64) PRIMARY KEY, service_id VARCHAR(64) NOT NULL REFERENCES services(id), external_id VARCHAR(255) NOT NULL,
    address VARCHAR(255) NOT NULL, port INTEGER NOT NULL, node_id VARCHAR(255) NOT NULL DEFAULT '', runtime_state VARCHAR(32) NOT NULL,
    health_state VARCHAR(32) NOT NULL, ready_state VARCHAR(32) NOT NULL, readiness_source VARCHAR(16) NOT NULL DEFAULT 'auto', weight INTEGER NOT NULL DEFAULT 1,
    success_count INTEGER NOT NULL DEFAULT 0, failure_count INTEGER NOT NULL DEFAULT 0, last_probe_sequence BIGINT NOT NULL DEFAULT 0,
    last_probe_error TEXT NOT NULL DEFAULT '', first_seen_at TIMESTAMPTZ NOT NULL, ready_since TIMESTAMPTZ NULL, drain_since TIMESTAMPTZ NULL,
    stale_since TIMESTAMPTZ NULL, last_seen_at TIMESTAMPTZ NOT NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, UNIQUE(service_id, external_id)
);
CREATE INDEX idx_endpoints_service ON service_endpoints(service_id);
CREATE INDEX idx_endpoints_ready ON service_endpoints(ready_state, runtime_state, health_state);
CREATE TABLE certificates (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL REFERENCES gateway_pools(id), provider VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL,
    primary_domain VARCHAR(255) NOT NULL, certificate_pem TEXT NOT NULL DEFAULT '', encrypted_private_key BYTEA NULL,
    encryption_key_id VARCHAR(255) NOT NULL DEFAULT '', fingerprint VARCHAR(128) NOT NULL DEFAULT '', serial_number VARCHAR(255) NOT NULL DEFAULT '',
    not_before TIMESTAMPTZ NULL, not_after TIMESTAMPTZ NULL, attempt_count INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_certificates_pool ON certificates(pool_id);
CREATE INDEX idx_certificates_status_next ON certificates(status, next_attempt_at);
CREATE TABLE domains (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL REFERENCES gateway_pools(id), host VARCHAR(255) NOT NULL,
    tls_mode VARCHAR(16) NOT NULL, https_redirect BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    certificate_id VARCHAR(64) NULL REFERENCES certificates(id), source VARCHAR(16) NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(pool_id, host)
);
CREATE INDEX idx_domains_pool ON domains(pool_id);
CREATE TABLE routes (
    id VARCHAR(64) PRIMARY KEY, domain_id VARCHAR(64) NOT NULL REFERENCES domains(id), service_id VARCHAR(64) NOT NULL REFERENCES services(id),
    external_key VARCHAR(255) NOT NULL DEFAULT '', path TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0, strip_path BOOLEAN NOT NULL DEFAULT FALSE,
    target_port INTEGER NOT NULL DEFAULT 0, upstream_protocol VARCHAR(16) NOT NULL DEFAULT 'http', upstream_server_name VARCHAR(255) NOT NULL DEFAULT '',
    websocket_enabled BOOLEAN NOT NULL DEFAULT FALSE, enabled BOOLEAN NOT NULL DEFAULT TRUE, source VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_routes_domain ON routes(domain_id);
CREATE INDEX idx_routes_service ON routes(service_id);
CREATE INDEX idx_routes_precedence ON routes(domain_id, path, priority, source);
CREATE TABLE certificate_domains (
    certificate_id VARCHAR(64) NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    domain_id VARCHAR(64) NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    PRIMARY KEY(certificate_id, domain_id)
);
CREATE TABLE acme_accounts (
    id VARCHAR(64) PRIMARY KEY, directory_url TEXT NOT NULL, email VARCHAR(255) NOT NULL, registration TEXT NOT NULL DEFAULT '',
    encrypted_private_key BYTEA NOT NULL, encryption_key_id VARCHAR(255) NOT NULL, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(directory_url, email)
);
CREATE TABLE acme_challenges (
    id VARCHAR(64) PRIMARY KEY, certificate_id VARCHAR(64) NOT NULL REFERENCES certificates(id) ON DELETE CASCADE, domain VARCHAR(255) NOT NULL,
    challenge_type VARCHAR(32) NOT NULL, token VARCHAR(255) NOT NULL UNIQUE, key_authorization TEXT NOT NULL, status VARCHAR(32) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_acme_challenges_expiry ON acme_challenges(expires_at);
CREATE TABLE gateways (
    id VARCHAR(64) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL REFERENCES gateway_pools(id), name VARCHAR(255) NOT NULL,
    address TEXT NOT NULL DEFAULT '', certificate_hash VARCHAR(128) NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT TRUE, status VARCHAR(32) NOT NULL,
    desired_revision BIGINT NOT NULL DEFAULT 0, applied_revision BIGINT NOT NULL DEFAULT 0, agent_version VARCHAR(64) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '', last_seen_at TIMESTAMPTZ NULL, revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_gateways_pool ON gateways(pool_id);

-- +goose Down
DROP TABLE gateways, acme_challenges, acme_accounts, certificate_domains, routes, domains, certificates, service_endpoints, services, runtimes, gateway_pools;
