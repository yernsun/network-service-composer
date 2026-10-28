# PostgreSQL

PostgreSQL 18 usa el puerto 5432. Monte /var/lib/postgresql, no la ruta antigua /var/lib/postgresql/data.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

## Parámetros

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

[Despliegue Compose y Swarm](swarm-operations.md)
