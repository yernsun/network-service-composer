# pgAdmin

pgAdmin usa correo y contraseña de administrador en el puerto 80. DATABASE_NETWORK debe ser una red PostgreSQL existente. Registre el servidor manualmente tras iniciar sesión; pgAdmin no crea PostgreSQL. Datos: /var/lib/pgadmin.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

## Parámetros

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

[Despliegue Compose y Swarm](swarm-operations.md)
