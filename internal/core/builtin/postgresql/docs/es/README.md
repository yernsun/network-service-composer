# PostgreSQL

PostgreSQL 18 usa el puerto 5432. Monte /var/lib/postgresql, no la ruta antigua /var/lib/postgresql/data.

Seleccione un Runtime Docker Compose con la administración habilitada. La instalación en Swarm está bloqueada para estas plantillas con estado.

## Parámetros

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

[Instalación y mantenimiento](operations.md)
