# Hive installieren

*[English](../install-hive.md)*

Ein Hive ist ein einzelnes Programm, `hive`, mit der Video-Engine MediaMTX
daneben. Er läuft als Systemdienst, hält seine Daten in einem Verzeichnis und
spricht erst mit der Cloud, wenn Sie ihn in der App angemeldet haben.

## Voraussetzungen

- Linux (x86-64 oder ARM64, systemd) oder Windows 10/11 (x86-64). Windows auf
  ARM wird nicht unterstützt: Für die Video-Engine gibt es dafür keinen Build.
  Ein Raspberry Pi 4 oder 5 mit 64-Bit-Debian eignet sich gut für zu Hause.
- Eine Festplatte für Aufnahmen. Rechnen Sie grob mit 1 GB pro Kamera und
  Stunde bei 1080p H.264; eine 1-TB-Festplatte fasst etwa einen Monat einer
  Kamera oder eine Woche von vier.
- Ausgehender Internetzugang zu `gw.camhive.app:8443` und
  `relay.camhive.app:8444`. Eine Portweiterleitung ist nicht nötig.
- Die Uhr des Computers muss stimmen. Lassen Sie NTP eingeschaltet; geht die
  Uhr nach, lehnt der Hive Zugriffstokens ab.

## Linux mit deb oder rpm

```bash
sudo dpkg -i camhive-hive_*_linux_amd64.deb     # or: sudo rpm -i camhive-hive_*_linux_amd64.rpm
```

Das Paket installiert den Hive und seine Video-Engine in `/usr/lib/camhive`
(`hive`, `mediamtx`), verlinkt `/usr/bin/hive` dorthin, richtet den Dienst
`camhive-hive` ein und startet ihn. Die Daten liegen in `/var/lib/camhive`, die
Konfiguration in `/etc/camhive/hive.yaml`. Die Lizenzen liegen in
`/usr/share/doc/camhive-hive`.

Dann den Hive anmelden:

```bash
sudo hive claim --api https://api.camhive.app
```

## Linux aus dem Archiv (jede Distribution, Raspberry Pi)

Laden Sie `hive_<version>_linux_<arch>.tar.gz` herunter und entpacken Sie es.
Es enthält `hive` und die Video-Engine `mediamtx`; lassen Sie beide im selben
Verzeichnis, der Hive sucht die Engine neben sich. Zum Beispiel:

```bash
sudo install -m 755 hive mediamtx /usr/local/bin/
```

Sollen die Aufnahmen auf eine andere Festplatte (typisch bei einem Raspberry Pi
mit USB-Laufwerk), legen Sie das Datenverzeichnis **bei der Installation**
fest: Es wird in die Dienstdefinition geschrieben.

```bash
sudo CAMHIVE_DATA_DIR=/media/usb/camhive hive install
```

```bash
sudo CAMHIVE_DATA_DIR=/media/usb/camhive hive claim --api https://api.camhive.app
```

```bash
sudo hive start
```

#### Eine externe Festplatte: über fstab einbinden, per UUID

Binden Sie die Festplatte **vor** `hive install` über `/etc/fstab` ein, per
UUID und mit `nofail`, nie über das automatische Einbinden des Desktops (die
Ordner unter `/media/<benutzer>/`, die beim Anstecken erscheinen): Das kommt
erst, wenn sich jemand anmeldet, und eine USB-Festplatte, die kurz wegfällt
und wiederkommt, bekommt einen anderen Gerätenamen und eine neue Einbindung,
die der Hive nicht kennt. Nehmen Sie lieber ext4; NTFS geht, markiert aber
nach einem verlorenen Schreibvorgang die ganze Festplatte als fehlerhaft und
lässt sich dann erst nach einer Reparatur unter Windows wieder beschreibbar
einbinden.

```bash
sudo blkid /dev/sda1
```

Notieren Sie die `UUID="…"`, legen Sie den Einhängepunkt an und tragen Sie eine
Zeile in `/etc/fstab` ein (hier für ext4 unter `/mnt/cameras`):

```bash
sudo mkdir -p /mnt/cameras
```

```
UUID=0a1b2c3d-…  /mnt/cameras  ext4  defaults,nofail,x-systemd.device-timeout=30s  0  2
```

```bash
sudo mount -a
```

Installieren Sie dann mit `CAMHIVE_DATA_DIR=/mnt/cameras/camhive`. Liegt das
Datenverzeichnis oder `archive_dir` aus `hive.yaml` auf einer anderen
Festplatte als das System, bekommt der Dienst dafür `RequiresMountsFor=`, und
sobald der Hive dort seine Identität hat auch `ConditionPathExists=…/hive.key`.
Der Hive führt diese Zeilen in
`/etc/systemd/system/camhive-hive.service.d/mounts.conf`: `hive install`
schreibt die Datei, `hive move-data` schreibt sie für die neue Festplatte
neu, und jeder Start des Dienstes bringt sie auf den Stand, wirksam ab dem
nächsten Start. Ein Hive, der vor Version 0.1.11 installiert wurde, ist also
nach seinem zweiten Start mit 0.1.11 geschützt; sofort geschützt ist er, wenn
Sie nach dem Update `hive install` mit demselben `CAMHIVE_DATA_DIR` noch einmal
ausführen. systemd startet den Hive dann nie, solange die Festplatte fehlt.
Ein Hive, der sein Datenverzeichnis leer auf der Systemfestplatte findet,
während der Dienst dort eine Festplatte erwartet, wird kein neuer, nicht
angemeldeter Hive: Er wartet, `hive status` meldet
`NOT RECORDING … disk_not_mounted`, und er startet von selbst innerhalb von
30 Sekunden, sobald die Festplatte eingebunden ist. (Die Cloud hört solange
nichts von ihm und meldet ihn offline; und weil `hive.yaml` auf dieser
Festplatte liegt, antwortet er `hive status` bis dahin unter der
Standardadresse 127.0.0.1:16016.) Nach einem Neustart ohne die
Festplatte binden Sie sie ein und führen `sudo hive start` aus.

Fällt die Festplatte weg, während der Hive läuft, merkt er das innerhalb
einer Minute, meldet **Aufnahme gestoppt** (sofort eine Benachrichtigung, ein
roter Hinweis in der App, `hive status`) und läuft weiter. Ist die Festplatte
wieder da, starten Sie den Hive neu (`sudo hive restart`): Er hält noch die
Dateien der alten Einbindung.

Alles, was dem Hive gehört, liegt im Datenverzeichnis: `hive.yaml`
(Konfiguration), `hive.key` (seine Identität, nie weitergeben), `hive.db`,
`archive/`, `exports/`, `logs/hive.log`, `engine/` (MediaMTX-Konfiguration und
-Log).

### Die Festplatte ersetzen

Wenn die Festplatte versagt (die App zeigt einen Hinweis, siehe
[Fehlerbehebung](troubleshooting.md#die-festplatte-versagt)) oder einfach voll
ist, ziehen Sie den Hive auf eine neue um. Kopieren Sie zuerst diese Dateien
aus dem Datenverzeichnis von Hand an einen anderen Ort, in dieser Reihenfolge;
sie sind klein, und ohne sie lässt sich der Hive nicht wiederherstellen:

1. `hive.key`: die Identität des Hives
2. `cloud.json`: seine Anmeldung (daneben `hive.crt` und `cloud-ca.crt`)
3. `hive.yaml`: seine Einstellungen (`/etc/camhive/hive.yaml`, wenn der Hive
   ohne `CAMHIVE_DATA_DIR` installiert wurde)
4. `archive-key/`: der Schlüsselbund; ohne ihn ist ein verschlüsseltes Archiv
   verloren
5. `hive.db`: Kameras, Index, Freigaben und das Aktivitätsprotokoll

Schließen Sie dann die neue Festplatte an, binden Sie sie ein (in
`/etc/fstab` per UUID mit `nofail`, wie unter
[Eine externe Festplatte](#eine-externe-festplatte-über-fstab-einbinden-per-uuid),
nicht über das automatische Einbinden des Desktops; ext4 oder ein anderes
Linux-Dateisystem, nicht FAT, exFAT oder NTFS) und führen Sie aus, mit einem einfachen Verzeichnisnamen
(Buchstaben, Ziffern, `/ . - _`; keine Leerzeichen, keine Shell-Zeichen wie
`; & | $ * ?`):

```bash
sudo hive move-data /mnt/newdisk/camhive
```

Der Befehl lehnt ein Verzeichnis auf der zu ersetzenden Festplatte ab. Er
startet den Hive neu, sodass er antwortet, aber nicht aufnimmt, kopiert und
prüft (Größe und SHA-256, von der Festplatte zurückgelesen) die Dateien oben,
`exports/` und dann die Aufnahmen, die neuesten zuerst, bis die neue
Festplatte zu 80 % voll wäre oder den freien Mindestplatz des Hives
(`min_free_gb`) erreicht; ältere Aufnahmen bleiben auf der alten Festplatte.
Ein Segment, das die alte Festplatte nicht lesen kann, bleibt dort und wird
gezählt; der Umzug geht weiter. Während des Kopierens zeigt die App den Hive
und seine Aufnahmen, und Sie können sich anmelden und das Archiv
entsperren, aber Änderungen (Kameras, Einstellungen, die lokale Anmeldung,
neue Exporte) werden mit „Der Hive zieht auf eine neue Festplatte um“
abgelehnt, bis der Umzug beendet ist; `hive.db` wird am Ende mit den
Ereignissen und Anmeldungen dieser Zeit erneut kopiert. Dann richtet er den
Dienst auf das neue Verzeichnis aus (ein Drop-in,
`/etc/systemd/system/camhive-hive.service.d/data-dir.conf`, mit
`RequiresMountsFor=` und `ConditionPathExists=` für die neue Festplatte),
startet den
Hive dort und wartet, bis er antwortet; startet er nicht, kommt die alte
Definition zurück und der Hive läuft wieder von der alten Festplatte. Er
nennt, was nicht gepasst hat oder nicht lesbar war, und löscht oder ändert
auf der alten Festplatte nichts. `archive/` oder `archive-key/` dürfen
Verweise auf eine andere Festplatte sein: Kopiert wird, worauf sie zeigen. In
der App zeigt **Einstellungen → Aufnahmen und Festplatte → Auf eine
neue Festplatte umziehen** dieselben Schritte und den Fortschritt.

Wird der Befehl unterbrochen (Strom, volle Festplatte), führen Sie ihn mit
demselben Verzeichnis erneut aus: Er macht dort weiter, wo er aufgehört hat,
und kopiert nicht erneut, was schon geprüft ist. Bis er fertig ist, nimmt der
Hive nicht auf; `hive status` sagt es. Um den Umzug abzubrechen und wieder auf
die alte Festplatte aufzunehmen (das geht auch, wenn der Dienst schon auf das
neue Verzeichnis zeigt; die Dienstdefinition wird so zurückgesetzt, wie sie
vor dem Umzug war):

```bash
sudo hive move-data --cancel
```

Der geführte Umzug braucht Linux mit systemd (deb, rpm oder `hive install`).
Anderswo stoppen Sie den Dienst, kopieren das Datenverzeichnis von Hand und
installieren den Dienst mit `CAMHIVE_DATA_DIR` auf den neuen Ort erneut.

## Windows

Entpacken Sie `hive_<version>_windows_amd64.zip` nach
`C:\Program Files\Camhive` (es enthält `hive.exe` und die Video-Engine
`mediamtx.exe`; lassen Sie beide zusammen) und führen Sie dann in einer
PowerShell **als Administrator** aus:

```powershell
cd 'C:\Program Files\Camhive'; .\hive.exe install; .\hive.exe claim --api https://api.camhive.app; .\hive.exe start
```

Die Daten liegen in `C:\ProgramData\Camhive`. Der Dienst heißt `camhive-hive`
und erscheint unter Dienste als "Camhive Hive".

## Hive anmelden

`hive claim` gibt aus:

```
Claim code: 7K3M-Q2XD
Enter it in the Camhive app (Add hive) within 10 minutes. Waiting...
```

Wählen Sie in der App *Hive hinzufügen*, tippen Sie den Code ein (Groß- und
Kleinschreibung spielt keine Rolle, der Bindestrich ist optional), geben Sie
dem Hive einen Namen und bestätigen Sie. Der Befehl gibt dann
`Claimed. Hive id hv_…` aus und bittet Sie, den Dienst neu zu starten:

```bash
sudo hive restart
```

Ein Hive lässt sich einmal anmelden. Um ihn in ein anderes Konto zu
verschieben, entfernen Sie ihn in der App (Hive → *Einstellungen* → *Diesen Hive
entfernen* → **Hive entfernen**), löschen Sie `cloud.json` aus dem
Datenverzeichnis und melden Sie ihn neu an. Die Aufnahmen bleiben auf der
Festplatte.

## Dienstbefehle

| Befehl | Wirkung |
|---|---|
| `hive status` | `running`, `stopped` oder `unknown`, dann was der laufende Hive meldet: seine Version, das letzte Update oder die letzte Zurücknahme, die angebotene Version, ob dieser Befehl älter ist als der Hive, und eine Zeile `lan: https://<address>:<port>` pro lokaler Adresse (um den Hive [ohne Internet](connection.md#ohne-internet-den-hive-über-seine-adresse-öffnen) zu öffnen). Endet mit 0, ob ein Hive antwortet oder nicht, mit 1 nur, wenn der Dienststatus nicht lesbar ist; Skripte lesen die erste Zeile |
| `hive start` / `stop` / `restart` | Den Dienst steuern |
| `hive install` / `uninstall` | Den Dienst einrichten oder entfernen; Aufnahmen bleiben erhalten. Der Dienst startet `hive-updater`, der den Hive startet (siehe Aktualisieren) |
| `hive run` | Im Vordergrund laufen (zum Ausprobieren) |
| `hive diag` | Ein Diagnosepaket schreiben, siehe [Fehlerbehebung](troubleshooting.md) |
| `hive version` | Version, Commit und Build-Datum dieses Befehls, markiert mit `(this binary)`, dann die Version des laufenden Hives; `hive --version` gibt nur die erste Zeile aus |
| `hive update` | `hive-updater` bitten, ein verfügbares Update jetzt zu installieren (braucht root / Administrator); dasselbe wie Jetzt aktualisieren in der App |
| `hive move-data <neues-verzeichnis>` | Den Hive auf eine neue Festplatte umziehen (root; siehe [Die Festplatte ersetzen](#die-festplatte-ersetzen)); `--cancel` bricht einen unfertigen Umzug ab |

Mit einem eigenen Datenverzeichnis nimmt ein Befehl ohne `CAMHIVE_DATA_DIR`
das aus der installierten Dienstdefinition (Linux); sonst stellen Sie ihm
`CAMHIVE_DATA_DIR=…` voran, oder der Befehl sucht am Standardort.

## Ports, die der Hive nutzt

| Port | Wo | Zweck |
|---|---|---|
| 16016/tcp | nur localhost | Unverschlüsselte lokale API (HTTP) |
| 16443/tcp | alle Schnittstellen | Lokale TLS-API für die Web-App in Ihrem Netz und für das Relay |
| 8890/udp | alle Schnittstellen | WebRTC-Livebild (`engine_webrtc_udp_port`) |
| 8889, 9997/tcp | nur localhost | Interna von MediaMTX (`engine_webrtc_port`, `engine_api_port`) |
| ein freier Port/tcp, beim Start gewählt | nur localhost | Kamerastreams, die der Hive an MediaMTX übergibt |

Öffnen Sie 16443/tcp und 8890/udp in der Firewall des Computers selbst, falls
er eine hat. Am Router muss nichts geöffnet werden. Belegt ein anderes Programm
schon einen der MediaMTX-Ports, verlegen Sie ihn in `hive.yaml`
([Konfigurationsreferenz](../../operations/config-reference.md#hive-hiveyaml),
englisch).

## Aktualisieren

Der Dienst startet `hive-updater`, einen kleinen Prozess, der den Hive startet,
ihn neu startet, wenn er stoppt, und signierte Updates installiert. Eine neue
Version läuft neben der alten; ist sie nicht innerhalb von fünf Minuten gesund
(der Hive antwortet und jede Kamera, die vorher aufgezeichnet hat, zeichnet
wieder auf), kehrt der Updater von selbst zur alten Version zurück und versucht
diese Version nie wieder. Wie das abgesichert ist:
[Hive updates](../../security/updates.md) in der Entwicklerdokumentation
(englisch).

**In der App:** Hive → *Einstellungen* → *Updates* (nur Besitzer) zeigt die
laufende Version, ein wartendes Update und das letzte Update oder die letzte Zurücknahme.
Dort wählen Sie automatische oder manuelle Updates, das Update-Fenster und den
Kanal, und **Jetzt aktualisieren** installiert ein wartendes Update sofort,
unabhängig von Modus und Fenster. Bei automatischen Updates zeigt eine wartende
Version, wann sie installiert wird (im Fenster); eine Version, die einen
neueren `hive-updater` braucht, fordert Sie auf, stattdessen das aktuelle Paket
zu installieren, weil Jetzt aktualisieren sie nicht installieren kann. Das
Ergebnis (aktualisiert oder zurückgenommen) erscheint im Aktivitätsprotokoll des
Hives. Update-Einstellungen brauchen eine Anmeldung über Camhive; die lokale
Notfall-Anmeldung kann sie nicht ändern. Die App schreibt Ihre Auswahl in
`hive.yaml`; der Hive übernimmt sie bei seiner nächsten Update-Prüfung,
innerhalb einer Minute nach dem Speichern, ohne Neustart. Ein Hive, dessen
Updater älter als v0.1.7 ist, liest `hive.yaml` nur beim Start seines Dienstes:
Die App weist darauf hin, und `sudo hive restart` (oder die Installation des
aktuellen Pakets) übernimmt die Änderung. Eine Funktion, die einen neueren Hive
braucht, zeigt, mit welcher Version sie kommt; der Besitzer bekommt dort
ebenfalls **Jetzt aktualisieren**.

Einstellungen in `hive.yaml` (die ersten drei gibt es auch in der App):

| Einstellung | Standard | Bedeutung |
|---|---|---|
| `updates` | `automatic` | `manual`: Der Hive meldet nur, dass ein Update verfügbar ist; Jetzt aktualisieren in der App oder `hive update` installiert es. Auch Sicherheitskorrekturen warten |
| `update_window` | `02:00-05:00` | Wann automatische Updates installiert werden dürfen, in der Ortszeit des Hives, `HH:MM-HH:MM` mit zwei verschiedenen Uhrzeiten; darf über Mitternacht gehen. Sicherheitskorrekturen warten nicht |
| `update_channel` | `stable` | `beta`, um neue Versionen früher zu bekommen |
| `update_url` | `https://updates.dev.camhive.app` | Woher signierte Update-Informationen kommen; sie werden gegen Schlüssel geprüft, die im Hive eingebaut sind, dem Server selbst wird also nicht vertraut. In der App nicht änderbar |

Änderungen an diesen Einstellungen von Hand in `hive.yaml` gelten genauso: bei
der nächsten Prüfung, ohne Neustart (ab v0.1.7).

Der Updater bewahrt seine Versionen in `/var/lib/camhive-updater` auf
(Windows: `C:\Program Files\Camhive\updater`), nur für root / Administratoren
lesbar, nie im Datenverzeichnis. Installierte und zurückgenommene Updates
erscheinen im Aktivitätsprotokoll.

**Wie Sie ein Update bemerken:** Der Hive prüft den Kanal einmal pro Stunde und
installiert innerhalb des Fensters. Das Aktivitätsprotokoll des Hives zeigt
`update.available`, wenn eine neue Version gefunden wurde, `update.applied`,
wenn sie läuft, und `update.rolled_back`, wenn die neue Version nicht gesund
war; das Dienstprotokoll schreibt `hive started ... version=` mit der laufenden
Version. Der `hive`-Befehl, den Sie eintippen, ist das Programm, das das Paket
installiert hat; der Updater startet seine eigene Kopie der neueren Version,
daher können die beiden sich unterscheiden. Beide Befehle fragen den laufenden
Hive über seine lokale API (`listen_addr`):

```
$ hive status
running
hive: 0.1.6 (a73f337, 2026-11-15)
last update: 0.1.5 -> 0.1.6, applied 2026-11-16 03:12 +01:00
update on offer: 0.1.7
this command: 0.1.5 (0af03ce), older than the running hive; hive-updater runs its own copy
lan: https://192.168.1.23:16443

$ hive version
camhive-hive 0.1.5 (0af03ce) (this binary)
running hive: 0.1.6 (a73f337, 2026-11-15)
this command: 0.1.5 (0af03ce), older than the running hive; hive-updater runs its own copy
```

`last update` zeigt `rolled_back`, wenn die neue Version nicht gesund war;
`needs a package install` nennt eine Version, die der Updater nicht selbst
installieren kann. Ein älterer Befehl schadet nicht: Installieren Sie das neue
Paket, wenn es erscheint, oder ignorieren Sie es.
`hive: not answering on 127.0.0.1:16016` bedeutet, dass dort kein Hive läuft
(gestoppt oder startet noch). Ohne Leserecht für `hive.yaml` melden die Befehle
`hive.yaml not readable, tried the default 127.0.0.1:16016` und fragen dort;
wurde `listen_addr` verlegt, führen Sie sie mit `sudo` aus. Hives mit v0.1.4
oder älter haben noch keinen Updater und brauchen einmal eine manuelle
Installation des neuen Pakets oder Archivs:

- **deb oder rpm:** Installieren Sie das neue Paket. Der Dienst, sein
  Datenverzeichnis (`CAMHIVE_DATA_DIR`), Identität, Konfiguration und
  Aufnahmen bleiben erhalten; ein Dienst, der den Hive direkt gestartet hat,
  wird auf den Updater umgestellt.
- **Archiv:** Ersetzen Sie `hive`, `hive-updater` und `mediamtx` durch die
  Dateien aus dem neuen Archiv, dann `sudo hive install` (stellt den Dienst auf
  den Updater um, falls er es noch nicht war) und `sudo hive restart`.
