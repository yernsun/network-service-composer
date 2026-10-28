-- +goose Up
ALTER TABLE services ADD COLUMN runtime_ready_instances INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN runtime_ready_instances;
