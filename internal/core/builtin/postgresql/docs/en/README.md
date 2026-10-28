# PostgreSQL

PostgreSQL 18 on port 5432. Mount the parent data directory /var/lib/postgresql, not the legacy /var/lib/postgresql/data path.

Select a Docker Compose Runtime with management enabled. Swarm installation is blocked for these stateful templates.

## Parameters

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

[Installation and maintenance](operations.md)
