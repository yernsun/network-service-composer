-- +goose Up
ALTER TABLE service_endpoints RENAME TO service_instances;
ALTER TABLE runtimes DROP COLUMN backend_mode;
ALTER TABLE services DROP COLUMN discovery_mode;

ALTER TABLE gateway_pools ADD COLUMN probe_revision BIGINT NOT NULL DEFAULT 0;

ALTER TABLE services ADD COLUMN qualified_name TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN dns_name TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN dns_status TEXT NOT NULL DEFAULT 'MISSING';
ALTER TABLE services ADD COLUMN dns_last_error TEXT NOT NULL DEFAULT '';
UPDATE services
SET qualified_name = CASE
    WHEN (SELECT runtime_type FROM runtimes WHERE runtimes.id=services.runtime_id)='swarm' THEN name
    WHEN namespace<>'' THEN namespace || '_' || name
    ELSE name
END;
UPDATE services SET dns_name=qualified_name;
UPDATE services SET dns_status=CASE WHEN routing_eligible THEN 'READY' ELSE 'MISSING' END;

ALTER TABLE domains ADD COLUMN http_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE domains ADD COLUMN https_enabled BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE domains SET https_enabled=(tls_mode<>'disabled');

ALTER TABLE gateways ADD COLUMN protocol_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gateways ADD COLUMN renderer_version TEXT NOT NULL DEFAULT '';
ALTER TABLE gateways ADD COLUMN model_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE gateways ADD COLUMN rendered_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE gateways ADD COLUMN applied_probe_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE service_dns_probes (
    service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_sequence BIGINT NOT NULL DEFAULT 0,
    target_token TEXT NOT NULL DEFAULT '',
    success BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT NOT NULL DEFAULT '',
    latency_ns BIGINT NOT NULL DEFAULT 0,
    observed_at TIMESTAMP NULL
);

-- +goose Down
DROP TABLE service_dns_probes;
ALTER TABLE service_instances RENAME TO service_endpoints;
ALTER TABLE runtimes ADD COLUMN backend_mode TEXT NOT NULL DEFAULT 'direct';
ALTER TABLE services ADD COLUMN discovery_mode TEXT NOT NULL DEFAULT 'direct';
ALTER TABLE gateways DROP COLUMN rendered_digest;
ALTER TABLE gateways DROP COLUMN applied_probe_revision;
ALTER TABLE gateways DROP COLUMN model_digest;
ALTER TABLE gateways DROP COLUMN renderer_version;
ALTER TABLE gateways DROP COLUMN protocol_version;
ALTER TABLE domains DROP COLUMN https_enabled;
ALTER TABLE domains DROP COLUMN http_enabled;
ALTER TABLE services DROP COLUMN dns_last_error;
ALTER TABLE services DROP COLUMN dns_status;
ALTER TABLE services DROP COLUMN dns_name;
ALTER TABLE services DROP COLUMN qualified_name;
ALTER TABLE gateway_pools DROP COLUMN probe_revision;
