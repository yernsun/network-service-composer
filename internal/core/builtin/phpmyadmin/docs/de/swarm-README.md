# phpMyAdmin

phpMyAdmin verbindet PMA_HOST und PMA_PORT über DATABASE_NETWORK. Melden Sie sich an Port 80 mit vorhandenen MySQL-Zugangsdaten an. Automatische Anmeldung und freie Serverwahl sind deaktiviert. Sitzungen: /sessions.

Diese Version unterstützt Docker Compose und Swarm Services. Swarm verwendet eine Replik auf dem ausgewählten Datenknoten, lokale benannte Volumes und Updates mit vorherigem Stoppen. Ein ausgefallener Datenknoten führt nicht zur Verlagerung auf andere Knoten.

## Parameter

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

Wählen Sie einen Datenknoten mit Ready/Active. Persistente Dienste bleiben mit einer Replik auf diesem Knoten; bei Ausfall sind sie nicht verfügbar.

[Bereitstellung mit Compose und Swarm](swarm-operations.md)
