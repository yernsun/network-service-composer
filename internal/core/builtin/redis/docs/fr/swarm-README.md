# Redis

Cache Redis avec persistance AOF ; authentification sur le port 6379. Données : /data.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

## Paramètres

- `REDIS_PASSWORD`

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

[Déploiement Compose et Swarm](swarm-operations.md)
