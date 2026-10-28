# phpMyAdmin

phpMyAdmin conecta con PMA_HOST y PMA_PORT mediante DATABASE_NETWORK. Inicie sesión en el puerto 80 con credenciales MySQL existentes. El acceso automático y la selección arbitraria de servidor están deshabilitados. Sesiones: /sessions.

Seleccione un Runtime Docker Compose con la administración habilitada. La instalación en Swarm está bloqueada para estas plantillas con estado.

## Parámetros

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

[Instalación y mantenimiento](operations.md)
