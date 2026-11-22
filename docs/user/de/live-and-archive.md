# Live und Archiv

*[English](../live-and-archive.md)*

Livebild, Aufnahmen und Kameraverwaltung teilen sich eine Seite: Öffnen Sie
einen Hive unter *Hives*, und Sie sehen alle seine Kameras.

## Live

Jede Kamera ist eine Live-Kachel. Der Schalter neben dem Hive-Namen wählt die
Kachelgröße, dargestellt als kleine Raster: **Große Kacheln**, **Mittlere
Kacheln**, **Kleine Kacheln** oder eine **Liste** ohne Video, die im
Mobilfunknetz fast keinen Datenverkehr kostet. Die Auswahl wird auf diesem
Gerät gespeichert. Es gibt keine Seiten: Scrollen Sie, um weitere Kameras zu
sehen. Nur Kacheln auf dem Bildschirm streamen; eine weggescrollte Kachel
pausiert, bis sie wieder sichtbar ist. Jede Kachel zeigt ihren Zustand:
Verbinde…, Live, Verbinde erneut…, Kamera offline oder Deaktiviert, und hat
eine Schaltfläche für Vollbild.

Klicken Sie auf eine Kamera, um sie zu **öffnen**. Sie rückt nach oben in einen
großen Player, mit den Aufnahmen des Tages auf einer Zeitleiste darunter
(siehe [Archiv](#archiv)); die anderen Kameras bleiben darunter in der
gewählten Größe. Schließen Sie sie mit ×.

Jede Kachel und jede Zeile der Liste hat eine Schaltfläche **Aufnahmen**
(Uhr-Symbol). Sie öffnet die Kamera an ihrer Zeitleiste statt live: Wählen Sie
einen Moment auf der Zeitleiste, um ihn abzuspielen, oder *Zurück zu live*.

- **Qualität.** Die Angabe *Volle Qualität* oder *Reduzierte Qualität
  (Tarif)* sagt Ihnen, welchen Stream Sie bekommen. Große Kacheln und die
  geöffnete Kamera nutzen den Hauptstream; mittlere und kleine Kacheln nutzen
  für alle den Substream; der Fernzugriff im Tarif Free nutzt ebenfalls den
  Substream. Zu Hause im eigenen Netz bekommen Sie immer volle Qualität.
- **Weg.** Das Livebild kommt per WebRTC direkt vom Hive in Ihren Browser. Zu
  Hause baut die App die Verbindung direkt mit dem Hive auf; unterwegs reicht
  die Camhive-Cloud das Verbindungsangebot weiter. Sitzen beide Seiten hinter
  striktem NAT, läuft der Stream über das Camhive-TURN-Relay; dabei wird
  nichts gespeichert.
- **Neuverbindungen** warten zunehmend länger, von 1 bis 30 Sekunden. Eine
  Kamera, die auf dem Hive offline ist, wird als offline angezeigt, statt sich
  endlos zu drehen.
- **Sitzung beendet.** Ihr Tarif erlaubt eine bestimmte Anzahl gleichzeitiger
  Fernsitzungen. Verdrängt eine neuere Anmeldung Sie, meldet die Seite das;
  schließen Sie das andere Gerät oder wechseln Sie den Tarif.
- **Ton.** Eine Kamera, die Ton aufnimmt (*Ton aufnehmen* im Formular
  *Bearbeiten* der Kamera, nur Besitzer) und ein Mikrofon hat, bekommt auf
  ihrer Kachel eine Lautsprecher-Schaltfläche neben Vollbild, sodass sie auch
  im Vollbild da ist. Live startet stumm, weil Browser Ton nicht von selbst
  abspielen; tippen Sie auf die Schaltfläche, um ihn zu hören. Die Auswahl
  wird pro Kamera auf diesem Gerät gespeichert. Verweigert der Browser beim
  Öffnen der Seite trotzdem den Ton, läuft das Bild weiter, die Schaltfläche
  zeigt wieder Ton aus, und ein Tippen schaltet ihn ein. Kameras ohne Ton
  zeigen keine Schaltfläche. Jede Kachel hat ihre eigene Schaltfläche, sodass
  mehrere Kameras gleichzeitig Ton abspielen können.
- **PTZ.** Besitzer und Bediener bekommen an der geöffneten Kamera ein
  Steuerkreuz, wenn sie PTZ unterstützt, siehe [Kameras](cameras.md#ptz).

## Archiv

Die geöffnete Kamera spielt Aufnahmen von der Festplatte des Hives; eine
eigene Archivseite gibt es nicht. Sie startet live (oder an der Zeitleiste,
wenn mit **Aufnahmen** geöffnet), und die Zeitleiste darunter zeigt heute.

1. Klicken Sie auf die **Zeitleiste**, um die Aufnahme ab diesem Moment
   abzuspielen. *Zurück zu live* kehrt zum Livebild zurück.
2. Die **Tage** unter der Zeitleiste reichen von der ältesten Aufnahme bis
   heute, in der Zeitzone Ihres Browsers; Tage mit Aufnahmen sind
   hervorgehoben, die anderen lassen sich nicht wählen. Ein früherer Tag
   öffnet dessen Aufnahmen. *Stream* wechselt zwischen Haupt- und Substream.
   Unterwegs mit einem Tarif mit reduzierter Fernqualität nutzen Zeitleiste
   und Wiedergabe immer die Aufnahmen des Substreams.
   Die Wiedergabe startet stumm. *Ton aus*/*Ton an* spielt bei Kameras, die
   Ton aufnehmen, den aufgezeichneten Ton zum Bild; er wird nur geladen,
   solange der Ton an ist (etwa 1 MB pro Minute). Exporte behalten den Ton
   immer. *Tempo* spielt mit 1×, 2×, 4× oder 8×; *Standbild* speichert das
   angezeigte Bild als PNG-Datei auf Ihrem Gerät (es wird nirgendwohin
   gesendet). Die Wiedergabe läuft von selbst in der nächsten Aufnahme des
   Tages weiter.
3. Die **Zeitleiste** zeigt den Tag: aufgezeichnete Abschnitte als Blöcke,
   **Bewegung** als rote Bänder so lang wie sie dauerte, **Speicher knapp** als
   rote Linie quer über die Leiste, wo es begann (es betrifft den ganzen Hive,
   deshalb zeigt es jede Kamera), Statuswechsel der Kamera (online/offline)
   als kleine graue Marken, eine Welle *Aktivität* (wie stark sich das Bild
   verändert hat; ein Hinweis, keine Bewegungserkennung) und bei Kameras, die
   Ton aufnehmen, eine Linie *Lautstärke*, sowie den Abspielkopf; die Zeit,
   die er anzeigt, mit Sekunden, steht über der Leiste und folgt der
   Wiedergabe. Fahren Sie mit der Maus darüber, um die Zeit unter dem Zeiger
   zu lesen; klicken Sie, um dorthin zu springen. Zoomen Sie mit dem Mausrad,
   zwei Fingern oder den Schaltflächen (ganzer Tag, 6 Stunden, 1 Stunde um
   den Abspielkopf); + und − funktionieren auch. Hineingezoomt folgt die
   Leiste dem Abspielkopf, und Ziehen (oder Wischen auf dem Handy) schiebt die
   Aufnahme unter dem Abspielkopf; über den ganzen Tag verschiebt Ziehen den
   Abspielkopf. Beim Loslassen wird ab dort abgespielt. Die Pfeiltasten
   springen eine Minute, mit Umschalt zehn Minuten.
4. Ein Klick in eine Lücke springt zum nächsten aufgezeichneten Moment; folgt
   nichts mehr, meldet die Seite das.
5. **Ereignisse (n)** unter der Zeitleiste listet die Ereignisse des Tages,
   das Neueste zuerst: Bewegung mit ihrer Dauer, Speicher knapp und
   Statuswechsel der Kamera. Klicken Sie auf eines, um die Aufnahme ab diesem
   Moment abzuspielen. Bewegung, die noch andauert, ist mit *läuft noch*
   markiert.

### Bewegung

Der Hive markiert Bewegung für jede Kamera; dafür wird nichts an die Cloud
gesendet und kein Tarif gebraucht. Er nimmt die Bewegung aus einer von zwei
Quellen, gewählt pro Kamera:

- **Von der Kamera erkannt.** Eine über ONVIF hinzugefügte Kamera, die
  Ereignisse anbietet, meldet Bewegung selbst; sobald sie das tut, nutzt der
  Hive diese Meldung. Schalten Sie die Bewegungserkennung in den Einstellungen
  der Kamera selbst ein und stellen Sie dort ihre Zonen und Empfindlichkeit
  ein. Ist die Erkennung aus, meldet die Kamera nichts, und der Hive
  beobachtet stattdessen das Bild. Ein Rekorder (NVR), der als eine Kamera
  hinzugefügt wurde, meldet die Bewegung aller seiner Kanäle zusammen.
- **Im Bild erkannt.** Bei jeder anderen Kamera (über RTSP-Adresse
  hinzugefügt oder ohne ONVIF-Ereignisse) beobachtet der Hive, wie groß die
  komprimierten Bilder des Substreams sind: Eine ruhige Szene ist klein,
  Bewegung macht die Bilder um ein Mehrfaches größer. Nichts wird
  dekodiert, deshalb kostet das fast keine Rechenleistung, auch nicht auf
  einem Raspberry Pi. Es ist ein guter Hinweis, keine Alarmanlage: Eine
  Kamera mit *konstanter Bitrate* (CBR) zeigt wenig, starkes Bildrauschen in
  der Nacht verdeckt kleine Bewegungen, ein eingeschaltetes Licht kann eine
  Sekunde lang wie Bewegung aussehen, und Bewegung, die länger als etwa
  anderthalb Minuten andauert, geht im Hintergrund unter. Stellen Sie den
  Substream der Kamera für die besten Ergebnisse auf *variable Bitrate*
  (VBR).

Kurze Pausen (unter 10 Sekunden) zählen als eine Bewegung, und eine Bewegung
dauert höchstens 30 Minuten; die Ereignisliste zeigt, wie jede erkannt wurde.

Die Wiedergabe streamt fragmentiertes MP4 direkt vom Hive und startet im
lokalen Netz innerhalb von ein bis zwei Sekunden. Über das Relay hängt sie von
der Upload-Bandbreite bei Ihnen zu Hause ab.

## Export

Besitzer und Bediener können aus der geöffneten Kamera einen Clip schneiden.
Springen Sie zum Anfang, wählen Sie *Ab hier exportieren*, geben Sie die Länge
in Minuten ein und starten Sie. Der Hive schreibt ein MP4 des gewählten Streams
ohne Neukodierung und listet es unter dem Player; laden Sie es dort herunter.
Die maximale Länge hängt vom Tarif des Besitzers ab: 5 Minuten bei Free, 60 bei
Plus, unbegrenzt bei Pro. Der lokale Export im eigenen Netz ist nie durch den
Tarif begrenzt.

Die Liste der Exporte gilt pro Nutzer und übersteht einen Neustart des Hives.
Ein Export, der noch lief, als der Hive stoppte, erscheint als fehlgeschlagen;
starten Sie ihn neu. Fertige Exporte werden standardmäßig 7 Tage und bis zu
insgesamt 10 GB aufbewahrt; danach gehen die ältesten zuerst. Der Besitzer kann
beide Grenzen in `hive.yaml` ändern (`export_days`, `export_max_gb`). Laden Sie
herunter, was Sie behalten wollen.

## Aufbewahrung

Der Hive löscht Aufnahmen, die älter als die eingestellte Anzahl Tage sind
(standardmäßig 30), und hält unabhängig davon mindestens den eingestellten
freien Platz auf der Aufnahmefestplatte frei: Alte Exporte gehen zuerst, dann
die ältesten vollständigen Segmente. Die Aufnahme stoppt nie wegen der
Aufbewahrung; lässt sich die Festplatte nicht freiräumen, meldet der Hive
"Speicher knapp" und macht weiter; die Zeitleiste jeder Kamera zeigt, wann es
begann und, sobald wieder Platz ist, wann es endete.
Ein Wechsel in einen kleineren Tarif löscht nie etwas.

### Wie lange die Festplatte reicht

**Hive → Verbindung → Speicher → Speicher anzeigen…** zeigt, was der Hive
hält (ab v0.1.10; ein älterer Hive zeigt, welche Version es bringt):

- die Größe der Festplatte, die Aufnahmen darauf, die Exporte und den freien
  Platz;
- pro Kamera: die gespeicherten Tage, ihre Größe, wie viel sie pro Tag
  schreibt (der größere Durchschnitt aus dem letzten Tag und der letzten
  Woche, damit eine gestern erhöhte Bitrate sofort sichtbar ist; Raten und
  benötigter Platz sind aufgerundet) und die Tage, die sie behalten wird;
- einen Satz, der alles zusammenfasst, zum Beispiel *Bei 46 GB pro Tag hält
  diese Festplatte 11 Tage; die Aufbewahrung ist 30 Tage, tatsächlich werden
  es also 11.* Kann die Festplatte die Aufbewahrung nicht halten, nennt ein
  Hinweis den nötigen Platz und wann der Hive beginnt, die ältesten
  Aufnahmen für Platz zu löschen, oder dass er es gerade tut. Behält Ihr
  Tarif Aufnahmen weniger Tage als die Einstellung, sagt der Block das. Verkürzen Sie die Aufbewahrung, nehmen Sie
  weniger auf (niedrigere Bitrate, ohne Substream) oder verwenden Sie eine
  größere Festplatte; die Aufnahme stoppt deshalb nie.

Der Besitzer ändert dort zwei Einstellungen: **Aufnahmen behalten (Tage)**
(0 bis 3650; bei 0 bleiben Aufnahmen, bis der Platz ausgeht; die eigene
Aufbewahrung einer Kamera, siehe [Kameras](cameras.md), geht vor) und **Auf
der Festplatte frei halten (GiB)** (mindestens 1 GiB, also 1,07 GB, und
weniger als die Festplatte; der Wert ist in GiB, wie in `hive.yaml`). Sie werden in `hive.yaml` geschrieben und gelten beim nächsten
Aufräumlauf, innerhalb von fünf Minuten, ohne Neustart; das
Aktionsprotokoll hält die Änderung fest (`storage.settings_changed`). Löschen
die neuen Werte bei diesem Lauf Aufnahmen (eine Kamera mit dem Wert des
ganzen Hives hat ältere Aufnahmen als die neue Zahl Tage, oder der neue
freie Platz liegt über dem freien Platz), fragt die App vorher nach:
Gelöschte Aufnahmen lassen sich nicht wiederherstellen. Den freien Platz auf
0 zu setzen, was die Platzregel abschaltet, geht nur von Hand in
`hive.yaml`, und die Festplatte kann dann volllaufen.

Mitglieder sehen nur die gespeicherten Tage und die Größe der Aufnahmen der
mit ihnen geteilten Kameras.
Die Zahlen folgen derselben Rechnung wie der
[Speicherrechner](/de/calculator/) auf der Website, mit
dem, was Ihre Kameras wirklich schreiben.

## Aufnahmen löschen

Der Besitzer kann Aufnahmen sofort von der Festplatte des Hives entfernen, zum
Beispiel wenn eine gefilmte Person die Löschung verlangt (DSGVO Art. 17).
Mitglieder, auch Bediener, können das nicht. Es funktioniert im Heimnetz ohne
die Cloud.

- **Hive → Verbindung → Aufnahmen löschen** wählt die Kameras (oder *Alle
  Kameras, auch entfernte*, was auch Aufnahmen inzwischen entfernter Kameras
  umfasst) und was gelöscht wird: *Alles*, *Ein Zeitraum* oder Aufnahmen
  *Älter als* eine Anzahl Tage. *Auch Exporte dieser Aufnahmen löschen*
  entfernt auch Exporte, die sich damit überschneiden; sonst bleiben diese
  Kopien.
- In einer geöffneten Kamera startet **Diesen Tag löschen…** dasselbe Formular
  mit dieser Kamera und diesem Tag vorausgefüllt.
- **Vorschau** zeigt pro Kamera, was der Hive wirklich löschen wird: den
  Zeitraum, die Anzahl der Dateien und ihre Größe. Aufnahmen sind Dateien von
  wenigen Minuten (`segment_minutes`); eine Datei, die den gewählten Zeitraum
  berührt, wird ganz gelöscht, der Zeitraum kann also etwas größer sein als
  gewählt, nie kleiner. Beide Streams (Haupt- und Substream) gehen immer
  zusammen.
- **Endgültig löschen** löscht sofort und gibt den Platz frei. Es gibt keinen
  Papierkorb und kein Rückgängig. Bevor *Alles* gelöscht wird, müssen Sie den
  Namen der Kamera (eine Kamera) oder des Hives eintippen.
- Die Datei, die gerade aufgezeichnet wird, wird innerhalb weniger Minuten
  gelöscht, sobald der Hive sie schließt; Vorschau und Ergebnis sagen, wie
  viele das sind. Wird gerade noch ein Export dieser Aufnahmen erstellt,
  lehnt der Hive ab, bis er fertig ist.
- Jede Löschung wird im eigenen Aktionsprotokoll des Hives festgehalten (wer,
  welche Kameras, welcher Zeitraum, wie viel), das sich nicht löschen lässt.
  Die App zeigt dieses Protokoll noch nicht; der Besitzer kann es über die API
  des Hives lesen (`GET /v0/actions`).

## Verschlüsselte Aufnahmen

Der Besitzer kann den Hive seine Aufnahmen verschlüsseln lassen, sodass
niemand, der das Gerät oder seine Festplatte mitnimmt, sie abspielen kann. Die
Verschlüsselung ist aus, bis der Besitzer sie einschaltet, und lässt sich nicht
wieder ausschalten. Der Besitzer findet sie im Reiter **Verbindung** des Hives
unter **Verschlüsselte Aufnahmen** → **Verschlüsselung verwalten…** (dafür
braucht es Hive-Version 0.1.7 oder neuer).

- **Einschalten.** Die App erklärt zuerst, was das bedeutet: Ohne die
  Passphrase *und* die Schlüsseldatei kann niemand die Aufnahmen wieder
  abspielen, auch Camhive nicht, und die Verschlüsselung lässt sich nicht
  ausschalten. Nach **Verstanden, Passphrase wählen** tippen Sie zweimal eine
  Passphrase mit 10 bis 256 Zeichen ein und wählen
  **Verschlüsselung einschalten**. Die App bittet Sie dann, sofort
  **Schlüsseldatei herunterladen** zu wählen, und ist erst danach fertig;
  bewahren Sie die Datei getrennt vom Hive auf (ein USB-Stick, Ihr Passwort-Manager). Die Schlüsseldatei allein nützt einem Dieb
  nichts. Ab dann wird jede Aufnahme wenige Sekunden nach dem Schließen ihrer
  Datei verschlüsselt (Dateien sind 2 Minuten lang, solange die
  Verschlüsselung an ist), und im Hintergrund auch die Aufnahmen und Exporte,
  die schon auf der Festplatte liegen.
- **Nach jedem Neustart** des Hives (ein Update, ein Stromausfall) ist das
  Archiv **gesperrt**. Der Hive nimmt wie immer auf und das Livebild läuft, und
  die Zeitleiste zeigt weiter, wann es Aufnahmen gibt, aber Wiedergabe, Ton und
  Exporte warten, bis der Besitzer das Archiv mit der Passphrase entsperrt.
  Die Hive-Seite weist darauf hin: Der Besitzer tippt auf **Entsperren**, gibt
  die Passphrase ein, und die Aufnahmen spielen wieder; Mitglieder lesen, dass
  das Archiv von seinem Besitzer gesperrt ist. Derselbe Hinweis steht unter
  einer geöffneten Kamera, wo die Schaltflächen für Ton und Export ausgegraut
  sind, solange es gesperrt ist. Im Reiter Verbindung kann der Besitzer die
  Aufnahmen auch jederzeit wieder sperren (**Aufnahmen sperren**). Fünf falsche
  Passphrasen in 15 Minuten blockieren das Entsperren für 15 Minuten; bei einem
  Hive neuer als 0.1.7 sagt die App, wie lange es noch dauert, bei 0.1.7
  selbst nur, dass es bis zu 15 Minuten sind. Entsperren und Sperren
  funktionieren auch mit der Notfall-Anmeldung in Ihrem Netz
  ([Verbindung](connection.md)); Einschalten der Verschlüsselung, Ändern der
  Passphrase und Herunterladen der Schlüsseldatei brauchen eine Anmeldung über
  Camhive, und die App sagt das, statt sie anzubieten.
- **Automatisch sperren** (ab Hive-Version 0.1.10). Unter **Automatisch
  sperren** legt der Besitzer fest, wann der Hive die Aufnahmen von selbst
  sperrt, damit der Schlüssel nicht wochenlang in seinem Arbeitsspeicher
  bleibt. Beides ist aus, bis Sie es ändern, und was zuerst fällig ist, sperrt:
  - **Wenn niemand Aufnahmen angesehen hat seit** 1 Stunde, 8 Stunden,
    24 Stunden oder 7 Tagen. Eine Aufnahme abspielen, ihren Ton, einen Export
    starten oder herunterladen, egal von wem, startet die Zeit neu; das
    Entsperren ebenso. Ein Mitglied, das weiter zusieht, hält die Aufnahmen
    entsperrt; nur die zweite Einstellung sperrt, während jemand zusieht.
  - **Wenn ich das Haus verlasse**, nach 10 Minuten oder 1 Stunde unterwegs.
    Die App meldet dem Hive, wenn sie ihn nicht mehr in Ihrem Netz erreicht
    und stattdessen über das Camhive-Relay geht, und wenn sie ihn zu Hause
    wieder erreicht; wer innerhalb der Zeit zurückkommt, hebt die Sperre auf,
    damit ein WLAN-Aussetzer nicht sperrt. Melden kann die App das nur,
    solange sie auf einer Hive-Seite oder im Reiter Verbindung geöffnet ist
    (sie erinnert den Hive einmal pro Minute daran):
    Gehen Sie mit geschlossener App oder bleibt Ihr Telefon zu Hause, gilt nur
    die erste Einstellung. Bei mehreren Geräten zählt das, das zuletzt
    gemeldet hat. Entsperren Sie von unterwegs, beginnt die Zeit neu.

  Läuft eine Zeit, zeigt der Reiter, wann die Aufnahmen sperren, etwa
  „Sperrt in 42 Minuten“. Der Hive sperrt wie die Schaltfläche **Aufnahmen
  sperren**: Laufende Wiedergabe endet bei ihrer nächsten Anfrage, Mitglieder lesen, dass das Archiv von
  seinem Besitzer gesperrt ist, und das Aktivitätsprotokoll zeigt die Sperre
  mit ihrem Grund (niemand hat zugesehen, oder Sie sind unterwegs). Entsperren
  braucht immer Ihre Passphrase; nichts entsperrt sich von selbst, wenn Sie
  heimkommen. Die Camhive-Cloud ist nicht beteiligt: Sie hat den Schlüssel
  nicht und erfährt nicht, ob Sie zu Hause sind. Diese Einstellungen ändern
  braucht eine Anmeldung über Camhive. Unterwegs können Sie sie verschärfen;
  eine Sperre ausschalten oder verlängern geht nur zu Hause in Ihrem Netz,
  damit niemand, der an Ihre Anmeldung gekommen ist, sie von außen lockern
  kann. Ein Hive, der geschlafen hat (ein Laptop mit geschlossenem Deckel),
  sperrt beim Aufwachen sofort, wenn die Zeit inzwischen abgelaufen ist.
- **Passphrase ändern** (**Passphrase ändern…**: die aktuelle, dann zweimal
  die neue) lässt die Aufnahmen, wie sie sind. Eine früher heruntergeladene
  Schlüsseldatei öffnet sich nur mit der alten Passphrase, deshalb bietet die
  App direkt nach der Änderung **Neue Schlüsseldatei herunterladen** an.
- **Warnt die App, dass neue Aufnahmen unverschlüsselt gespeichert werden,**
  konnte der Hive den Archivschlüssel nicht von seiner Festplatte lesen. Das
  Entsperren der Aufnahmen schreibt ihn neu, und die Verschlüsselung läuft
  weiter.
- **Exporte** werden auf dem Hive verschlüsselt gespeichert und als normale
  MP4-Dateien heruntergeladen, solange das Archiv entsperrt ist.
- **Was eine gestohlene Festplatte noch zeigt:** die letzten Minuten jeder
  Kamera, die noch nicht verschlüsselt waren, wann und wie viel jede Kamera
  aufgenommen hat (nicht was) und die Ereignisse. Solange das Archiv entsperrt
  ist, liegt der Schlüssel im Arbeitsspeicher des Hives: Ein Computer, der in
  den Ruhezustand geht oder Arbeitsspeicher auf die Festplatte auslagert, kann
  ihn dorthin schreiben. Unter Windows können ihn auch Absturzabbilder
  enthalten, die ein Administrator eingeschaltet hat.
