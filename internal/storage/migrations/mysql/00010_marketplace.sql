-- +goose Up
ALTER TABLE catalog_sources ADD COLUMN approved_endpoint TEXT NULL;
ALTER TABLE catalog_sources ADD COLUMN market_json LONGTEXT NULL;
UPDATE catalog_sources SET approved_endpoint='', market_json='{}';
ALTER TABLE catalog_sources MODIFY COLUMN approved_endpoint TEXT NOT NULL;
ALTER TABLE catalog_sources MODIFY COLUMN market_json LONGTEXT NOT NULL;
ALTER TABLE catalog_app_versions ADD COLUMN market_json LONGTEXT NULL;
UPDATE catalog_app_versions SET market_json='{}';
ALTER TABLE catalog_app_versions MODIFY COLUMN market_json LONGTEXT NOT NULL;
ALTER TABLE catalog_app_versions ADD COLUMN withdrawn BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE catalog_app_versions DROP COLUMN withdrawn;
ALTER TABLE catalog_app_versions DROP COLUMN market_json;
ALTER TABLE catalog_sources DROP COLUMN market_json;
ALTER TABLE catalog_sources DROP COLUMN approved_endpoint;
