# WordPress

WordPress enthält einen eigenen MySQL-Dienst. Öffnen Sie den Installer an Port 80 erst nach Datenbankbereitschaft. Erhalten Sie database-data und wordpress-data.

Diese Version unterstützt Docker Compose und Swarm Services. Swarm verwendet eine Replik auf dem ausgewählten Datenknoten, lokale benannte Volumes und Updates mit vorherigem Stoppen. Ein ausgefallener Datenknoten führt nicht zur Verlagerung auf andere Knoten.

## Parameter

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

Wählen Sie einen Datenknoten mit Ready/Active. Persistente Dienste bleiben mit einer Replik auf diesem Knoten; bei Ausfall sind sie nicht verfügbar.

[Bereitstellung mit Compose und Swarm](swarm-operations.md)
