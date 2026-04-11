#!/bin/bash
# wacli DB sync — pulls wacli.db from Mac Mini to MacBook
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-db-sync.sh
# Installed to: ~/bin/wacli-db-sync.sh via `make install-sync`
#
# Runs on MacBook as LaunchAgent (every 30s).
# Creates a consistent SQLite backup on Mac Mini, then rsyncs to local.
# whatslack file watcher picks up DB changes automatically.

set -euo pipefail

REMOTE="macmini"
REMOTE_DB="$HOME/.wacli/wacli.db"
REMOTE_EXPORT="$HOME/.wacli/wacli-export.db"
LOCAL_DB="$HOME/.wacli/wacli.db"
LOG_PREFIX="[wacli-db-sync]"

log() { echo "$LOG_PREFIX $(date '+%H:%M:%S') $1"; }

# Check Mac Mini is reachable (fast timeout)
if ! ssh -o ConnectTimeout=3 -o BatchMode=yes "$REMOTE" true 2>/dev/null; then
    log "WARN: Mac Mini unreachable, skipping sync"
    exit 0
fi

# Create consistent backup on Mac Mini
if ! ssh "$REMOTE" "sqlite3 '$REMOTE_DB' '.backup $REMOTE_EXPORT'" 2>/dev/null; then
    log "ERROR: sqlite3 backup failed on Mac Mini"
    exit 1
fi

# Ensure local directory exists
mkdir -p "$(dirname "$LOCAL_DB")"

# Rsync the backup to local (delta transfer, compressed)
if rsync -az "$REMOTE:$REMOTE_EXPORT" "$LOCAL_DB" 2>/dev/null; then
    log "OK: synced $(stat -f%z "$LOCAL_DB" 2>/dev/null || echo '?') bytes"
else
    log "ERROR: rsync failed"
    exit 1
fi
