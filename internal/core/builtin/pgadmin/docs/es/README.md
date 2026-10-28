# pgAdmin

pgAdmin usa correo y contraseña de administrador en el puerto 80. DATABASE_NETWORK debe ser una red PostgreSQL existente. Registre el servidor manualmente tras iniciar sesión; pgAdmin no crea PostgreSQL. Datos: /var/lib/pgadmin.

Seleccione un Runtime Docker Compose con la administración habilitada. La instalación en Swarm está bloqueada para estas plantillas con estado.

## Parámetros

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

[Instalación y mantenimiento](operations.md)
