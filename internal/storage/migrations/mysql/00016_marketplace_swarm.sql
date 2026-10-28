-- +goose Up
ALTER TABLE catalog_app_versions ADD COLUMN swarm_yaml TEXT NOT NULL DEFAULT ('');
ALTER TABLE catalog_app_versions ADD COLUMN swarm_sha256 VARCHAR(64) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE catalog_app_versions DROP COLUMN swarm_sha256;
ALTER TABLE catalog_app_versions DROP COLUMN swarm_yaml;
