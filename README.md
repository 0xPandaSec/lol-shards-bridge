# LoL-Shard-Bridge

## The problem

The League of Legends client has **no bulk-disenchant**: every champion,
skin or ward shard has to be disenchanted or unlocked individually, click
by click. With several shards in your inventory that quickly becomes
tedious and annoying.

## The solution: WebUI

The **LoL-Shards-WebUI** takes care of that for you — sorting, multi-select,
"remove duplicates" and with a single click **disenchant all shards** or
**unlock champions**. The WebUI runs at **https://lolshards.pandasec.de**
and shows your shards at a glance.

## Why the bridge?

Your shards do **not** live on a server — they only exist in your local
League client. So the WebUI (in your browser) can talk to your client, the
**bridge** mediates — a small program you run locally on your machine. It
talks to the client through its local LCU API, and your game data never
leaves your computer.

**In short:** WebUI in the browser = user interface, bridge on your machine =
connection to your League client.

## Installation

1. Download **bridge.exe** from the
   [Releases](https://github.com/0xPandaSec/lol-shards-bridge/releases/latest)
   page.

2. **Start League of Legends** and log in (the client must stay open while
   in use).

3. **Start the bridge** — just **double-click bridge.exe**. No flags needed:
   the bridge knows the WebUI (https://lolshards.pandasec.de) automatically
   and **opens it directly in your browser**. Keep the terminal window
   **open**.

4. Done — in the WebUI you can disenchant and unlock shards.

## Options (optional)

| Flag | Description |
|------|-------------|
| `--no-browser` | Do not open the browser on start |
| `--open <url>` | A different address for the browser to open |
| `--port <number>` | Port, default `8700` |
| `--allow-origin <url>` | Additional allowed origins, e.g. your own domain (repeatable) |
| `--key <secret>` | API key; then expected via the `X-UI-Key` header |

## Privacy

The bridge communicates exclusively **locally** with the League client
(`127.0.0.1`, lockfile-verified). No game data is transmitted to third
parties.
