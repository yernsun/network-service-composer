# WordPress

WordPress inclut un service MySQL dédié. Attendez la disponibilité de la base avant d’ouvrir l’installation sur le port 80. Conservez database-data et wordpress-data.

Choisissez un Runtime Docker Compose dont la gestion est activée. L’installation Swarm est bloquée pour ces modèles avec état.

## Paramètres

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

[Installation et maintenance](operations.md)
