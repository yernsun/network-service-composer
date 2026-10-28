# pgAdmin

pgAdmin utilise un courriel administrateur et un mot de passe sur le port 80. DATABASE_NETWORK désigne un réseau PostgreSQL existant. Enregistrez ensuite le serveur manuellement ; pgAdmin ne crée pas de serveur PostgreSQL. Données : /var/lib/pgadmin.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

## Paramètres

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

[Déploiement Compose et Swarm](swarm-operations.md)
