# PostgreSQL

PostgreSQL 18 nutzt Port 5432. Mounten Sie /var/lib/postgresql statt des alten Pfads /var/lib/postgresql/data.

Wählen Sie eine Docker-Compose-Laufzeit mit aktivierter Verwaltung. Die Installation dieser zustandsbehafteten Vorlagen unter Swarm ist gesperrt.

## Parameter

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

Wählen Sie eine feste Marktversion, lesen Sie die Parameter und tragen Sie eigene Geheimnisse ein. Passwörter haben keinen verwendbaren Standardwert; verwenden Sie mindestens 16 Byte. Veröffentlichen Sie keine echten Geheimnisse im Markt.

[Installation und Wartung](operations.md)
