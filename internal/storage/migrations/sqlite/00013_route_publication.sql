-- +goose Up
CREATE TABLE routes_new (
    id TEXT PRIMARY KEY,
    domain_id TEXT NOT NULL REFERENCES domains(id),
    service_id TEXT NULL REFERENCES services(id),
    external_key TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    strip_path BOOLEAN NOT NULL DEFAULT FALSE,
    target_port INTEGER NOT NULL DEFAULT 0,
    upstream_protocol TEXT NOT NULL DEFAULT 'http',
    upstream_server_name TEXT NOT NULL DEFAULT '',
    websocket_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    route_status TEXT NOT NULL DEFAULT 'ACTIVE',
    source TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    kind TEXT NOT NULL DEFAULT 'proxy',
    match_kind TEXT NOT NULL DEFAULT 'prefer',
    external_url TEXT NOT NULL DEFAULT '',
    response_code INTEGER NOT NULL DEFAULT 0,
    response_value TEXT NOT NULL DEFAULT '',
    read_timeout_seconds INTEGER NOT NULL DEFAULT 60,
    connect_timeout_seconds INTEGER NOT NULL DEFAULT 5
);
INSERT INTO routes_new (id,domain_id,service_id,external_key,path,priority,strip_path,target_port,upstream_protocol,upstream_server_name,websocket_enabled,enabled,route_status,source,last_error,revision,created_at,updated_at)
SELECT id,domain_id,service_id,external_key,path,priority,strip_path,target_port,upstream_protocol,upstream_server_name,websocket_enabled,enabled,route_status,source,last_error,revision,created_at,updated_at FROM routes;
DROP TABLE routes;
ALTER TABLE routes_new RENAME TO routes;
CREATE INDEX idx_routes_domain ON routes(domain_id);
CREATE INDEX idx_routes_service ON routes(service_id);
CREATE INDEX idx_routes_precedence ON routes(domain_id,path,priority,source);

-- +goose Down
SELECT 1;
