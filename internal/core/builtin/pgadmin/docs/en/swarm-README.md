# pgAdmin

pgAdmin uses an administrator email and password on port 80. Set DATABASE_NETWORK to an existing PostgreSQL network. After signing in, register the database server manually; pgAdmin does not create a PostgreSQL server. Data: /var/lib/pgadmin.

This version supports Docker Compose and Swarm Services. Swarm uses one replica pinned to the selected data node, named local volumes, and stop-first updates. An offline data node prevents scheduling on another node.

## Parameters

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Choose a Ready/Active data node. Persistent services use one replica and stay on this node; an offline node pauses availability.

[Compose and Swarm deployment](swarm-operations.md)
