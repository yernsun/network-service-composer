# WordPress

WordPress includes a dedicated MySQL service. Wait for database readiness before opening the web installer on port 80. Preserve both database-data and wordpress-data volumes.

Select a Docker Compose Runtime with management enabled. Swarm installation is blocked for these stateful templates.

## Parameters

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

[Installation and maintenance](operations.md)
