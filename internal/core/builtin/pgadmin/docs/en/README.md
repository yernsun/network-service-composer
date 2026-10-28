# pgAdmin

pgAdmin uses an administrator email and password on port 80. Set DATABASE_NETWORK to an existing PostgreSQL network. After signing in, register the database server manually; pgAdmin does not create a PostgreSQL server. Data: /var/lib/pgadmin.

Select a Docker Compose Runtime with management enabled. Swarm installation is blocked for these stateful templates.

## Parameters

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

[Installation and maintenance](operations.md)
