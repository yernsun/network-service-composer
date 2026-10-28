# Installation et maintenance

MySQL avec mots de passe root et applicatif distincts. Port 3306 ; données : /var/lib/mysql.

Choisissez une version fixe du marché, lisez ses paramètres et saisissez vos propres secrets. Les mots de passe n’ont pas de valeur utilisable par défaut ; utilisez au moins 16 octets. Ne publiez aucun secret réel dans le marché.

Exécutez la vérification préalable, examinez Compose, les volumes et les dépendances réseau, puis confirmez l’installation. Consultez l’opération et la santé de chaque conteneur ; être en cours d’exécution ne garantit pas un état sain.

Les bases de données exposent uniquement des ports internes. Créez une Route HTTP avec TLS seulement pour les applications Web, après avoir vérifié l’accès depuis le Gateway.

Stockage et connexion

Avant une mise à jour, sauvegardez les volumes et les bases avec des outils adaptés et testez la restauration. Restaurer une image ou une configuration ne restaure pas les données métier.

L’arrêt conserve les volumes nommés. La suppression de l’application ne doit pas supprimer implicitement les données persistantes. Gardez les éléments de restauration et désignez le responsable des prochaines mises à jour.

Ces modèles ne publient aucun port hôte. Les réseaux externes des bases doivent déjà exister et être accessibles depuis l’application.

La synchronisation ajoute des versions immuables sans mettre à jour les charges installées. Une version retirée reste dans l’historique mais ne peut plus être installée.
