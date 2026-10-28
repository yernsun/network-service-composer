# Installation und Wartung

phpMyAdmin verbindet PMA_HOST und PMA_PORT über DATABASE_NETWORK. Melden Sie sich an Port 80 mit vorhandenen MySQL-Zugangsdaten an. Automatische Anmeldung und freie Serverwahl sind deaktiviert. Sitzungen: /sessions.

Wählen Sie eine feste Marktversion, lesen Sie die Parameter und tragen Sie eigene Geheimnisse ein. Passwörter haben keinen verwendbaren Standardwert; verwenden Sie mindestens 16 Byte. Veröffentlichen Sie keine echten Geheimnisse im Markt.

Führen Sie die Vorprüfung aus, prüfen Sie Compose, Volumes und Netzwerkabhängigkeiten und bestätigen Sie die Installation. Prüfen Sie den Vorgang und die Zustandsprüfung jedes Containers; laufend bedeutet nicht automatisch gesund.

Datenbanken stellen nur interne Ports bereit. Erstellen Sie eine HTTP-Route mit TLS nur für Webanwendungen und prüfen Sie zuerst den Zugriff vom Gateway.

Speicher und Verbindung

Sichern Sie vor Updates Volumes und Datenbanken mit geeigneten Werkzeugen und testen Sie die Wiederherstellung. Die Wiederherstellung eines Images oder einer Konfiguration stellt keine Geschäftsdaten wieder her.

Beim Stoppen bleiben benannte Volumes erhalten. Das Entfernen einer Anwendung darf persistente Daten nicht implizit löschen. Bewahren Sie Wiederherstellungsunterlagen auf und bestimmen Sie die Verantwortung für weitere Updates.

Die Vorlagen veröffentlichen keine Host-Ports. Externe Datenbanknetzwerke müssen bereits bestehen und von der Anwendung erreichbar sein.

Die Marktsynchronisierung ergänzt unveränderliche Versionen, aktualisiert aber keine installierten Workloads. Zurückgezogene Versionen bleiben im Verlauf, sind jedoch nicht neu installierbar.
