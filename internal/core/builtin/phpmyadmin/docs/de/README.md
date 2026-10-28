# phpMyAdmin

phpMyAdmin verbindet PMA_HOST und PMA_PORT über DATABASE_NETWORK. Melden Sie sich an Port 80 mit vorhandenen MySQL-Zugangsdaten an. Automatische Anmeldung und freie Serverwahl sind deaktiviert. Sitzungen: /sessions.

Wählen Sie eine Docker-Compose-Laufzeit mit aktivierter Verwaltung. Die Installation dieser zustandsbehafteten Vorlagen unter Swarm ist gesperrt.

## Parameter

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

Wählen Sie eine feste Marktversion, lesen Sie die Parameter und tragen Sie eigene Geheimnisse ein. Passwörter haben keinen verwendbaren Standardwert; verwenden Sie mindestens 16 Byte. Veröffentlichen Sie keine echten Geheimnisse im Markt.

[Installation und Wartung](operations.md)
