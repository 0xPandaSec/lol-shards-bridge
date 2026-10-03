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

3. Die **Bridge starten** – einfach auf **bridge.exe doppelklicken**. Keine
   Flags nötig: Die Bridge kennt die WebUI (https://lolshards.pandasec.de)
   automatisch und **öffnet sie direkt in deinem Browser**. Das
   Terminal-Fenster dabei **offen lassen**.

4. Fertig – in der WebUI kannst du Shards entzaubern und aktivieren.

## Optionen (optional)

| Flag | Erklärung |
|------|-----------|
| `--no-browser` | Öffnet den Browser beim Start nicht |
| `--open <url>` | Andere Adresse, die der Browser öffnen soll |
| `--port <nummer>` | Port, Standard `8700` |
| `--allow-origin <url>` | Weitere erlaubte Ursprünge, z. B. eigene Domain (mehrfach möglich) |
| `--key <geheim>` | API-Key; wird dann via `X-UI-Key`-Header erwartet |

## Datenschutz

Die Bridge kommuniziert ausschließlich **lokal** mit dem League-Client
(`127.0.0.1`, Lockfile-geprüft). Es werden keine Spieldaten an Dritte
übertragen.
