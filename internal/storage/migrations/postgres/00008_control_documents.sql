-- +goose Up
CREATE TABLE control_documents (
    id VARCHAR(180) PRIMARY KEY,
    kind VARCHAR(48) NOT NULL,
    parent_id VARCHAR(180) NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    revision BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_control_parent ON control_documents(kind,parent_id,id);

ALTER TABLE application_revisions ADD COLUMN configuration_source TEXT NOT NULL DEFAULT '{}';

-- The renderer changed; existing gateways must receive a new configuration tuple.
UPDATE gateway_pools SET desired_revision=desired_revision+1,revision=revision+1;

-- +goose Down
ALTER TABLE application_revisions DROP COLUMN configuration_source;
DROP TABLE control_documents;
