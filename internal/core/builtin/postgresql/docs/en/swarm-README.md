# PostgreSQL

PostgreSQL 18 on port 5432. Mount the parent data directory /var/lib/postgresql, not the legacy /var/lib/postgresql/data path.

This version supports Docker Compose and Swarm Services. Swarm uses one replica pinned to the selected data node, named local volumes, and stop-first updates. An offline data node prevents scheduling on another node.

## Parameters

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

Choose a Ready/Active data node. Persistent services use one replica and stay on this node; an offline node pauses availability.

[Compose and Swarm deployment](swarm-operations.md)
