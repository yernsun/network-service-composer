# pgAdmin: Déploiement Compose et Swarm

pgAdmin utilise un courriel administrateur et un mot de passe sur le port 80. DATABASE_NETWORK désigne un réseau PostgreSQL existant. Enregistrez ensuite le serveur manuellement ; pgAdmin ne crée pas de serveur PostgreSQL. Données : /var/lib/pgadmin.

Cette version prend en charge Docker Compose et les services Swarm. Swarm utilise une seule réplique sur le nœud choisi, des volumes locaux nommés et des mises à jour avec arrêt préalable. Un nœud hors ligne ne provoque pas de déplacement vers un autre nœud.

Choisissez un nœud Ready/Active. Les services persistants restent sur ce nœud avec une seule réplique ; ils sont indisponibles si le nœud est hors ligne.

L’arrêt conserve les volumes nommés. Le redémarrage exige des preuves récentes de la présence des volumes d’origine sur le même nœud. Les volumes absents ou remplacés bloquent le démarrage. Un changement de nœud ou des répliques persistantes supplémentaires exigent une migration distincte.

Sauvegardez séparément la base et les fichiers persistants. Le retour à une configuration antérieure ne restaure pas les données. Les identifiants initiaux ne concernent que les volumes vides. WordPress inclut MySQL ; configurez l’administrateur du site lors de la première visite.

[README](swarm-README.md)

Si le résultat de STOP est inconnu, START reste bloqué jusqu’à ce qu’un nouveau STOP confirme la fin des tâches et la conservation des volumes d’origine. Une première installation au résultat inconnu ne peut pas utiliser des volumes de remplacement vides. Si le stockage d’origine ne peut pas être identifié, restaurez une sauvegarde vérifiée.
