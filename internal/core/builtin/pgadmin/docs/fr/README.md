# pgAdmin

pgAdmin utilise un courriel administrateur et un mot de passe sur le port 80. DATABASE_NETWORK désigne un réseau PostgreSQL existant. Enregistrez ensuite le serveur manuellement ; pgAdmin ne crée pas de serveur PostgreSQL. Données : /var/lib/pgadmin.

Choisissez un Runtime Docker Compose dont la gestion est activée. L’installation Swarm est bloquée pour ces modèles avec état.

## Paramètres

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

[Installation et maintenance](operations.md)
