# phpMyAdmin

phpMyAdmin connects to PMA_HOST and PMA_PORT through DATABASE_NETWORK. Use existing MySQL credentials at the login page on port 80. No automatic login or arbitrary server selection is enabled. Sessions: /sessions.

This version supports Docker Compose and Swarm Services. Swarm uses one replica pinned to the selected data node, named local volumes, and stop-first updates. An offline data node prevents scheduling on another node.

## Parameters

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

Choose a Ready/Active data node. Persistent services use one replica and stay on this node; an offline node pauses availability.

[Compose and Swarm deployment](swarm-operations.md)
