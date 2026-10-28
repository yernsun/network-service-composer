# pgAdmin

pgAdmin nutzt an Port 80 Administrator-E-Mail und Passwort. DATABASE_NETWORK muss ein vorhandenes PostgreSQL-Netzwerk sein. Registrieren Sie nach der Anmeldung den Datenbankserver manuell; pgAdmin erstellt keinen PostgreSQL-Server. Daten: /var/lib/pgadmin.

Diese Version unterstützt Docker Compose und Swarm Services. Swarm verwendet eine Replik auf dem ausgewählten Datenknoten, lokale benannte Volumes und Updates mit vorherigem Stoppen. Ein ausgefallener Datenknoten führt nicht zur Verlagerung auf andere Knoten.

## Parameter

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Wählen Sie einen Datenknoten mit Ready/Active. Persistente Dienste bleiben mit einer Replik auf diesem Knoten; bei Ausfall sind sie nicht verfügbar.

[Bereitstellung mit Compose und Swarm](swarm-operations.md)
