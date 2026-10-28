# Instalación y mantenimiento

pgAdmin usa correo y contraseña de administrador en el puerto 80. DATABASE_NETWORK debe ser una red PostgreSQL existente. Registre el servidor manualmente tras iniciar sesión; pgAdmin no crea PostgreSQL. Datos: /var/lib/pgadmin.

Elija una versión fija, revise sus parámetros e introduzca sus propios secretos. Las contraseñas no tienen un valor predeterminado utilizable; use al menos 16 bytes. No publique secretos reales en el mercado.

Ejecute la comprobación previa, revise Compose, los volúmenes y las dependencias de red, y confirme la instalación. Consulte la operación y la salud de cada contenedor; estar en ejecución no garantiza un estado saludable.

Las bases de datos solo exponen puertos internos. Cree una Route HTTP con TLS únicamente para aplicaciones web, tras verificar el acceso desde el Gateway al servicio.

Almacenamiento y conectividad

Antes de actualizar, haga copias de volúmenes y bases con herramientas adecuadas y pruebe su restauración. Restaurar una imagen o configuración no restaura los datos de negocio.

Detener la aplicación conserva los volúmenes con nombre. Eliminarla no debe borrar implícitamente los datos persistentes. Conserve los materiales de recuperación y asigne la responsabilidad de futuras actualizaciones.

Estas plantillas no publican puertos del host. Las redes externas de bases de datos deben existir y ser accesibles desde la aplicación.

Sincronizar añade versiones inmutables; no actualiza cargas instaladas. Una versión retirada permanece en el historial, pero no se puede instalar de nuevo.
