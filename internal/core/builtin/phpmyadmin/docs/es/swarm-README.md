# phpMyAdmin

phpMyAdmin conecta con PMA_HOST y PMA_PORT mediante DATABASE_NETWORK. Inicie sesión en el puerto 80 con credenciales MySQL existentes. El acceso automático y la selección arbitraria de servidor están deshabilitados. Sesiones: /sessions.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

## Parámetros

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

[Despliegue Compose y Swarm](swarm-operations.md)
