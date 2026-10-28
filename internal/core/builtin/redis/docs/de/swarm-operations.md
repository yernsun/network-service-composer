# Redis: Bereitstellung mit Compose und Swarm

Redis-Cache mit AOF-Persistenz; Anmeldung an Port 6379. Daten: /data.

Diese Version unterstützt Docker Compose und Swarm Services. Swarm verwendet eine Replik auf dem ausgewählten Datenknoten, lokale benannte Volumes und Updates mit vorherigem Stoppen. Ein ausgefallener Datenknoten führt nicht zur Verlagerung auf andere Knoten.

Wählen Sie einen Datenknoten mit Ready/Active. Persistente Dienste bleiben mit einer Replik auf diesem Knoten; bei Ausfall sind sie nicht verfügbar.

Beim Stoppen bleiben benannte Volumes erhalten. Ein erneuter Start erfordert aktuelle Nachweise, dass die ursprünglichen Volumes auf demselben Knoten bestehen. Fehlende oder ersetzte Volumes verhindern den Start. Knotenwechsel und zusätzliche persistente Repliken erfordern eine separate Datenmigration.

Sichern Sie Datenbankinhalte und persistente Dateien getrennt. Ein Konfigurationsrollback stellt keine Daten wieder her. Initiale Zugangsdaten gelten nur für leere Volumes. WordPress enthält MySQL; richten Sie den Website-Administrator beim ersten Besuch ein.

[README](swarm-README.md)

Bei unbekanntem Ergebnis von STOP bleibt START gesperrt, bis ein erneutes STOP das Ende der Tasks und den Erhalt der ursprünglichen Volumes bestätigt. Eine erste Installation mit unbekanntem Ergebnis darf keine leeren Ersatzvolumes erzeugen. Wenn der ursprüngliche Speicher nicht identifiziert werden kann, stellen Sie eine geprüfte Sicherung wieder her.
