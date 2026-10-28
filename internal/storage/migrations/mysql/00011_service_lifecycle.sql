-- +goose Up
ALTER TABLE services
    ADD COLUMN runtime_created_at TIMESTAMP(6) NULL,
    ADD COLUMN runtime_updated_at TIMESTAMP(6) NULL,
    ADD COLUMN removed_at TIMESTAMP(6) NULL;

-- +goose Down
ALTER TABLE services
    DROP COLUMN removed_at,
    DROP COLUMN runtime_updated_at,
    DROP COLUMN runtime_created_at;
