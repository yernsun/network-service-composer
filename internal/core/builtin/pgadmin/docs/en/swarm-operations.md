# pgAdmin: Compose and Swarm deployment

pgAdmin uses an administrator email and password on port 80. Set DATABASE_NETWORK to an existing PostgreSQL network. After signing in, register the database server manually; pgAdmin does not create a PostgreSQL server. Data: /var/lib/pgadmin.

This version supports Docker Compose and Swarm Services. Swarm uses one replica pinned to the selected data node, named local volumes, and stop-first updates. An offline data node prevents scheduling on another node.

Choose a Ready/Active data node. Persistent services use one replica and stay on this node; an offline node pauses availability.

Stopping retains named volumes. Starting again requires fresh evidence that the original volumes still exist on the same node. Missing or replaced volumes block startup. Node changes and additional persistent replicas require a separate data migration.

Back up database contents and persistent files separately. Configuration rollback does not restore data. Initialization credentials only apply to empty volumes. WordPress includes its own MySQL; complete website administrator setup on first visit.

[README](swarm-README.md)

If a STOP result is unknown, START remains blocked until a repeated STOP confirms that tasks exited and the original volumes remain. An unknown first installation cannot be retried with empty replacement volumes; use verified backup recovery when original storage cannot be identified.
