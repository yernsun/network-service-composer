# PostgreSQL

PostgreSQL 18 utilise le port 5432. Montez /var/lib/postgresql plutôt que l’ancien chemin /var/lib/postgresql/data.

Choisissez un Runtime Docker Compose dont la gestion est activée. L’installation Swarm est bloquée pour ces modèles avec état.

## Paramètres

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

[Installation et maintenance](operations.md)
