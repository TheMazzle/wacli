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
wacli draait als macOS LaunchAgent: `~/Library/LaunchAgents/com.wacli.sync.plist`

### Herstarten na rebuild
```bash
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.wacli.sync.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.wacli.sync.plist
```
**Let op:** `launchctl kickstart` werkt niet betrouwbaar voor Go binaries. Gebruik altijd bootout+bootstrap.

### Logs bekijken
```bash
tail -f ~/.wacli/sync.log
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

## Conventions
- Go code style: standaard gofmt
- Error handling: wrap met `fmt.Errorf("context: %w", err)`
- Logging: `fmt.Fprintf(os.Stderr, "[tag] message\n", ...)`
- IPC handlers: fire-and-forget waar mogelijk, sync responses komen via bestaande event handlers
