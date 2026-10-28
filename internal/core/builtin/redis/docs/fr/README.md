# Redis

Cache Redis avec persistance AOF ; authentification sur le port 6379. Données : /data.

Choisissez un Runtime Docker Compose dont la gestion est activée. L’installation Swarm est bloquée pour ces modèles avec état.

## Paramètres

- `REDIS_PASSWORD`

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

[Installation et maintenance](operations.md)
