# phpMyAdmin

phpMyAdmin connects to PMA_HOST and PMA_PORT through DATABASE_NETWORK. Use existing MySQL credentials at the login page on port 80. No automatic login or arbitrary server selection is enabled. Sessions: /sessions.

Select a Docker Compose Runtime with management enabled. Swarm installation is blocked for these stateful templates.

## Parameters

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

[Installation and maintenance](operations.md)
