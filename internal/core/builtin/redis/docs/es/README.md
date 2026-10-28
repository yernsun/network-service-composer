# Redis

Caché Redis con persistencia AOF; autenticación en el puerto 6379. Datos: /data.

Seleccione un Runtime Docker Compose con la administración habilitada. La instalación en Swarm está bloqueada para estas plantillas con estado.

## Parámetros

- `REDIS_PASSWORD`

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

[Instalación y mantenimiento](operations.md)
