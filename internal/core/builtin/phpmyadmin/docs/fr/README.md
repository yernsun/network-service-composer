# phpMyAdmin

phpMyAdmin se connecte à PMA_HOST et PMA_PORT via DATABASE_NETWORK. Connectez-vous sur le port 80 avec les identifiants MySQL existants. La connexion automatique et le choix arbitraire du serveur sont désactivés. Sessions : /sessions.

Choisissez un Runtime Docker Compose dont la gestion est activée. L’installation Swarm est bloquée pour ces modèles avec état.

## Paramètres

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

[Installation et maintenance](operations.md)
