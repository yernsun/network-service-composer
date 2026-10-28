# Redis

Redis-Cache mit AOF-Persistenz; Anmeldung an Port 6379. Daten: /data.

Diese Version unterstützt Docker Compose und Swarm Services. Swarm verwendet eine Replik auf dem ausgewählten Datenknoten, lokale benannte Volumes und Updates mit vorherigem Stoppen. Ein ausgefallener Datenknoten führt nicht zur Verlagerung auf andere Knoten.

## Parameter

- `REDIS_PASSWORD`

Wählen Sie einen Datenknoten mit Ready/Active. Persistente Dienste bleiben mit einer Replik auf diesem Knoten; bei Ausfall sind sie nicht verfügbar.

[Bereitstellung mit Compose und Swarm](swarm-operations.md)
