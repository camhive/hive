# Kameras

*[English](../cameras.md)*

Öffnen Sie einen Hive: Seine Seite zeigt alle Kameras. Nur der Besitzer kann
Kameras hinzufügen, bearbeiten, ausschalten oder entfernen; Mitglieder sehen
die Kameras, die für sie freigegeben sind. *Kamera hinzufügen* ist die letzte
Kachel (oder die letzte Zeile der Liste). Bearbeiten, Ausschalten und Entfernen
finden Sie an der geöffneten Kamera und in der Liste.

## Kamera über ONVIF hinzufügen (empfohlen)

1. *Kamera hinzufügen* → *Im Netz suchen (ONVIF)*. Der Hive durchsucht Ihr Netz
   einige Sekunden lang und listet die Geräte, die geantwortet haben.
2. Wählen Sie ein Gerät oder tippen Sie seine Dienstadresse von Hand ein
   (üblich ist `http://192.168.1.50/onvif/device_service`). Geben Sie
   Benutzername und Passwort der Kamera ein und drücken Sie *Profile lesen*.
3. Der Hive liest Hersteller, Modell und Firmware und listet die Profile der
   Kamera. Das größte Profil ist als **Hauptstream** vorausgewählt, das
   kleinste als **Substream**. Geben Sie der Kamera einen Namen und optional
   eine Gruppe und speichern Sie.

Kameras mit falscher Anmeldung antworten "Die Kamera hat die Anmeldung
abgelehnt". Kameras in einem anderen VLAN oder hinter einer WLAN-Isolierung
findet die Suche nicht; nutzen Sie dann die manuelle Adresse oder das
RTSP-Formular.

## Kamera über RTSP-Adresse hinzufügen

Nutzen Sie das für Kameras ohne ONVIF oder wenn die Suche sie nicht sieht.

| Feld | Bedeutung |
|---|---|
| Name | Pflichtfeld, bis zu 100 Zeichen |
| Gruppe | Optionale Bezeichnung zum Sortieren der Liste |
| RTSP-URL | Der **Hauptstream**, `rtsp://host:554/path`. Schreiben Sie die Anmeldedaten nicht in die URL |
| Substream-URL | Optional: der zweite, kleinere Stream der Kamera (`rtsp://` oder `rtsps://`, ohne Anmeldedaten in der URL); es gilt dieselbe Anmeldung |
| Kamera-Benutzer, Kamera-Passwort | Die Anmeldung der Kamera; der Hive speichert das Passwort verschlüsselt |

Den Pfad finden Sie im Handbuch der Kamera oder auf der Support-Seite des
Herstellers. Häufige Pfade:

| Kamera | Haupt | Sub |
|---|---|---|
| Sonoff CAM-S2 | `rtsp://IP:554/av_stream/ch0` | `rtsp://IP:554/av_stream/ch1` |
| Reolink | `rtsp://IP:554/h264Preview_01_main` | `…_sub` |
| Hikvision / HiLook | `rtsp://IP:554/Streaming/Channels/101` | `…/102` |
| Dahua / Amcrest | `rtsp://IP:554/cam/realmonitor?channel=1&subtype=0` | `subtype=1` |
| TP-Link Tapo | `rtsp://IP:554/stream1` | `stream2` |

Eine schnelle Prüfung von jedem Computer, auf dem ffmpeg installiert ist:

```bash
ffprobe -rtsp_transport tcp "rtsp://user:pass@192.168.1.50:554/av_stream/ch0"
```

Tragen Sie den Substream ein, wenn die Kamera einen hat (Spalte Sub der Tabelle
oben): Raster und Fernzugriff nutzen ihn dann statt des vollen Streams. Sie
können ihn später unter **Bearbeiten** hinzufügen, ändern oder entfernen; ein
leeres Feld entfernt ihn. Hives älter als v0.1.8 übernehmen den Substream nur
beim Hinzufügen der Kamera; zum Bearbeiten braucht es v0.1.8.

## Haupt- und Substream

Jede Kamera hat einen **Hauptstream** (volle Qualität) und kann einen
**Substream** haben (reduziert). Beide werden aufgezeichnet. Die App nutzt den
Substream für mittlere und kleine Kacheln und für den Fernzugriff im Tarif
Free; hat eine Kamera nur einen Stream, nutzt die App diesen.

## Einschalten, bearbeiten, entfernen

- **Aktiv** (*Ausschalten* / *Einschalten* an der geöffneten Kamera) heißt,
  dass der Hive die Kamera abruft und aufzeichnet. Schalten Sie sie aus, um die
  Aufnahme zu stoppen und die Kamera samt Verlauf zu behalten. Ausgeschaltete
  Kameras bleiben für den Besitzer sichtbar, damit er sie wieder einschalten
  kann.
- **Bearbeiten** ändert Name, Gruppe, Anmeldung, Adresse und bei RTSP-Kameras
  die Substream-URL. Lassen Sie das Passwort leer, um das aktuelle zu
  behalten. Eine neue Adresse (IP oder Hostname) behält die Kamera, ihre
  Aufnahmen und ihre Freigaben; Ports und Pfade bleiben. Antwortet dort ein
  anderes Gerät, meldet der Hive das und verschiebt die Kamera nur, wenn Sie
  bestätigen, dass es eine neue Kamera ist (dann lernt er das neue Gerät).
- **Entfernen** löscht die Kamera aus dem Hive. Ihre Aufnahmen bleiben auf der
  Festplatte, bis die Aufbewahrung sie entfernt, und zwar nach der Einstellung
  des ganzen Hives: Die eigene Aufbewahrung einer Kamera verschwindet mit der
  Kamera.

## Wenn eine Kamera eine neue Adresse bekommt

Router zu Hause vergeben Adressen, die sich ändern können, etwa nach einem
Stromausfall. Der Hive merkt sich, was jede Kamera ist: ihre Netzwerkkarte
(MAC) und bei ONVIF-Kameras ihre Geräte-ID und Seriennummer. Ist eine Kamera im
eigenen Netz des Hives zwei Minuten lang offline, sucht der Hive nach ihr: Er
bittet ONVIF-Kameras, sich zu melden, und spricht still jede Adresse seines
Netzes an (höchstens 254), um zu sehen, welches Gerät welche MAC hat. Danach
sucht er nach 2, 5 und 15 Minuten erneut und stündlich, solange die Kamera
fehlt.

Er verschiebt die Kamera nur, wenn das gefundene Gerät zu allem passt, was er
weiß, und die Anmeldung der Kamera annimmt. Die Anmeldung wird nur an ein Gerät
gesendet, dessen MAC oder Geräte-ID bereits passt, und nur als Digest, nie im
Klartext. Die Kamera behält ihre ID, Aufnahmen und Freigaben; die Zeitleiste
zeigt ein Ereignis *Kameraadresse geändert* (`camera.address_changed`).

Das Bearbeitungsformular der Kamera zeigt, was der Hive über sie weiß und ob
er sie auf diese Weise finden kann:

- *Bekommt die Kamera vom Router eine neue Adresse, findet der Hive sie
  wieder.* — Sie ist im Netz des Hives.
- *Diese Kamera ist in einem anderen Netz als der Hive* — hinter einem Router
  oder in einem anderen VLAN. Dort kann der Hive nicht suchen; reservieren Sie
  die Adresse der Kamera in Ihrem Router und ändern Sie sie von Hand, wenn sie
  wechselt.

Adressen für Kameras im Router zu reservieren (DHCP-Reservierung, "immer
diese IP zuweisen") ist in jedem Fall die zuverlässigste Einrichtung. Kameras,
die vor dieser Version hinzugefügt wurden, lernen ihre Identität bei ihrer
nächsten Verbindung; bis dahin lässt sich ein Umzug nicht automatisch beheben.
Zwei gleiche RTSP-Kameras hinter einem WLAN-Repeater, der MAC-Adressen
umschreibt, lassen sich nicht unterscheiden; ändern Sie ihre Adresse von Hand.

## Status

Kacheln und Liste zeigen den Online-Status jeder Kamera, alle 10 Sekunden
aktualisiert. Eine Kamera ist online, wenn ihr Hauptstream Daten liefert; zwei
fehlgeschlagene Prüfungen hintereinander (etwa 10 Sekunden) markieren sie als
offline. Änderungen werden als Ereignisse festgehalten, sichtbar als Marken auf
der Zeitleiste des Archivs und unter *Aktivität*.

## Kamerazustand

Der Hive beobachtet auch, wie es jeder Kamera geht, damit Sie von einem
defekten Kabel oder einer ausgefallenen Beleuchtung erfahren, bevor Aufnahmen
fehlen. Trifft ein Grund zu, zeigt die Kameraseite eine Zustandszeile mit dem,
was der Hive gemessen hat, und einem Rat, und die Kamera bekommt ein kleines
Zeichen in der Liste und auf ihrer Kachel. Das sieht jeder, der die Kamera
ansehen darf, auch Personen, mit denen sie geteilt ist. Der Besitzer erhält
eine Benachrichtigung, wenn eine Kamera Aufmerksamkeit braucht (Einstellung
*Offline* unter *Benachrichtigungen*, höchstens eine pro Tag und Kamera), und
keine, wenn es vorbei ist: Das Zeichen verschwindet.

| Grund | Wann der Hive ihn meldet | Was zu tun ist |
|---|---|---|
| Bitrate gesunken | Ein Stream lieferte in der letzten Stunde weniger als 60 % dessen, was er zu dieser Stunde sonst liefert. „Sonst“ ist dieselbe Uhrzeit (und die Stunden daneben) an den letzten 7 Tagen, eine jeden Abend ruhige Szene ist also normal; der Hive braucht zuerst 3 Tage Verlauf. | Auch eine ruhigere Szene senkt die Bitrate; sieht das Bild falsch aus, prüfen Sie das Kabel oder das PoE-Budget |
| Aufnahmen fehlen | Mehr als 30 Sekunden Aufnahme fehlen in der letzten Stunde, oder drei Prüfungen hintereinander (etwa 15 Minuten) kommt kein neues Segment, während die Kamera online ist | Prüfen Sie das Kabel und aktualisieren Sie dann die Firmware der Kamera |
| Neuverbindungen | Der Stream brach in der letzten Stunde 4-mal oder öfter ab und kam wieder | Prüfen Sie das Kabel oder das PoE-Budget |
| Uhr geht falsch | Die Uhr einer ONVIF-Kamera wich beim letzten Lesen (alle 6 Stunden) mehr als 30 Sekunden von der des Hives ab | Stellen Sie den NTP-Server der Kamera ein |
| Ausfallmuster | An 3 der letzten 7 Tage in derselben Stunde 5 Minuten oder länger offline | Prüfen Sie, was zu dieser Stunde Strom oder Netzwerk der Kamera abschaltet |
| Nachtbild | Nachts (22:00–06:00) fiel die Bitrate unter 60 % der eigenen früheren Nächte und unter 25 % des Tages: Das Bild wurde dunkel | Die IR-Beleuchtung ist vielleicht ausgefallen |
| Stream geändert | Codec oder Auflösung änderten sich in den letzten 24 Stunden, ohne dass jemand die Kamera in Camhive geändert hat | Prüfen Sie die Videoeinstellungen der Kamera und aktualisieren Sie ihre Firmware, wenn sie sie zurückgesetzt hat |

Was nicht zählt: Zeit, in der die Kamera offline war (dafür gibt es eine eigene
Benachrichtigung), Zeit, in der der Hive oder seine Video-Engine nicht lief
(ein Neustart, ein Hochfahren, ein Update), und ein Neustart der Quelle direkt
nachdem Sie die Kamera geändert haben. Verspätete Segmente meldet der Hive
nicht, solange sein Speicher knapp ist; dafür gibt es einen eigenen Hinweis.
Ein Nachtbild verschwindet am Tag; die anderen Gründe verschwinden erst
deutlich jenseits ihrer Schwelle (Bitrate über 80 %, höchstens 5 Sekunden
fehlend, höchstens eine Neuverbindung in der Stunde), damit sie nicht kommen
und gehen. Neuverbindungen zählt der Hive seit seinem letzten Start.

## PTZ

Schwenken, Neigen und Zoomen funktioniert bei ONVIF-Kameras, die einen
PTZ-Dienst anbieten. Besitzer und Bediener sehen an der geöffneten Kamera ein
Steuerkreuz: Halten Sie eine Richtung gedrückt, um die Kamera zu bewegen,
tippen Sie auf eine gespeicherte Position, um dorthin zu fahren. Reine
RTSP-Kameras lassen sich nicht bewegen.

## Bewegungsempfindlichkeit

Jede Kamera hat eine **Bewegungsempfindlichkeit**: Kamera öffnen → *Kamera
bearbeiten* (nur der Besitzer). Sie bestimmt, wie leicht der Hive an dieser
Kamera auf Bewegung reagiert:

| Empfindlichkeit | Reagiert auf | Gedacht für |
|---|---|---|
| Niedrig | größere Bewegung, die mindestens etwa 4 Sekunden dauert | einen Baum, eine Straße, wechselndes Licht |
| Normal (Standard) | Bewegung von etwa 2 Sekunden | die meisten Kameras |
| Hoch | kleine Bewegung von einer Sekunde | einen ruhigen Raum, ein fernes Tor |

Mit der eigenen Bewegungserkennung der Kamera (ONVIF) verlangt *Niedrig*
zusätzlich, dass die Kamera 3 Sekunden lang Bewegung meldet; *Normal* und
*Hoch* übernehmen die Meldung der Kamera, wie sie kommt. Stellen Sie die
Empfindlichkeit dann auf der Webseite der Kamera ein.

**Beruhigen.** Hat eine Kamera mehr als 20 Bewegungen in einer Stunde, macht
der Hive sie von selbst eine Stufe weniger empfindlich, und das Formular
zeigt *Beruhigt seit 14:10*. Nach einer Stunde ohne Bewegung kehrt sie zu
ihrer Empfindlichkeit zurück. Beides steht in der Aktivität des Hives.
Beruhigen hebt die Schwelle, es beginnen also weniger Bewegungen; die, die
beginnen, stehen wie immer auf der Zeitleiste. Ein Neustart des Hives
beginnt jede Kamera unberuhigt. Mit der eigenen Erkennung der Kamera
verlangt eine beruhigte Kamera eine längere Meldung: 1 Sekunde bei *Hoch*,
3 bei *Normal*, 6 bei *Niedrig*.

Das Feld erscheint auf Hives, die es anbieten (ab v0.1.10). Über die API des
Hives: `PATCH /v0/cameras/{id}` mit `sensitivity` `low`, `normal` oder
`high`. Wie aus Bewegung eine Benachrichtigung wird, steht unter
[Benachrichtigungen](account-and-plans.md#warum-bewegung-ruhig-bleibt).

## Aufnahmeeinstellungen

Die Aufnahme wird in `hive.yaml` auf dem Hive eingestellt: Segmentlänge (10
Minuten), Aufbewahrung in Tagen (30) und die Mindestmenge an freiem Speicher
(10 GB), unter der die ältesten Aufnahmen zuerst entfernt werden. Siehe die
[Konfigurationsreferenz](../../operations/config-reference.md#hive-hiveyaml)
(englisch). Die eigene Aufbewahrung einer Kamera stellen Sie in der App ein
(unten).

### Aufnahmen einer Kamera länger oder kürzer behalten

Eine Kamera kann ihre Aufnahmen eine eigene Anzahl von Tagen behalten statt
des Werts für den ganzen Hive: eine Einfahrtskamera 90 Tage, eine Innenkamera
3.

**In der App** (nur Besitzer): Öffnen Sie die Kamera → *Kamera bearbeiten* →
**Aufnahmen behalten**:

- *Hive-Standard*: Die Kamera folgt dem Wert des ganzen Hives (neue Kameras
  beginnen hier);
- *Anzahl Tage*, dann **Tage**: Ganze Tage von 0 bis 3650 behalten die
  Aufnahmen dieser Kamera so viele Tage; bei 0 bleiben sie, bis die Festplatte
  den Platz braucht.

Wählen Sie wieder *Hive-Standard*, folgt die Kamera wieder dem Wert des ganzen
Hives. Das Feld erscheint bei Hives, die es anbieten (ab v0.1.9); bei älteren
Hives hat das Formular kein solches Feld.

**Über die API des Hives**: `PATCH /v0/cameras/{id}` mit `retention_days`
(nur Besitzer), eine Zahl von 0 bis 3650 oder `null` für den Wert des ganzen
Hives.

Die Änderung gilt beim nächsten Aufräumlauf, innerhalb von fünf Minuten, ohne
Neustart, und das Aktionsprotokoll hält sie fest. Die längste Aufbewahrung
Ihres Tarifs begrenzt die Tage einer Kamera genauso wie den Wert des ganzen
Hives. Die Mindestmenge an freiem Speicher gilt weiter für jede Kamera: Wird
der Platz knapp, gehen die ältesten Aufnahmen zuerst, egal wie lange ihre
Kamera sie behalten soll.

Die eigene Aufbewahrung einer Kamera braucht einen Hive neuer als 0.1.7. Nach
der Rückkehr zu einer älteren Version behält der Hive die Einstellung in seiner
Datenbank, ignoriert sie aber: Jede Kamera folgt dem Wert des ganzen Hives,
sodass eine 90-Tage-Kamera unter einem 30-Tage-Hive beim nächsten Aufräumlauf
die Tage 30–90 verliert. Die Engine-Ports in `hive.yaml` werden auf dieselbe
Weise ignoriert.
