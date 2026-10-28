# phpMyAdmin: Despliegue Compose y Swarm

phpMyAdmin conecta con PMA_HOST y PMA_PORT mediante DATABASE_NETWORK. Inicie sesión en el puerto 80 con credenciales MySQL existentes. El acceso automático y la selección arbitraria de servidor están deshabilitados. Sesiones: /sessions.

Esta versión admite Docker Compose y servicios Swarm. Swarm usa una réplica en el nodo elegido, volúmenes locales con nombre y actualizaciones con parada previa. Si el nodo de datos se desconecta, no se traslada el servicio a otro nodo.

Seleccione un nodo Ready/Active. Los servicios persistentes usan una réplica fija en ese nodo; si se desconecta, quedan indisponibles.

La parada conserva los volúmenes con nombre. El siguiente inicio exige pruebas recientes de que los volúmenes originales siguen en el mismo nodo. Los volúmenes ausentes o reemplazados bloquean el inicio. Cambiar de nodo o añadir réplicas persistentes requiere una migración independiente.

Respalde por separado la base y los archivos persistentes. Revertir la configuración no restaura los datos. Las credenciales iniciales solo se aplican a volúmenes vacíos. WordPress incluye MySQL; configure al administrador del sitio en la primera visita.

[README](swarm-README.md)

Si el resultado de STOP es desconocido, START permanece bloqueado hasta que otro STOP confirme que las tareas terminaron y los volúmenes originales se conservaron. Una primera instalación con resultado desconocido no puede usar volúmenes de reemplazo vacíos. Si no se puede identificar el almacenamiento original, restaure una copia de seguridad verificada.
