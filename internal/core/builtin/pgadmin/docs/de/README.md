# pgAdmin

pgAdmin nutzt an Port 80 Administrator-E-Mail und Passwort. DATABASE_NETWORK muss ein vorhandenes PostgreSQL-Netzwerk sein. Registrieren Sie nach der Anmeldung den Datenbankserver manuell; pgAdmin erstellt keinen PostgreSQL-Server. Daten: /var/lib/pgadmin.

Wählen Sie eine Docker-Compose-Laufzeit mit aktivierter Verwaltung. Die Installation dieser zustandsbehafteten Vorlagen unter Swarm ist gesperrt.

## Parameter

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Wählen Sie eine feste Marktversion, lesen Sie die Parameter und tragen Sie eigene Geheimnisse ein. Passwörter haben keinen verwendbaren Standardwert; verwenden Sie mindestens 16 Byte. Veröffentlichen Sie keine echten Geheimnisse im Markt.

[Installation und Wartung](operations.md)
