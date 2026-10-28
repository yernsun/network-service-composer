# PostgreSQL

PostgreSQL 18 utilise le port 5432. Montez /var/lib/postgresql plutôt que l’ancien chemin /var/lib/postgresql/data.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

## Paramètres

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

[Déploiement Compose et Swarm](swarm-operations.md)
