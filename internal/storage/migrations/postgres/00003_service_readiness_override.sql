-- +goose Up
ALTER TABLE services ADD COLUMN readiness_override TEXT NULL;

-- +goose Down
ALTER TABLE services DROP COLUMN readiness_override;
