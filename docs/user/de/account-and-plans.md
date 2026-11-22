# Konto und Tarife

*[English](../account-and-plans.md)*

## Konto

*Konto* enthält alles über Sie selbst, nicht über Ihre Hives.

- **Passwort.** Mindestens 10 Zeichen. Eine Änderung meldet alle anderen
  Geräte ab.
- **Zwei-Faktor-Authentifizierung.** *Zwei-Faktor aktivieren* zeigt einen
  QR-Code und das Geheimnis als Text; bestätigen Sie mit einem Code aus Ihrer
  Authenticator-App. Danach erhalten Sie acht Wiederherstellungscodes, einmal
  angezeigt, mit einer Schaltfläche zum Herunterladen. Jeder
  Wiederherstellungscode meldet Sie einmal an, falls Sie die App verlieren. Zum
  Deaktivieren brauchen Sie Ihr Passwort und einen aktuellen Code.
- **Sitzungen.** Jedes angemeldete Gerät mit Browser und letzter Aktivität;
  beenden Sie jede Sitzung, die Sie nicht kennen.
- **Passwort vergessen?** auf der Anmeldeseite schickt einen Link, der eine
  Stunde gilt.

Die Anmeldung wird pro Adresse gebremst und nach zehn fehlgeschlagenen
Versuchen für 15 Minuten gesperrt.

## Tarife

Camhive ist ein Abonnement pro Konto; jeder Hive, der Ihnen gehört, ist bis zur
Anzahl des Tarifs enthalten. Kameras werden nie gezählt, Datenverkehr wird nie
gemessen.

| | Free | Plus | Pro |
|---|---|---|---|
| Preis | 0 | 5 €/Monat · 50 €/Jahr | 12 €/Monat · 120 €/Jahr |
| Hives | 1 | 3 | 10 (+2 €/Monat für jeden weiteren) |
| Kameras, Ansehen im eigenen Netz, lokaler Export | unbegrenzt | unbegrenzt | unbegrenzt |
| Nutzer des Kontos (inkl. Besitzer) | 3 | 5 | 15 |
| Gleichzeitige Fernsitzungen | 2 | 4 | 10 |
| Fernzugriff | reduzierte Qualität (Substream) | volle Qualität | volle Qualität |
| Kamerafreigaben (Empfänger) | 2 | 5 | 15 |
| Fernexport | 5/Tag, bis 5 Min. | unbegrenzt, bis 60 Min. | unbegrenzt, API |
| Benachrichtigungen | E-Mail + Push, 1 Push-Gerät | E-Mail + Push, 5 Push-Geräte | E-Mail + Push, 20 Push-Geräte, + Webhooks (geplant) |
| Analyse | Bewegung | + Objekterkennung (geplant) | + Gesichter/Kennzeichen, Suche (geplant) |
| Früher Zugang zu Updates | | | ja |

*Tarif* zeigt Ihren aktuellen Tarif, Zusatzpakete und die Vergleichstabelle,
so wie die Cloud sie liefert; die Zahlen dort gelten, falls diese Seite
hinterherhinkt.

**Während der Beta** vergeben wir bezahlte Tarife selbst, und Promo-Codes
funktionieren; Kartenzahlung kommt später. Geben Sie einen Code unter *Tarif*
→ *Einlösen* ein; jeder Code gilt einmal pro Konto.

**Wenn ein Tarif endet,** wird nichts gelöscht. Überzählige Hives ruhen (sie
zeichnen weiter auf und bleiben in Ihrem Netz erreichbar), überzählige
Mitglieder werden deaktiviert, Fernsitzungen fallen auf die Anzahl von Free
zurück. Ein erneutes Upgrade stellt alles wieder her. Sie erhalten Hinweise 14,
7 und 1 Tag vor Ablauf.

## Benachrichtigungen

Camhive benachrichtigt den Besitzer eines Hives, wenn etwas Aufmerksamkeit
braucht. Öffnen Sie *Konto* → *Benachrichtigungen*, um zu wählen, was Sie wie
erreicht.

| Ereignis | Wann es gesendet wird | Standard |
|---|---|---|
| Eine Kamera ist offline | wenn sie 3 Minuten lang kein Video gesendet hat; eine Kamera, die innerhalb dieser Zeit zurück ist, löst nichts aus | E-Mail und Push |
| Ein Hive ist nicht erreichbar | wenn er 5 Minuten lang von Camhive getrennt war (Stromausfall, Internet weg) | E-Mail und Push |
| Wieder online | nach einer Offline-Benachrichtigung, wenn die Kamera oder der Hive zurück ist, auf denselben Kanälen | wie die Offline-Benachrichtigung |
| Wenig Speicher | wenn die Archivfestplatte den von Ihnen eingestellten freien Platz nicht mehr halten kann; jeweils einmal, wenn das beginnt | E-Mail und Push |
| Bewegung | wenn an einer Kamera Bewegung beginnt, die eine Wartezeit lang (10 Minuten, wenn Sie nichts ändern) ruhig war; nach einer Push-Meldung schweigt die Kamera, bis sie so lange ruhig war | Push (E-Mail ist aus) |

Push braucht zuerst ein Gerät (siehe unten); bis dahin erhalten Sie nur E-Mails.

Die Seite zeigt, was die letzte Woche gebracht hat, zum Beispiel *Diese
Woche: 5.600 Bewegungen, 312 Push-Benachrichtigungen.* So sehen Sie, was
jede Einstellung bewirkt.

### Wann Benachrichtigungen kommen

Bewegung hat ein **Wann**:

- *Jedes Mal* (Standard): wie in der Tabelle oben.
- *Stündliche Zusammenfassung*: eine Benachrichtigung am Ende jeder Stunde
  mit Bewegung, zum Beispiel *14 Bewegungen an 3 Kameras, erste 08:12,
  letzte 17:40*. Ein Tippen öffnet die Aufnahmen des Hives an diesem Tag.
- *Tägliche Zusammenfassung*: eine Benachrichtigung um Mitternacht für den
  Tag.
- *Nie*: keine Bewegungsmeldungen; die Bewegungen bleiben auf der
  Zeitleiste des Hives und in den Zahlen.

Zusammenfassungen gelten je Hive und in Ihrer Zeitzone. Offline- und
Speicherhinweise kommen immer sofort, weil Sie dann handeln müssen.

### Warum Bewegung ruhig bleibt

Eine Kamera, die den ganzen Tag Bewegung sieht (eine Straße, ein Baum im
Wind), würde Sie sonst den ganzen Tag benachrichtigen. Camhive hält das auf
vier Arten klein:

- **Wartezeit, die auf Ruhe wartet.** Nach einer Push-Meldung schweigt die
  Kamera, bis sie eine Wartezeit lang ruhig war. Eine Straßenkamera meldet
  sich einmal, wenn der Verkehr beginnt, und wird dann still; eine Kamera,
  die zehn Minuten ruhig war, meldet sich sofort wieder. Alle Bewegungen
  stehen auf der Zeitleiste.
- **Zu Hause.** Solange der Besitzer den Hive zu Hause (im Netz des Hives,
  Hives ab v0.1.10) in der Camhive-App geöffnet hat, markiert der Hive seine
  Bewegung als *still*: Sie wird gezählt und steht auf der Zeitleiste, wird
  aber nicht gemeldet. Nur die App des Besitzers meldet, und nur, solange sie
  den Hive zeigt; eine App im Hintergrund oder eine geschlossene meldet
  innerhalb einer Minute nichts mehr, und fünf Minuten später wird Bewegung
  wieder gemeldet. Mitglieder zählen noch nicht.
  Der Hive sagt der Cloud nie, wer zu Hause ist. Er sagt ihr bei jeder
  Bewegung, ob sie still ist: Der Ereignisstrom der Cloud hält das bis zu
  24 Stunden, der Benachrichtiger entscheidet darüber innerhalb von Minuten
  und behält es nicht (eine stille Bewegung, die auf eine Zusammenfassung
  wartet, ist nur eine Zahl je Hive).
- **Ruhezeiten** (unten) halten Bewegung bis zu ihrem Ende zurück.
- **Tageslimit.** Nach 50 Bewegungsmeldungen an einem Tag in Ihrem Konto
  (alle Hives zusammen) kommt Bewegung bis Mitternacht als eine
  Zusammenfassung pro Stunde je Hive. Eine Benachrichtigung sagt Ihnen das
  einmal.

Auf dem Hive selbst hat jede Kamera eine **Bewegungsempfindlichkeit** und
beruhigt sich an einem windigen Tag von selbst; siehe
[Kameras](cameras.md#bewegungsempfindlichkeit).

- **Kanäle.** Jeder Tarif enthält E-Mail und Push. Webhooks für Pro sind
  geplant (#111).
- **Push-Geräte.** Wählen Sie unter *Push auf Ihren Geräten* in jedem Browser
  und auf jedem Handy, das Benachrichtigungen bekommen soll,
  *Push-Benachrichtigungen auf diesem Gerät erhalten* und erlauben Sie
  Benachrichtigungen, wenn der Browser fragt. Free enthält ein Gerät, Plus
  fünf, Pro zwanzig. Die Liste zeigt Ihre Geräte; entfernen Sie eines, das Sie
  nicht mehr nutzen. Nach einem Wechsel in einen kleineren Tarif bleiben die
  Geräte über dem neuen Limit erhalten, bekommen aber nichts (markiert mit
  *über dem Limit Ihres Tarifs*), bis Sie upgraden oder ein anderes entfernen.
  *Testbenachrichtigung senden* (einmal pro Minute) prüft, ob Push Ihre Geräte
  erreicht. Ein Tippen auf eine Benachrichtigung öffnet die Kamera (bei
  Bewegung mit den Aufnahmen dieses Tages) oder den Hive.
- **Abmelden** entfernt diesen Browser aus Ihren Geräten: An einem gemeinsam
  genutzten Computer bekommt die nächste Person nicht Ihre Benachrichtigungen.
  Melden Sie sich an und schalten Sie Push wieder ein, um sie wieder zu
  erhalten.
- **iPhone und iPad** stellen Push nur der installierten App zu: Tippen Sie in
  Safari auf *Teilen* → *Zum Home-Bildschirm*, öffnen Sie Camhive vom
  Home-Bildschirm und schalten Sie Push dort ein (iOS 16.4 oder neuer).
- **Blockierte Benachrichtigungen.** Haben Sie einmal abgelehnt, fragt der
  Browser nicht noch einmal: Erlauben Sie Benachrichtigungen für die
  Camhive-Seite in den Website-Einstellungen des Browsers und schalten Sie
  Push dann wieder ein.
- **Ruhezeiten** halten Bewegungsbenachrichtigungen in einem täglichen
  Zeitraum zurück, etwa nachts: Bewegung im Zeitraum wird nicht gemeldet, und
  an seinem Ende kommt eine Zusammenfassung. Eine Zusammenfassung, die in den
  Zeitraum fiele, wartet ebenfalls auf sein Ende. Offline- und
  Speicherhinweise kommen weiterhin sofort, weil Sie dann handeln müssen.
- **Je Kamera.** Öffnen Sie die Kamera auf der Seite ihres Hives und wählen
  Sie *Benachrichtigungen*, um Bewegungsbenachrichtigungen für diese Kamera
  auszuschalten oder ihre Wartezeit einzustellen: wie lange sie ruhig sein
  muss, bevor sie Sie wieder benachrichtigen darf (1 bis 60 Minuten).
- **Bewegung, während der Hive offline ist,** wird nicht nachträglich
  gemeldet; sie steht auf der Zeitleiste des Hives. Eine Benachrichtigung über
  Bewegung kommt innerhalb von Sekunden oder gar nicht.
- **Was die Cloud speichert.** Zu jeder Benachrichtigung: welcher Hive und
  welche Kamera, der Name der Kamera, die Art des Ereignisses, wann, wie viel
  Platz frei war (nur bei wenig Speicher), wie die Bewegung erkannt wurde
  (Kamera oder Bild) und ob sie zugestellt wurde; zu jeder Bewegung, von der
  sie erfährt: welcher Hive und welche Kamera, wann und auf welche
  Zusammenfassung sie wartet, damit sie zählen und die Wartezeit einhalten
  kann; von stillen Bewegungen, die auf eine Zusammenfassung warten, nur ihre
  Zahl je Hive. Nie ein Bild oder Video, nie, wer zu Hause ist. Diese
  Einträge werden nach 30 Tagen gelöscht.
- **Uhrzeiten** in Benachrichtigungen stehen in Ihrer Zeitzone. Die App
  speichert die Zeitzone Ihres Browsers jedes Mal, wenn Sie diese
  Einstellungen ändern; bis dahin bekommen deutsche Konten deutsche Zeit und
  alle anderen UTC.
- Nur der Besitzer eines Hives bekommt dessen Benachrichtigungen; Mitglieder
  mit freigegebenen Kameras noch nicht.
- Ein Hive, der nach dem Ende eines Tarifs ruht, sendet keine
  Benachrichtigungen, bis er wieder aktiv ist.

## Aktivität

*Aktivität* listet, was in Ihrem Konto und auf jedem Ihrer Hives passiert ist:
Anmeldungen, angemeldete Hives, Freigaben, Exporte, Tarifänderungen, jeweils
mit Person und Zeitpunkt. *Meine Daten herunterladen (JSON)* lädt alle Daten, die
wir über Sie haben, als JSON herunter.

### Die eigene Aktivität eines Hives

Der Hive führt auf seiner Festplatte ein eigenes Protokoll darüber, was mit ihm
passiert ist: Kameras hinzugefügt, geändert, verschoben oder entfernt, Exporte,
gelöschte Aufnahmen, Updates (verfügbar, installiert, zurückgenommen),
Archivverschlüsselung (eingeschaltet, entsperrt, gesperrt, Passphrase
geändert, falsche Passphrasen), das Herunterladen der Schlüsseldatei, die
lokale Notfall-Anmeldung, Live-Ansichten und was die Cloud ihm geschickt hat
(Lizenz, Zertifikat, Freigaben). Öffnen Sie den Hive → *Aktivität*, um es zu
lesen, das Neueste zuerst, mit der handelnden Person (Sie, die Camhive-Cloud, der
Updater oder der Hive selbst; Konten anderer Personen erscheinen mit ihrer
Konto-ID), dem Zeitpunkt und der Kamera. *Mehr laden* reicht weiter zurück, bis
zu den letzten 1000 Einträgen. Nur der Besitzer des Hives sieht diesen Reiter;
der Hive muss erreichbar sein, zu Hause oder über das Relay.

Über der Liste schränkt ein Filter ein, was angezeigt wird: Die Schaltflächen
*Kameras*, *Aufnahmen und Exporte*, *Updates*, *Anmeldungen und Zugriff* und
*Verschlüsselung* zeigen nur diese Arten (wählen Sie mehrere, um sie zu
kombinieren, keine, um alles zu sehen), und *Aktivität durchsuchen* sucht im
Text der Art, der Kamera und des Handelnden. *Anmeldungen und Zugriff*
enthält auch das Beenden einer Sitzung durch die Cloud und vom Hive gesendete
Diagnosen. Live-Ansichten und was die Cloud dem Hive schickt (Lizenz,
Zertifikat, Relay-Einstellungen, Freigaben, Fernverbindungen) werden den
ganzen Tag geschrieben und sind deshalb ausgeblendet, bis Sie
*Cloud-Sitzungen anzeigen* ankreuzen; die Zahl in Klammern sagt, wie viele der
geladenen Einträge, die auch zum Suchtext passen, ausgeblendet sind. Der
Filter wirkt auf die bereits geladenen Einträge: *Mehr laden* behält ihn bei
und sucht weiter zurück. Er ist Teil der Seitenadresse, sodass ein Neuladen
oder ein geteilter Link mit demselben Filter öffnet.

## Hive entfernen

Hive → *Verbindung* → *Diesen Hive entfernen* → **Hive entfernen**. Der Hive
wird in der Cloud widerrufen, ist von unterwegs nicht mehr erreichbar und gibt
seinen Platz in Ihrem Tarif frei. Die Aufnahmen bleiben auf dem Gerät. Um den
Computer wieder zu nutzen, löschen Sie `cloud.json` aus seinem
Datenverzeichnis und führen Sie `hive claim` aus.
