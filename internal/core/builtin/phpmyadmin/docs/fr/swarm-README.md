# phpMyAdmin

phpMyAdmin se connecte à PMA_HOST et PMA_PORT via DATABASE_NETWORK. Connectez-vous sur le port 80 avec les identifiants MySQL existants. La connexion automatique et le choix arbitraire du serveur sont désactivés. Sessions : /sessions.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

## Paramètres

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

[Déploiement Compose et Swarm](swarm-operations.md)
