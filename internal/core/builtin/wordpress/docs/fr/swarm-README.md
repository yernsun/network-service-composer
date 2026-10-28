# WordPress

WordPress inclut un service MySQL dédié. Attendez la disponibilité de la base avant d’ouvrir l’installation sur le port 80. Conservez database-data et wordpress-data.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

## Paramètres

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

[Déploiement Compose et Swarm](swarm-operations.md)
