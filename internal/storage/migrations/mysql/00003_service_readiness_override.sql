-- +goose Up
ALTER TABLE services ADD COLUMN readiness_override LONGTEXT NULL;

-- +goose Down
ALTER TABLE services DROP COLUMN readiness_override;
