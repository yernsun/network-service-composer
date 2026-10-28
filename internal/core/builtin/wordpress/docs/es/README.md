# WordPress

WordPress incluye un MySQL dedicado. Espere a que la base esté lista antes de abrir el instalador en el puerto 80. Conserve database-data y wordpress-data.

Seleccione un Runtime Docker Compose con la administración habilitada. La instalación en Swarm está bloqueada para estas plantillas con estado.

## Parámetros

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

[Instalación y mantenimiento](operations.md)
