-- +goose Up
CREATE TABLE gateway_pools (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    backend_network TEXT NOT NULL,
    challenge_upstream TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    desired_revision BIGINT NOT NULL DEFAULT 0,
    probe_gateway_id TEXT NOT NULL DEFAULT '',
    probe_epoch BIGINT NOT NULL DEFAULT 0,
    last_snapshot_sha256 TEXT NOT NULL DEFAULT '',
    last_snapshot_built_at TIMESTAMP NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE runtimes (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES gateway_pools(id),
    name TEXT NOT NULL UNIQUE,
    runtime_type TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    credential_ref TEXT NOT NULL DEFAULT '',
    cluster_id TEXT NOT NULL DEFAULT '',
    backend_mode TEXT NOT NULL DEFAULT 'direct',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL,
    observation_epoch BIGINT NOT NULL DEFAULT 0,
    applied_observation BIGINT NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMP NULL,
    last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_runtimes_pool ON runtimes(pool_id);

CREATE TABLE services (
    id TEXT PRIMARY KEY,
    runtime_id TEXT NOT NULL REFERENCES runtimes(id),
    external_id TEXT NOT NULL,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL DEFAULT '',
    discovered_protocol TEXT NOT NULL DEFAULT 'http',
    protocol_override TEXT NULL,
    discovered_port INTEGER NOT NULL DEFAULT 0,
    target_port_override INTEGER NULL,
    discovered_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    enabled_override BOOLEAN NULL,
    discovery_mode TEXT NOT NULL DEFAULT 'direct',
    readiness_mode TEXT NOT NULL DEFAULT 'auto',
    readiness_path TEXT NOT NULL DEFAULT '',
    readiness_scheme TEXT NOT NULL DEFAULT 'http',
    readiness_interval_ns BIGINT NOT NULL,
    readiness_timeout_ns BIGINT NOT NULL,
    readiness_success_threshold INTEGER NOT NULL,
    readiness_failure_threshold INTEGER NOT NULL,
    readiness_startup_grace_ns BIGINT NOT NULL,
    readiness_startup_timeout_ns BIGINT NOT NULL,
    readiness_tcp_fallback BOOLEAN NOT NULL,
    desired_instances INTEGER NOT NULL DEFAULT 0,
    running_instances INTEGER NOT NULL DEFAULT 0,
    ready_instances INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    configuration_error TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMP NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(runtime_id, external_id)
);
CREATE INDEX idx_services_runtime ON services(runtime_id);

CREATE TABLE service_endpoints (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id),
    external_id TEXT NOT NULL,
    address TEXT NOT NULL,
    port INTEGER NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    runtime_state TEXT NOT NULL,
    health_state TEXT NOT NULL,
    ready_state TEXT NOT NULL,
    readiness_source TEXT NOT NULL DEFAULT 'auto',
    weight INTEGER NOT NULL DEFAULT 1,
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_probe_sequence BIGINT NOT NULL DEFAULT 0,
    last_probe_error TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMP NOT NULL,
    ready_since TIMESTAMP NULL,
    drain_since TIMESTAMP NULL,
    stale_since TIMESTAMP NULL,
    last_seen_at TIMESTAMP NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(service_id, external_id)
);
CREATE INDEX idx_endpoints_service ON service_endpoints(service_id);
CREATE INDEX idx_endpoints_ready ON service_endpoints(ready_state, runtime_state, health_state);

CREATE TABLE certificates (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES gateway_pools(id),
    provider TEXT NOT NULL,
    status TEXT NOT NULL,
    primary_domain TEXT NOT NULL,
    certificate_pem TEXT NOT NULL DEFAULT '',
    encrypted_private_key BLOB NULL,
    encryption_key_id TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    serial_number TEXT NOT NULL DEFAULT '',
    not_before TIMESTAMP NULL,
    not_after TIMESTAMP NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP NULL,
    last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_certificates_pool ON certificates(pool_id);
CREATE INDEX idx_certificates_status_next ON certificates(status, next_attempt_at);

CREATE TABLE domains (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES gateway_pools(id),
    host TEXT NOT NULL,
    tls_mode TEXT NOT NULL,
    https_redirect BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL,
    certificate_id TEXT NULL REFERENCES certificates(id),
    source TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(pool_id, host)
);
CREATE INDEX idx_domains_pool ON domains(pool_id);

CREATE TABLE routes (
    id TEXT PRIMARY KEY,
    domain_id TEXT NOT NULL REFERENCES domains(id),
    service_id TEXT NOT NULL REFERENCES services(id),
    external_key TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    strip_path BOOLEAN NOT NULL DEFAULT FALSE,
    target_port INTEGER NOT NULL DEFAULT 0,
    upstream_protocol TEXT NOT NULL DEFAULT 'http',
    upstream_server_name TEXT NOT NULL DEFAULT '',
    websocket_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    source TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_routes_domain ON routes(domain_id);
CREATE INDEX idx_routes_service ON routes(service_id);
CREATE INDEX idx_routes_precedence ON routes(domain_id, path, priority, source);

CREATE TABLE certificate_domains (
    certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    domain_id TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    PRIMARY KEY(certificate_id, domain_id)
);

CREATE TABLE acme_accounts (
    id TEXT PRIMARY KEY,
    directory_url TEXT NOT NULL,
    email TEXT NOT NULL,
    registration TEXT NOT NULL DEFAULT '',
    encrypted_private_key BLOB NOT NULL,
    encryption_key_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(directory_url, email)
);

CREATE TABLE acme_challenges (
    id TEXT PRIMARY KEY,
    certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    challenge_type TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    key_authorization TEXT NOT NULL,
    status TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_acme_challenges_expiry ON acme_challenges(expires_at);

CREATE TABLE gateways (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES gateway_pools(id),
    name TEXT NOT NULL,
    address TEXT NOT NULL DEFAULT '',
    certificate_hash TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL,
    desired_revision BIGINT NOT NULL DEFAULT 0,
    applied_revision BIGINT NOT NULL DEFAULT 0,
    agent_version TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMP NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_gateways_pool ON gateways(pool_id);

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
