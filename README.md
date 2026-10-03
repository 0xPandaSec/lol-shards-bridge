# LoL-Shard-Bridge

## Das Problem

Der League-of-Legends-Client bietet **kein Massen-Entzaubern** an: Jeder
Champion-, Skin- oder Augen-Shard muss über unzählige Klicks einzeln
entzaubert oder aktiviert werden. Sind mehrere Shards im Inventar, wird das
schnell zeitraubend und nervig.

## Die Lösung: WebUI

Die **LoL-Shards-WebUI** übernimmt das für dich – sortieren, Mehrfachauswahl,
„Doppelte entfernen“ und mit einem Klick **alle Shards entzaubern** oder
Champions **aktivieren**. Die WebUI läuft über
**https://lolshards.pandasec.de** und zeigt deine Shards übersichtlich an.

## Warum die Bridge?

Deine Shards liegen **nicht** auf einem Server, sondern nur in deinem
lokalen League-Client. Damit die WebUI (im Browser) mit deinem Client reden
kann, vermittelt die **Bridge** – ein kleines Programm, das du lokal auf
deinem Rechner startest. Sie spricht mit dem Client über dessen lokale
LCU-API und deine Spieldaten verlassen deinen Rechner dabei nicht.

**Kurz:** WebUI im Browser = Bedienoberfläche, Bridge auf deinem Rechner =
Verbindung zu deinem League-Client.

## Installationsanleitung

1. **bridge.exe** aus den
   [Releases](https://github.com/0xPandaSec/lol-shards-bridge/releases/latest)
   herunterladen.

2. **League of Legends starten** und dich einloggen (der Client muss während
   der Nutzung offen bleiben).

3. Die **Bridge starten**. Für die entfernte WebUI mit deinem Ursprung:

   ```
   bridge.exe --allow-origin https://lolshards.pandasec.de
   ```

   Das Terminal(-Fenster) dabei **offen lassen**. Die Bridge wartet darauf,
   den League-Client zu finden (schließt den Prozess nicht, während du spielst).

4. Die **WebUI öffnen**: https://lolshards.pandasec.de

   Der Browser verbindet sich automatisch mit der Bridge
   (`127.0.0.1:8700`) und lädt deine Shards. Ist die Bridge noch nicht
   gestartet, zeigt die Seite eine Anleitung.

5. Optional: **Selbst bauen** (mit Go installiert):

   ```
   build.bat
   ```

## Optionen

| Flag | Erklärung |
|------|-----------|
| `--port <nummer>` | Port, Standard `8700` |
| `--key <geheim>` | API-Key; wird dann via `X-UI-Key`-Header erwartet |
| `--allow-origin <url>` | Zusätzliche erlaubte Ursprünge (mehrfach möglich) |
| `--no-browser` | Öffnet den Browser beim Start nicht |

## Datenschutz

Die Bridge kommuniziert ausschließlich **lokal** mit dem League-Client
(`127.0.0.1`, Lockfile-geprüft). Es werden keine Spieldaten an Dritte
übertragen.
