# Fehlerbehebung

*[English](../troubleshooting.md)*

## Meldungen in der App und was sie bedeuten

Cloud (Konto, Hives, Freigaben):

| Meldung | Ursache | Was tun |
|---|---|---|
| E-Mail oder Passwort falsch / Zu viele Versuche | Zehn fehlgeschlagene Anmeldungen | 15 Minuten warten oder das Passwort zurücksetzen |
| Bitte zuerst die E-Mail bestätigen | Code noch nicht eingegeben | Den Code aus der E-Mail auf der Seite *E-Mail bestätigen* eingeben. Keine E-Mail oder Code abgelaufen: *Konto erstellen* mit derselben E-Mail-Adresse erneut wählen; ein noch nicht bestätigtes Konto bekommt einen neuen Code, der alte gilt dann nicht mehr |
| Unbekannter oder abgelaufener Code | Anmeldecode des Hives älter als 10 Minuten, Bestätigungscode der E-Mail älter als 15 Minuten, oder vertippt | `hive claim` erneut ausführen / für die E-Mail *Konto erstellen* mit derselben Adresse erneut wählen |
| Dieser Hive ist bereits zugeordnet | Der Hive gehört zu einem Konto | Dort entfernen, `cloud.json` löschen, Hive neu anmelden (`hive claim`) |
| Ihr Tarif enthält das nicht | Ein Limit (Hives, Mitglieder, Freigaben, Sitzungen) | Der Kasten zeigt die Zahl; Tarif wechseln oder einen Platz freimachen |
| Der Hive hat nicht geantwortet | Hive offline oder nicht erreichbar | Siehe "Hive offline" unten |
| Der Hive ist bereits entfernt | Aktion an einem widerrufenen Hive | Hive neu anmelden (`hive claim`) |

Hive (Aufnahmen, Kameras, Live):

| Meldung | Ursache | Was tun |
|---|---|---|
| Die Uhr des Hives geht nach | Die Uhrzeit des Computers liegt mehr als eine Minute hinter der Cloud | NTP einschalten: `sudo timedatectl set-ntp true` (ein Raspberry Pi hat keine Uhrenbatterie) |
| Der Hive hat das Zugriffstoken abgelehnt | Der Hive hat die Signaturschlüssel der Cloud nicht erhalten, oder sie haben sich geändert | Den Hive neu starten; im Log nach `keys.set` suchen |
| Das Zugriffstoken ist abgelaufen | Seite lange geöffnet | Neu laden |
| Der Hive hat noch keine gültige Lizenz | Seit dem Anmelden des Hives nie mit der Cloud verbunden | Prüfen, ob er auf der Startseite online ist |
| Der Hive startet noch | Engine oder Speicher nicht bereit | Zehn Sekunden warten; bleibt es so, prüfen, ob MediaMTX installiert ist |
| Die Video-Engine des Hives hat einen Fehler gemeldet | MediaMTX hat eine Kamera oder einen Pfad abgelehnt | `engine/mediamtx.log` im Datenverzeichnis |
| Die Kamera hat nicht geantwortet / hat die Anmeldung abgelehnt | Netzwerk oder Anmeldedaten | Die RTSP-URL mit `ffprobe` von einem anderen Computer prüfen |
| An dieser Adresse antwortet ein anderes Gerät | Die Adresse einer Kamera wurde auf eine geändert, unter der eine andere Kamera oder ein anderes Gerät antwortet | Die IP prüfen; haben Sie die Kamera ersetzt, bestätigen Sie mit *Es ist eine neue Kamera, verwenden* |
| Unerwarteter Fehler (Code) | Etwas, das wir nicht übersetzt haben | Den Code mit Ihrer Meldung senden |

## Hive offline

Auf der Startseite zeigt der Hive einen grauen Punkt.

1. `hive status` auf dem Hive. Wenn gestoppt: `sudo hive start`.
2. `journalctl -u camhive-hive -n 50` (Linux) oder `logs\hive.log` im
   Datenverzeichnis (Windows). Suchen Sie nach `connected` oder dem Grund,
   warum nicht.
3. Ein Hive, der mit einem anderen Datenverzeichnis neu gestartet wurde,
   startet mit einer **neuen Identität** und ist nicht Ihr angemeldeter Hive.
   Stellen Sie sicher, dass der Dienst mit demselben `CAMHIVE_DATA_DIR`
   installiert wurde, mit dem Sie den Hive angemeldet haben.
4. Prüfen Sie die Uhr (`timedatectl`). mTLS scheitert mit einem falschen Datum.
5. Firewall: ausgehend 8443 und 8444 zu `*.camhive.app`.

## Nichts wird abgespielt, Live geht, aber das Archiv nicht

Live und Archiv nehmen verschiedene Wege (siehe [Verbindung](connection.md)).
Wenn Live funktioniert und das Archiv meldet, der Hive habe nicht geantwortet:

- Hive → *Verbindung*: Ist das Zertifikat noch *selbstsigniert*? Browser
  lehnen es auf dem Relay-Weg ab. Warten Sie auf das von Let's Encrypt; es
  wird bei jeder Neuverbindung und alle zehn Minuten angefordert.
- Weg "Nicht erreichbar" mit aufgeführten lokalen Adressen: Ihr Router
  blockiert den lokalen Namen. Erlauben Sie `h.camhive.app` in seinen
  DNS-Rebind-Einstellungen.

## Kameras werden nicht gefunden

- Eine Kamera, die funktioniert hat und nach einem Neustart des Routers
  offline ging, hat wahrscheinlich eine neue Adresse bekommen. Im eigenen Netz
  des Hives findet der Hive sie innerhalb von Minuten wieder (siehe
  [Wenn eine Kamera eine neue Adresse bekommt](cameras.md#wenn-eine-kamera-eine-neue-adresse-bekommt));
  sonst tragen Sie ihre neue Adresse unter *Bearbeiten* ein und reservieren
  sie im Router.

- Die Suche nutzt Multicast in Ihrem LAN; Kameras in einem anderen Subnetz
  oder im Gast-WLAN sind für sie unsichtbar. Nutzen Sie die manuelle Adresse.
- Bei vielen Kameras muss ONVIF erst in den Kameraeinstellungen eingeschaltet
  werden, manchmal mit einem eigenen ONVIF-Benutzer.
- RTSP-Pfade unterscheiden sich je nach Hersteller; siehe die Tabelle unter
  [Kameras](cameras.md).

## Port beim Start schon belegt

`listen tcp 127.0.0.1:16016: bind: address already in use` bedeutet, dass ein
anderer Hive-Prozess läuft, typischerweise ein `hive run` im Vordergrund neben
dem Dienst. Beenden Sie einen davon.

## Die Festplatte versagt

Der Hive prüft die Festplatte, auf die er aufnimmt, beim Start und alle 30
Minuten. Er liest SMART mit `smartctl` (installieren Sie `smartmontools`; ein
USB-Gehäuse wird erneut als SAT gefragt), die I/O-Fehler des Kernels auf
dieser Festplatte (`journalctl -k`, sonst `dmesg`), seine eigenen und die
fehlgeschlagenen Schreibvorgänge der Video-Engine und ein kleines
Testschreiben. Ausstehende oder nicht korrigierbare Sektoren, 50 oder mehr
umgelagerte Sektoren, eine fehlgeschlagene SMART-Prüfung, eine NVMe-SSD über
ihrer Nennhaltbarkeit, I/O-Fehler des Kernels oder fehlgeschlagene
Schreibvorgänge bedeuten **versagt**: Der Hive nimmt weiter auf, der
Eigentümer erhält eine Benachrichtigung (Gruppe „Speicher“) und auf der
Hive-Seite einen Hinweis mit dem, was der Hive gesehen hat. Schlägt das
Testschreiben fehl, weil die Festplatte nicht beschreibbar ist (ein I/O-Fehler
oder ein schreibgeschützt neu eingebundenes Dateisystem), ist der Zustand
**ausgefallen**: Die Aufnahme ist gestoppt. Eine volle Festplatte versagt
nicht; das ist *wenig Speicher*.

Was zu tun ist: Sichern Sie `hive.key`, `cloud.json`, `hive.yaml`,
`archive-key/` und `hive.db` aus dem Datenverzeichnis (der Hinweis nennt sie
mit vollem Pfad) und ersetzen Sie die Festplatte mit
`sudo hive move-data <neues-verzeichnis>` wie unter
[Die Festplatte ersetzen](install-hive.md#die-festplatte-ersetzen). Die
Benachrichtigung endet, wenn die Festplatte einen Tag lang gesund aussah.

Ohne `smartctl` und ohne Zugriff auf das Kernel-Protokoll ist der Zustand
*unbekannt*, ebenso auf Dateisystemen, deren Festplatte der Hive nicht
ermitteln kann (btrfs-Subvolumes, ZFS, mergerfs, Netzlaufwerke); unter
Windows und macOS ist er unbekannt, solange die eigenen Schreibvorgänge des
Hives nicht fehlschlagen. Eine Festplatte, die bei einer Prüfung schläft,
wird nicht geweckt; sie wird bei der nächsten wieder gefragt.

## Logs

| Was | Wo |
|---|---|
| Hive | `<data>/logs/hive.log` (JSON-Zeilen, 20 MB × 5) und das Dienstjournal |
| Video-Engine | `<data>/engine/mediamtx.log` (bei jedem Start der Engine geleert) |
| Dienstjournal (Linux) | `journalctl -u camhive-hive -f` |

Passwörter in Kamera-URLs werden in Logs unkenntlich gemacht.

## Das Diagnosepaket

```bash
sudo hive diag
```

schreibt `camhive-diag-<timestamp>.zip` in das Datenverzeichnis und listet
seinen Inhalt: Versionen, die Konfiguration mit maskierten Geheimnissen,
Kameras ohne Passwörter, Speicherbelegung, letzte Aktionen und Löschungen und
die Logs. Es enthält kein Video, keine Schlüssel und keine Passwörter. Lesen
Sie es durch und hängen Sie es dann an Ihre Meldung an. Der Support kann den
Hive auch über die Cloud um ein Paket bitten; es landet am selben Ort und wird
unter *Aktivität* vermerkt.
