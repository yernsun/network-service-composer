# Redis

Caché Redis con persistencia AOF; autenticación en el puerto 6379. Datos: /data.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

## Parámetros

- `REDIS_PASSWORD`

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

[Despliegue Compose y Swarm](swarm-operations.md)
