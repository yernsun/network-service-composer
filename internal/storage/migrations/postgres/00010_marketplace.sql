-- +goose Up
ALTER TABLE catalog_sources ADD COLUMN approved_endpoint TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_sources ADD COLUMN market_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE catalog_app_versions ADD COLUMN market_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE catalog_app_versions ADD COLUMN withdrawn BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE catalog_app_versions DROP COLUMN withdrawn;
ALTER TABLE catalog_app_versions DROP COLUMN market_json;
ALTER TABLE catalog_sources DROP COLUMN market_json;
ALTER TABLE catalog_sources DROP COLUMN approved_endpoint;
