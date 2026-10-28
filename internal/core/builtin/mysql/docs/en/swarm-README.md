# MySQL

MySQL with separate root and application passwords. Connect on port 3306; data: /var/lib/mysql.

This version supports Docker Compose and Swarm Services. Swarm uses one replica pinned to the selected data node, named local volumes, and stop-first updates. An offline data node prevents scheduling on another node.

## Parameters

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

Choose a Ready/Active data node. Persistent services use one replica and stay on this node; an offline node pauses availability.

[Compose and Swarm deployment](swarm-operations.md)
