# WordPress

WordPress enthält einen eigenen MySQL-Dienst. Öffnen Sie den Installer an Port 80 erst nach Datenbankbereitschaft. Erhalten Sie database-data und wordpress-data.

Wählen Sie eine Docker-Compose-Laufzeit mit aktivierter Verwaltung. Die Installation dieser zustandsbehafteten Vorlagen unter Swarm ist gesperrt.

## Parameter

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

Wählen Sie eine feste Marktversion, lesen Sie die Parameter und tragen Sie eigene Geheimnisse ein. Passwörter haben keinen verwendbaren Standardwert; verwenden Sie mindestens 16 Byte. Veröffentlichen Sie keine echten Geheimnisse im Markt.

[Installation und Wartung](operations.md)
