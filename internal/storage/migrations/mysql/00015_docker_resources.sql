-- +goose Up
CREATE TABLE docker_resources (
    cluster_id VARCHAR(255) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    docker_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    create_operation_id VARCHAR(255) NOT NULL UNIQUE,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (cluster_id, kind, docker_id),
    UNIQUE KEY docker_resource_name (cluster_id, kind, name)
);

-- +goose Down
DROP TABLE docker_resources;
