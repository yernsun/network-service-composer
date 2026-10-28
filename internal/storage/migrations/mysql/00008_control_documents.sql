-- +goose Up
CREATE TABLE control_documents (
    id VARCHAR(180) PRIMARY KEY,
    kind VARCHAR(48) NOT NULL,
    parent_id VARCHAR(180) NOT NULL DEFAULT '',
    payload LONGTEXT NOT NULL,
    revision BIGINT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL
);
CREATE INDEX idx_control_parent ON control_documents(kind,parent_id,id);

ALTER TABLE application_revisions ADD COLUMN configuration_source JSON NOT NULL DEFAULT (JSON_OBJECT());

-- The renderer changed; existing gateways must receive a new configuration tuple.
UPDATE gateway_pools SET desired_revision=desired_revision+1,revision=revision+1;

-- +goose Down
ALTER TABLE application_revisions DROP COLUMN configuration_source;
DROP TABLE control_documents;
