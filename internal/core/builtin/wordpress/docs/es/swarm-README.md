# WordPress

WordPress incluye un MySQL dedicado. Espere a que la base esté lista antes de abrir el instalador en el puerto 80. Conserve database-data y wordpress-data.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

## Parámetros

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

[Despliegue Compose y Swarm](swarm-operations.md)
