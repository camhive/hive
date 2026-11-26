# Erste Schritte

*[English](../README.md)*

Camhive behält Ihre Kameraaufnahmen zu Hause. Ein kleines Programm, der
**Hive**, läuft auf einem Computer bei Ihnen zu Hause (ein PC, ein NAS, ein
Raspberry Pi), zeichnet Ihre IP-Kameras auf seiner eigenen Festplatte auf und
lädt nie Video irgendwohin hoch. Die Camhive-**Cloud** weiß nur, wer Sie sind,
welche Hives Ihnen gehören und mit wem Sie sie teilen. Die **Web-App** unter
`app.camhive.app` spricht direkt mit Ihrem Hive, wenn Sie zu Hause sind, und
über unser Relay, wenn Sie unterwegs sind.

Drei Dinge sind immer kostenlos und hängen nie von einem Abonnement ab:
Aufnahme, Ansehen im eigenen Netz und lokaler Export von Clips.

## In fünfzehn Minuten zum ersten Bild

1. **Konto erstellen.** Öffnen Sie die App, wählen Sie *Konto erstellen* und
   geben Sie Ihre E-Mail-Adresse und ein Passwort mit mindestens 10 Zeichen
   ein. Ein 6-stelliger Code kommt per E-Mail; geben Sie ihn auf der nächsten
   Seite ein und melden Sie sich dann an.
2. **Hive installieren** auf dem Computer, der aufzeichnen soll. Folgen Sie
   [Hive installieren](install-hive.md). Am Ende führen Sie `hive claim` aus;
   der Befehl zeigt einen Code wie `7K3M-Q2XD` an.
3. **Hive anmelden.** Wählen Sie in der App *Hive hinzufügen*, geben Sie den
   Code innerhalb von zehn Minuten ein und geben Sie dem Hive einen Namen. Die
   Seite wartet, bis der Hive online kommt, und zeigt dann den Befehl zum
   Neustart. Führen Sie ihn aus.
4. **Kameras hinzufügen.** Öffnen Sie den Hive und wählen Sie die Kachel
   *Kamera hinzufügen*. Die meisten Kameras werden über ONVIF automatisch
   gefunden; sonst fügen Sie eine RTSP-Adresse ein. Siehe
   [Kameras](cameras.md).
5. **Ansehen.** Öffnen Sie den Hive: Jede Kamera läuft live; klicken Sie auf
   eine, um sie mit ihren Aufnahmen auf einer anklickbaren Zeitleiste zu
   öffnen.

## Wo Sie was finden

| In der App | Was es tut |
|---|---|
| Startseite | Ihre Hives und die für Sie freigegebenen Hives, mit Online-Status |
| Hive → Kameras | Live-Kacheln oder Liste; die geöffnete Kamera mit Zeitleiste, Wiedergabe, Export und PTZ; Kameras hinzufügen, bearbeiten, ausschalten, entfernen (nur Besitzer) |
| Hive → Freigabe | Wer welche Kameras dieses Hives sieht (nur Besitzer) |
| Hive → Einstellungen | Der Hive auf einen Blick (Version, Weg, Festplatte, Aufnahmen, Verschlüsselung, Updates); wie die App den Hive erreicht; Speicher, Zustand der Festplatte, Verschlüsselung, Aufnahmen löschen, Umzug auf eine neue Festplatte; lokale Anmeldung; Updates; Hive entfernen (das meiste nur für den Besitzer) |
| Mitglieder | Personen, die Sie eingeladen haben; offene Einladungen |
| Konto | Passwort, Zwei-Faktor, angemeldete Geräte |
| Tarif | Ihr Tarif und seine Limits, Promo-Codes |
| Aktivität | Alles, was in Ihrem Konto passiert ist, zum Herunterladen |

Die App gibt es auf Englisch und Deutsch; Sie wechseln mit der EN/DE-Auswahl
in der Kopfzeile. Sie lässt sich auf Handy oder Computer wie eine native App
installieren, siehe [Wie die App Ihren Hive erreicht](connection.md).

## Alle Seiten

- [Hive installieren](install-hive.md)
- [Kameras](cameras.md)
- [Live und Archiv](live-and-archive.md)
- [Teilen](sharing.md)
- [Konto und Tarife](account-and-plans.md)
- [Wie die App Ihren Hive erreicht](connection.md)
- [Fehlerbehebung](troubleshooting.md)

## Was Camhive nicht ist

- Camhive ist kein Cloud-Videospeicher. Fällt der aufzeichnende Computer aus,
  sind die Aufnahmen darauf verloren. Wählen Sie eine Festplatte, der Sie
  vertrauen, und lassen Sie den Hive laufen.
- Camhive wandelt nichts um. Was Ihre Kamera sendet (H.264 oder H.265), wird
  so aufgezeichnet und abgespielt. Browser spielen H.264 überall; ob H.265
  läuft, hängt von Browser und Gerät ab.
- Livebild von außerhalb Ihres Zuhauses läuft direkt (Peer-to-Peer) über
  WebRTC und weicht nur dann auf unser TURN-Relay aus, wenn Ihr Netz direkte
  Verbindungen blockiert.
