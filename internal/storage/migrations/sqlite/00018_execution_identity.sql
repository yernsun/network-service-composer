-- +goose Up
ALTER TABLE deployment_operations ADD COLUMN runtime_identity TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployment_operations DROP COLUMN runtime_identity;
