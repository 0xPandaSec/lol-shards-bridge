# LoL-Shard-Bridge

Lokales Hilfsprogramm zur **LoL-Shards-WebUI**. Die Bridge läuft auf deinem
Rechner neben dem League-Client und spricht mit dessen lokaler
LCU-HTTPS-API (Lockfile). Sie macht keine Daten bankseitig, alles passiert
direkt zwischen deinem Rechner und deinem eigenen LoL-Konto.

## Was sie kann

- Champion-, Skin- und Augen-Shards auslesen
- Shards **entzaubern** (in Blau/Orange-Essenz)
- Champion-Shards **aktivieren** (Upgrade; verbraucht Shard + Blaue Essenz)
- Updates per SSE Stream an die WebUI pushen
- CORS-Schutz: antwortet nur dem Ursprung der WebUI – fremde Origins
  werden mit 403 abgewiesen

## Voraussetzungen

- Windows (die `.exe` mitgeliefert; Quellcode unter `main.go`)
- League of Legends: Client **offen und eingeloggt**
- Zugriff auf die WebUI unter `https://lolshards.pandasec.de`
  (das Frontend liegt remote, die Daten bleiben lokal)

## Installation & Start

1. **bridge.exe** aus den [Releases](https://github.com/0xPandaSec/lol-shards-bridge/releases/latest)
   herunterladen (oder selbst mit `build.bat` bauen, benötigt Go).
2. LoL-Client starten und einloggen.
3. Bridge starten – für die entfernte WebUI mit deinem Ursprung:

   ```
   bridge.exe --allow-origin https://lolshards.pandasec.de
   ```

   Das kleine Fenster (Terminal) **offen lassen**, solange die Seite genutzt wird.

## Optionen

| Flag | Erklärung |
|------|-----------|
| `--port <nummer>` | Port, Standard `8700` |
| `--key <geheim>` | API-Key; wird dann via `X-UI-Key`-Header erwartet |
| `--allow-origin <url>` | Zusätzliche erlaubte Ursprünge (mehrfach möglich) |
| `--no-browser` | Öffnet den Browser beim Start nicht |

## WebUI

Die WebUI liegt unter **https://lolshards.pandasec.de** – der Browser erkennt
die lokale Bridge automatisch (`127.0.0.1:8700` / `8765`). Ist sie nicht
gestartet, zeigt die Seite eine Anleitung.

## Bauen

```
build.bat
```

## Datenschutz

Die Bridge kommuniziert ausschließlich lokal mit dem League-Client
(`127.0.0.1`, Lockfile-geprüft) und der von dir besuchten WebUI. Es werden
keine Spieldaten an Dritte übertragen.
