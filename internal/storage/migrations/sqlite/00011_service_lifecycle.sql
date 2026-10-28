-- +goose Up
ALTER TABLE services ADD COLUMN runtime_created_at TIMESTAMP NULL;
ALTER TABLE services ADD COLUMN runtime_updated_at TIMESTAMP NULL;
ALTER TABLE services ADD COLUMN removed_at TIMESTAMP NULL;

-- +goose Down
ALTER TABLE services DROP COLUMN removed_at;
ALTER TABLE services DROP COLUMN runtime_updated_at;
ALTER TABLE services DROP COLUMN runtime_created_at;
