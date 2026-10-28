# Redis

Redis cache with AOF persistence; authenticate on port 6379. Data: /data.

Select a Docker Compose Runtime with management enabled. Swarm installation is blocked for these stateful templates.

## Parameters

- `REDIS_PASSWORD`

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

[Installation and maintenance](operations.md)
