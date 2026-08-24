# wacli — Dev Instructions

## Stack
- **Language:** Go 1.22+
- **WhatsApp:** whatsmeow (multidevice Web protocol)
- **Database:** SQLite via go-sqlite3 (CGO)
- **IPC:** Unix socket (`~/.wacli/wacli.sock`), JSON-over-newline protocol

## Building

### KRITIEK: Build tags
wacli gebruikt FTS5 voor full-text search. Build ALTIJD met:
```bash
CGO_ENABLED=1 go build -tags sqlite_fts5 -o ~/bin/wacli ./cmd/wacli
```
**Zonder `-tags sqlite_fts5`** crasht de daemon met `no such module: fts5` bij elke FTS query.
Dit is de #1 meest gemaakte fout — go-sqlite3 schakelt FTS5 alleen in met deze build tag.

### Verificatie na build
```bash
wacli doctor --json | jq .fts_enabled
# Moet "true" zijn
```

## Daemon Management

### LaunchAgent
wacli draait als macOS LaunchAgent: `~/Library/LaunchAgents/com.productfunction.wacli.plist`
De health monitor draait als `com.productfunction.wacli-health` (elke 30 min).

### Verouderde client (405) — KRITIEK bij elke rebuild
whatsmeow pint een WhatsApp Web clientversie. WhatsApp weigert oudere clients met
`Client outdated (405) connect failure`. Dit gebeurde op 2026-06-17 en kostte
**68 dagen aan gemiste berichten** (zie `Decisions/2026-08-24-wacli-405-silent-sync-failure.md`
in TheBorg).

**Werk whatsmeow bij bij elke aanraking van dit project:**
```bash
go get go.mau.fi/whatsmeow@latest && make -C infra build reinstall
```
Reken op kleine API-patches — signatures breken tussen versies.

### Health & fatale states
- `internal/app/fatal.go` bepaalt welke connectiestates fataal zijn. Fatale states
  aborten de sync-loop met `[FATAL]` regels en een non-zero exit. **Nooit een
  connectie-event stil laten wegvallen** — dat was precies de bug.
- `infra/scripts/wacli-health.sh` draait elke 30 min. De hoofdcheck is de
  *leeftijd van het nieuwste bericht*, niet of het proces draait. "Draait het
  proces?" stond 68 dagen op groen terwijl er niets binnenkwam.
- Snelle diagnose: `make -C infra health`

### Herstarten na rebuild
```bash
launchctl bootout gui/$(id -u)/com.productfunction.wacli
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.productfunction.wacli.plist
```
**Let op:** `launchctl kickstart` werkt niet betrouwbaar voor Go binaries. Gebruik altijd bootout+bootstrap.

### Logs bekijken
```bash
tail -f ~/Library/Logs/Whatslack/wacli-sync.log   # sync daemon (ISO-8601 stamps, roteert bij 20 MB)
tail -f ~/Library/Logs/Claude/wacli-health.log       # health monitor
```

### Controleer of daemon draait
```bash
ls -la ~/.wacli/wacli.sock  # Socket moet bestaan
lsof ~/.wacli/wacli.sock    # Process moet actief zijn
```

## Architecture

### IPC Protocol
- **Socket:** `~/.wacli/wacli.sock`
- **Format:** JSON request + newline → JSON response + newline
- **Commands:** `send_text`, `send_file`, `mark_read`, `backfill`, `ping`
- **Handler interface** in `internal/ipc/ipc.go`: elke command = 1 method op Handler interface
- **Implementatie** in `cmd/wacli/sync.go`: `syncHandler` struct implementeert Handler

### Backfill Pattern
**BELANGRIJK:** Er zijn twee backfill-patronen in de codebase:
1. `BackfillHistory()` in `internal/app/backfill.go` — start eigen `Sync()` sessie. **NIET gebruiken vanuit daemon/IPC** want conflicteert met de actieve sync sessie.
2. `backfillDetectedGaps()` in `cmd/wacli/sync.go` — doet directe `RequestHistorySyncOnDemand()` op bestaande WA connectie. **DIT is het juiste pattern** voor IPC handlers en alles dat tijdens sync draait.

### Database
- **Pad:** `~/.wacli/wacli.db`
- **Ownership:** wacli is eigenaar, whatslack leest read-only
- **File watcher:** whatslack detecteert changes via notify crate op `~/.wacli/`

### MacBook sync (geen wacli daemon op MacBook)
wacli draait alleen op Mac Mini. MacBook krijgt data via SSH sync:

- **Script:** `infra/scripts/wacli-db-sync.sh` — draait als LaunchAgent op MacBook (elke 30s)
- **DB sync:** `sqlite3 wacli.db ".backup wacli-export.db"` op Mac Mini → rsync naar MacBook `~/.wacli/wacli.db`
- **Media sync:** rsync `~/.wacli/media/` (exclusief `status_broadcast/`) → MacBook `~/.wacli/media/`
- **Installeren/updaten op MacBook:** `scp macmini:~/Projects/wacli/infra/scripts/wacli-db-sync.sh ~/bin/wacli-db-sync.sh`
- **IPC commando's** (send, react, download) werken via de tunnel vanuit MacBook naar Mac Mini's socket
- **Let op:** als images niet renderen op MacBook, check eerst of `~/.wacli/media/` gesynchroniseerd is — `local_path` in DB wijst naar Mac Mini paden. DB sync ≠ media sync. (date: 2026-05-17)

## Conventions
- Go code style: standaard gofmt
- Error handling: wrap met `fmt.Errorf("context: %w", err)`
- Logging: `fmt.Fprintf(os.Stderr, "[tag] message\n", ...)`
- IPC handlers: fire-and-forget waar mogelijk, sync responses komen via bestaande event handlers
