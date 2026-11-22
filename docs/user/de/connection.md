# Wie die App Ihren Hive erreicht

*[English](../connection.md)*

Die Web-App hat zwei Arten von Datenverkehr zu einem Hive, und sie nehmen
verschiedene Wege.

| Datenverkehr | Weg |
|---|---|
| Livebild | WebRTC vom Hive in Ihren Browser, direkt (Peer-to-Peer). Zu Hause tauscht die App das Verbindungsangebot direkt mit dem Hive aus; unterwegs reicht die Cloud es weiter. Blockieren die Netze einen direkten Weg, leitet das Camhive-TURN-Relay die Medien weiter. |
| Alles andere: Aufnahmen, Exporte, Kameraeinstellungen | HTTPS zum lokalen Namen des Hives, wenn Sie im selben Netz sind (zu seiner lokalen Adresse, wenn der Name nicht aufgelöst werden kann, siehe [unten](#ohne-internet-den-hive-über-seine-adresse-öffnen)), sonst über das Camhive-Relay |

Hive → *Verbindung* zeigt, welcher Weg genutzt wird ("Direkt in Ihrem Netz",
"Direkt in Ihrem Netz, über die Adresse" oder "Über das Camhive-Relay") und
warum, mit einer Schaltfläche *Erneut prüfen*.

## Der lokale Name

Jeder Hive bekommt einen DNS-Namen wie `hv-8dca14eb9a16560f.h.camhive.app`,
der auf seine Adresse **in Ihrem Heimnetz** zeigt. Der Hive hält den Eintrag
aktuell; die App versucht ihn zuerst mit einer kurzen Wartezeit und merkt sich
die Antwort zehn Minuten lang. Auf dem lokalen Weg verlässt nichts Ihr Haus,
und die Qualität wird nie reduziert.

Manche Router lehnen DNS-Antworten ab, die auf private Adressen zeigen
("DNS-Rebind-Schutz"). Das erkennen Sie daran, dass die
Verbindungsseite meldet, der lokale Name habe nicht geantwortet, obwohl lokale
Adressen aufgeführt sind. Erlauben Sie die Domain `h.camhive.app` im Router
(Fritz!Box: Heimnetz → Netzwerk → Netzwerkeinstellungen → DNS-Rebind-Schutz;
OpenWrt: `rebind_domain`), oder nutzen Sie das Relay.

Chrome fragt beim ersten Mal womöglich um Erlaubnis, Geräte in Ihrem lokalen
Netz zu erreichen; erlauben Sie es.

## Zertifikate

Der Hive liefert TLS mit einem Zertifikat für seinen lokalen Namen und seinen
Relay-Namen. Direkt nach der Installation ist es **selbstsigniert**; Browser
vertrauen ihm nicht, deshalb funktioniert der Weg für Aufnahmen noch nicht.
Wenige Minuten nach der ersten Verbindung besorgt die Cloud ein
**Let's Encrypt**-Zertifikat für den Hive und übergibt es; der private
Schlüssel des Hives verlässt nie das Gerät. *Verbindung* zeigt den aktuellen
Aussteller. Bleibt es länger als eine halbe Stunde selbstsigniert, siehe
[Fehlerbehebung](troubleshooting.md).

## Wenn die Cloud nicht erreichbar ist

Hives funktionieren ohne die Cloud. Aufnahme, Aufbewahrung und Ansehen im
eigenen Netz laufen weiter. Die App hält für jeden Hive, der Ihnen gehört,
einen sieben Tage gültigen **Offline-Zugang** und die letzte Liste Ihrer
Hives, sodass Sie in Ihrem eigenen Netz Livebild und Aufnahmen auch dann
öffnen können, wenn die Cloud ausgefallen ist; ein Hinweis zeigt "Lokaler
Modus". Zugriff von Mitgliedern und Änderungen an Freigaben brauchen die
Cloud. Die Anmeldung an Ihrem Camhive-Konto braucht die Cloud.

Der Hive selbst hält eine signierte Kopie der Limits Ihres Tarifs. Sie gilt
sieben Tage und danach weitere dreißig Tage Kulanz und wird bei jeder
Verbindung erneuert, sodass ein Internetausfall nie einen Hive stoppt.

## Notfall-Anmeldung in Ihrem Netz

Bleibt die Cloud länger unerreichbar, als der Offline-Zugang reicht (sieben
Tage), oder ist die Internetverbindung Ihres Hives ausgefallen, während Sie zu
Hause sind, hat die App von der Cloud keinen Zugang mehr. Für diesen Fall
können Sie dem Hive eine **lokale Anmeldung** geben: einen Benutzernamen und
ein Passwort, die nur der Hive kennt. Sie ist aus, bis Sie sie einrichten, und
funktioniert nur in Ihrem Heimnetz.

Richten Sie sie ein, solange Camhive erreichbar ist:

1. Öffnen Sie den Hive → *Verbindung* → *Lokale Anmeldung für Notfälle* →
   *Lokale Anmeldung einrichten*.
2. Wählen Sie einen Benutzernamen und ein Passwort mit 10 bis 256 Zeichen
   (nicht der Benutzername) und drücken Sie *Lokale Anmeldung speichern*. Der
   Hive speichert nur einen Hash des Passworts; Camhive sieht es nie.

Ein neues Passwort oder *Lokale Anmeldung ausschalten* beendet jede
angemeldete lokale Sitzung; dasselbe gilt für die gleiche Änderung mit
`hive local-login` auf dem Computer des Hives. Jede Änderung, Anmeldung und
jeder Fehlversuch steht im Aktionsprotokoll des Hives (ein Fehlversuch hält
fest, ob der Benutzername stimmte, nie, was eingetippt wurde).

Eine lokale Anmeldung erreicht Kameras und Aufnahmen, aber nicht die
Update-Einstellungen des Hives: Sie zu ändern oder *Jetzt aktualisieren* zu
drücken, braucht eine Anmeldung über Camhive (Updates brauchen ohnehin das
Internet). Mit verschlüsselten Aufnahmen (siehe
[Live und Archiv](live-and-archive.md#verschlüsselte-aufnahmen)) kann eine
lokale Anmeldung sie nach einem Neustart mit der Passphrase entsperren und
wieder sperren; Einschalten der Verschlüsselung, Ändern der Passphrase und
Herunterladen der Schlüsseldatei brauchen eine Anmeldung über Camhive.

So nutzen Sie sie, wenn die Cloud den Hive nicht erreicht:

1. Seien Sie im selben Netz wie der Hive, in einem Browser, in dem Sie die App
   schon genutzt haben (er kennt Ihre Hives, den lokalen Namen des Hives und
   seine lokalen Adressen).
2. Öffnen Sie den Hive. Erreicht Camhive ihn nicht, antwortet der Hive aber in
   Ihrem Netz, zeigt die Seite *Im eigenen Netz anmelden*. Zeigt sie
   stattdessen *Hive über seine Adresse öffnen*, folgen Sie zuerst dem
   nächsten Abschnitt.
3. Geben Sie den lokalen Benutzernamen und das Passwort ein und drücken Sie
   *Lokal anmelden*.

Livebild, Aufnahmen, Exporte und Kameraeinstellungen funktionieren dann wie
für den Besitzer. Ein Hinweis zeigt, dass Sie lokal angemeldet sind: Freigaben,
Mitglieder und Ihr Tarif brauchen Camhive und sind nicht verfügbar. Die lokale
Anmeldung hält eine Stunde oder bis Sie den Tab schließen; danach melden Sie
sich erneut an. *Lokal abmelden* beendet sie sofort.

Grenzen, die Sie schützen:

- Die lokale Anmeldung wird über das Camhive-Relay, von Adressen außerhalb
  Ihres lokalen Netzes und vom Computer des Hives selbst abgelehnt, sodass
  niemand im Internet sie ausprobieren kann.
- Fünf falsche Passwörter in 15 Minuten von einem Gerät sperren dieses Gerät
  für 15 Minuten aus; fünfzig von überall sperren alle für 15 Minuten aus. Mit
  einem Hive neuer als 0.1.7 sagt die Seite, wie lange Sie warten müssen; mit
  0.1.7 oder älter kann sie nur sagen, dass die Sperre bis zu 15 Minuten
  dauert.
- Wer in Ihrem Netz das Passwort erfährt, kann alles ansehen und ändern, wie
  Sie. Nutzen Sie ein Passwort, das Sie nirgends sonst verwenden.
- Machen Sie Port 16443 des Hives nicht aus dem Internet erreichbar: keine
  Portweiterleitung im Router und kein Reverse-Proxy oder Tunnel davor. Ein
  Router oder Proxy, der Datenverkehr von außen von seiner eigenen lokalen
  Adresse aus weiterreicht, lässt diesen Verkehr so aussehen, als käme er aus
  Ihrem Netz.

**Nie eingerichtet und die Cloud ist weg?** Führen Sie auf dem Computer des
Hives als Administrator `sudo hive local-login set --username <name>` aus
(Windows: `hive.exe local-login set --username <name>` in einer
Administrator-Eingabeaufforderung) und tippen Sie das Passwort zweimal ein.
`hive local-login status` zeigt, ob sie an ist, `hive local-login disable`
schaltet sie aus.

## Ohne Internet: den Hive über seine Adresse öffnen

Der lokale Name des Hives (zum Beispiel `hv-8dca14eb9a16560f.h.camhive.app`)
wird im DNS des Internets nachgeschlagen. Ist Ihr Internet zu Hause
ausgefallen, kann ihn niemand nachschlagen. Die App merkt sich auch die lokalen
Adressen des Hives (zum Beispiel `192.168.1.23`) vom letzten Mal, als Camhive
den Hive erreicht hat, und versucht sie, wenn der Name nicht antwortet. Browser
vertrauen dem Zertifikat des Hives nur für seinen Namen, nicht für eine
Adresse; deshalb zeigt die Hive-Seite beim ersten Mal, wenn Sie das brauchen
(und wieder, nachdem das Zertifikat erneuert wurde, etwa alle zwei Monate, oder
die Adresse sich geändert hat), *Hive über seine Adresse öffnen*:

1. Öffnen Sie den Link (`https://192.168.1.23:16443/`). Der Browser warnt,
   dass die Verbindung nicht privat ist.
2. Prüfen Sie die Warnung, bevor Sie weitergehen: Sie muss genau die unten
   für Ihren Browser beschriebene sein, mit dem lokalen Namen Ihres Hives, wie
   ihn die Karte zeigt (hier `hv-8dca14eb9a16560f.h.camhive.app`).
3. Jede andere Warnung – "nicht vertrauenswürdig", "selbstsigniert", ein
   unbekannter Aussteller oder ein anderer Name – heißt: aufhören, schließen
   Sie diesen Tab. Unter dieser Adresse antwortet dann etwas anderes als Ihr
   Hive, und es würde Ihr lokales Passwort sehen.
4. Die Seite des Hives meldet, dass Ihr Browser ihn jetzt erreicht. Gehen Sie
   zurück zur App: Sie versucht es von selbst erneut (oder drücken Sie
   *Erneut versuchen*) und zeigt *Im eigenen Netz anmelden*. Livebild und
   Aufnahmen funktionieren dann wie oben.

**Chrome und Edge.** Die Seite zeigt den Code
`NET::ERR_CERT_COMMON_NAME_INVALID`. Unter *Erweitert* steht, dass der Server
nicht beweisen konnte, 192.168.1.23 zu sein, und dass sein
Sicherheitszertifikat von hv-8dca14eb9a16560f.h.camhive.app stammt. Erst dann
wählen Sie "Weiter zu 192.168.1.23 (unsicher)". Eine Fälschung zeigt
`NET::ERR_CERT_AUTHORITY_INVALID` und den Hinweis, dass das
Sicherheitszertifikat vom Betriebssystem Ihres Computers als nicht
vertrauenswürdig eingestuft wird, oder einen anderen Namen nach "stammt von".

**Firefox.** Die Seite zeigt den Code `SSL_ERROR_BAD_CERT_DOMAIN`. Unter
*Erweitert* steht, dass das Zertifikat nicht für 192.168.1.23 gilt, sondern nur
für hv-8dca14eb9a16560f.h.camhive.app. Erst dann wählen Sie "Risiko
akzeptieren und fortfahren". Eine Fälschung zeigt `SEC_ERROR_UNKNOWN_ISSUER`
oder `MOZILLA_PKIX_ERROR_SELF_SIGNED_CERT` oder nennt eine andere Website.

**Safari (Mac, iPhone, iPad).** Die Seite meldet "Diese Verbindung ist nicht
privat". Wählen Sie *Details einblenden* und sehen Sie sich das Zertifikat an:
Es muss für hv-8dca14eb9a16560f.h.camhive.app von Let's Encrypt (ISRG Root X1
oder X2) ausgestellt sein und darf nicht melden, dass es nicht
vertrauenswürdig ist. Erst dann wählen Sie "diese Website besuchen". Safari
wurde mit Camhive noch nicht getestet; erreicht die App den Hive danach immer
noch nicht, nutzen Sie Chrome, Edge oder Firefox.

Warum die Prüfung Sie schützt: Das Zertifikat des Hives wird öffentlich für
seinen Namen ausgestellt, und niemand sonst kann eines für diesen Namen
bekommen; ein Gerät, das sich als Ihr Hive ausgibt, kann also nicht dieselbe
Warnung erzeugen.

Hat der Hive eine neue Adresse bekommen, während das Internet weg war, finden
Sie sie in der Geräteliste Ihres Routers (Fritz!Box: *Heimnetz* → *Netzwerk*)
oder mit `hive status` auf dem Computer des Hives (die Zeilen
`lan: https://…`); tippen Sie sie in *Adresse des Hives* ein und drücken Sie
*Erneut versuchen*. Nur lokale IP-Adressen werden angenommen. Eine feste
Adresse für den Hive in Ihrem Router vermeidet das.

Was das nicht abdeckt:

- Ein Browser oder Handy, das die App nie geöffnet hat. Die App selbst kommt
  von Camhive; öffnen Sie sie einmal, angemeldet, auf jedem Gerät, das Sie im
  Notfall nutzen wollen, und richten Sie die lokale Anmeldung ein, solange
  Camhive erreichbar ist.
- Ein Hive, dessen Zertifikat noch nicht von Let's Encrypt stammt (direkt
  nach der Installation selbstsigniert, siehe [Zertifikate](#zertifikate)):
  Die App bietet die Adresse nur an, wenn das Zertifikat öffentlich
  ausgestellt ist, weil bei jedem anderen Zertifikat eine Fälschung dieselbe
  Warnung zeigen könnte.
- Browser: getestet mit Chrome; Edge nutzt dieselbe Engine; Firefox behandelt
  die Warnung genauso, wurde aber nicht getestet; Safari, iPhone und iPad
  wurden noch nicht getestet.

## Die App installieren

Die Web-App ist eine Progressive Web App. Sie öffnet sich in einem eigenen
Fenster und behält Ihre Anmeldung. Video wird nie auf dem Gerät
zwischengespeichert.

- **Chrome und Edge** (Desktop und Android): Wenn der Browser die App
  installieren kann, zeigt **Konto** *App installieren* mit der Schaltfläche
  **Camhive installieren**; sie öffnet den Installationsdialog des Browsers.
  Lehnen Sie ab, kommt die Schaltfläche wieder, wenn der Browser sie das
  nächste Mal anbietet. Das eigene Menü des Browsers ("Camhive
  installieren…", "Zum Startbildschirm hinzufügen") funktioniert ebenso.
- **iPhone und iPad**: Browser dort haben kein Installationsereignis. Konto
  zeigt, wie es geht: Tippen Sie auf *Teilen* → *Zum Home-Bildschirm*
  (Safari, und seit iOS 16.4 auch Chrome, Edge und Firefox über ihr
  Teilen-Menü) und öffnen Sie Camhive dann vom Home-Bildschirm.
  Benachrichtigungen unter iOS brauchen das.
- **Safari auf dem Mac** zeigt keine Karte; wählen Sie *Ablage* → *Zum Dock
  hinzufügen*.
- In der installierten App wird die Karte nicht gezeigt. Firefox auf dem
  Desktop kann keine Web-Apps installieren; er zeigt nichts.

### Neue Versionen der App

Die App sucht beim Start nach einer neuen Version und jede Stunde, solange sie
im Vordergrund offen ist (und sofort, wenn Sie sie nach einer Stunde oder mehr
wieder nach vorn holen). Eine neue Version lädt im Hintergrund und wartet: Eine
Leiste unter der Kopfzeile meldet *Eine neue Version ist verfügbar.* mit der
Schaltfläche **Aktualisieren**. Die App lädt erst neu, wenn Sie darauf tippen,
sodass ein Update nie ein Livebild oder eine Aufnahme unterbricht, die Sie
gerade ansehen. Lassen Sie sie stehen, startet die neue Version beim nächsten
Öffnen der App, nachdem Sie alle Camhive-Fenster oder -Tabs geschlossen haben.

**Symbol auf dem Home-Bildschirm.** Ändert sich das Camhive-Logo, übernehmen
Android und die Desktop-Browser das neue Symbol nach dem Update von selbst. Ein
iPhone oder iPad behält das Symbol, mit dem es hinzugefügt wurde: Entfernen Sie
Camhive vom Home-Bildschirm (Symbol gedrückt halten → *App entfernen* → *Vom
Home-Bildschirm entfernen*) und fügen Sie es mit *Teilen* → *Zum
Home-Bildschirm* wieder hinzu. Ihre Anmeldung auf diesem Gerät müssen Sie
danach erneut eingeben.

## Mobilfunknetze

Live über eine Mobilfunkverbindung funktioniert meist direkt (Peer-to-Peer).
NAT beim Mobilfunkanbieter kann das TURN-Relay erzwingen; das kostet nichts
extra und ist nicht begrenzt. Aufnahmen über Mobilfunk laufen über das Relay
und sind durch die Upload-Geschwindigkeit bei Ihnen zu Hause begrenzt.
