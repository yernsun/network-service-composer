-- +goose Up
CREATE TABLE docker_resources (
    cluster_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('config','secret')),
    docker_id TEXT NOT NULL,
    name TEXT NOT NULL,
    create_operation_id TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (cluster_id, kind, docker_id),
    UNIQUE (cluster_id, kind, name)
);

-- +goose Down
DROP TABLE docker_resources;
