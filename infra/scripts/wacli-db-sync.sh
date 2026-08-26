#!/bin/bash
# wacli DB + media sync — pulls wacli.db and media from Mac Mini to MacBook
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-db-sync.sh
# Installed to: ~/bin/wacli-db-sync.sh via `make install-sync`
#
# Runs on MacBook as LaunchAgent (every 30s).
# Creates a consistent SQLite backup on Mac Mini, then rsyncs to local.
# Also syncs media files so whatslack can render images locally.
# whatslack file watcher picks up DB changes automatically.
#
# 2026-08-25: de media-rsync hing 6,5 uur op een SSH-verbinding die dood was
# zonder dat TCP het merkte (de MacBook kreeg een nieuw DHCP-adres). launchd
# start geen nieuwe run zolang de oude leeft, dus de sync stond stil zonder
# enige melding. Vandaar de keepalives, de I/O-timeout en de stale-run guard.

set -uo pipefail

REMOTE="macmini"
REMOTE_DB="$HOME/.wacli/wacli.db"
REMOTE_EXPORT="$HOME/.wacli/wacli-export.db"
REMOTE_MEDIA="$HOME/.wacli/media/"
LOCAL_DB="$HOME/.wacli/wacli.db"
LOCAL_MEDIA="$HOME/.wacli/media/"
LOG_FILE="$HOME/Library/Logs/Whatslack/wacli-sync.log"
# LOCK is overridable zodat test-db-sync-stale-notify.sh de stale-run guard
# kan testen zonder het echte lockbestand aan te raken.
LOCK="${LOCK:-$HOME/.wacli/.db-sync.lock}"
LOG_PREFIX="[wacli-db-sync]"

# Een run die hier langer over doet is per definitie vastgelopen: de DB is
# ~40 MB over LAN en media zijn incrementeel.
MAX_RUN_SECONDS="${MAX_RUN_SECONDS:-300}"
MAX_LOG_BYTES=$(( 10 * 1024 * 1024 ))

# Detecteert een dode peer in ~30s in plaats van nooit.
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=5
          -o ServerAliveInterval=10 -o ServerAliveCountMax=3)

log() { echo "$LOG_PREFIX $(date '+%H:%M:%S') $1"; }

# --- Logrotatie: de log liep tot 23 MB zonder ooit te roteren. ---
if [[ -f "$LOG_FILE" ]]; then
    SIZE=$(stat -f%z "$LOG_FILE" 2>/dev/null || echo 0)
    if (( SIZE > MAX_LOG_BYTES )); then
        mv -f "$LOG_FILE" "$LOG_FILE.1" 2>/dev/null || true
    fi
fi

# --- Stale-run guard ---
# Draait er nog een vorige run? Dan óf gewoon wachten (normaal), óf hem
# opruimen als hij vastgelopen is. Zonder dit blokkeert één hangende run
# alle volgende, onbeperkt lang.
if [[ -f "$LOCK" ]]; then
    OLD_PID=$(head -1 "$LOCK" 2>/dev/null)
    if [[ -n "$OLD_PID" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
        OLD_START=$(sed -n '2p' "$LOCK" 2>/dev/null)
        NOW=$(date +%s)
        AGE=$(( NOW - ${OLD_START:-$NOW} ))
        if (( AGE > MAX_RUN_SECONDS )); then
            log "WARN: vorige run (pid $OLD_PID) hangt al ${AGE}s — opruimen"
            # Overridable zodat test-db-sync-stale-notify.sh een stub kan
            # inzetten in plaats van een echte notificatie te versturen.
            NOTIFY="${NOTIFY:-$HOME/Projects/bjorn-supervisor/infra/scripts/notify-user.sh}"
            [[ -x "$NOTIFY" ]] && FORCE_NOTIFY=1 "$NOTIFY" \
                "wacli db-sync hing ${AGE}s en is opgeruimd. Berichten liepen zolang achter." \
                >/dev/null 2>&1
            pkill -P "$OLD_PID" 2>/dev/null
            kill -9 "$OLD_PID" 2>/dev/null
            sleep 1
        else
            # Normale overlap; stilletjes afsluiten.
            exit 0
        fi
    fi
fi
printf '%s\n%s\n' "$$" "$(date +%s)" > "$LOCK"
trap 'rm -f "$LOCK"' EXIT

# --- Bereikbaarheid ---
if ! ssh "${SSH_OPTS[@]}" "$REMOTE" true 2>/dev/null; then
    log "WARN: Mac Mini unreachable, skipping sync"
    exit 0
fi

# --- Consistente snapshot op de Mac Mini ---
if ! ssh "${SSH_OPTS[@]}" "$REMOTE" "sqlite3 '$REMOTE_DB' '.backup $REMOTE_EXPORT'" 2>/dev/null; then
    log "ERROR: sqlite3 backup failed on Mac Mini"
    exit 1
fi

mkdir -p "$(dirname "$LOCAL_DB")" "$LOCAL_MEDIA"

# --timeout: rsync breekt af als er 60s geen data komt. Zonder dit wacht hij
# eeuwig op een verbinding die stilletjes is weggevallen.
RSYNC_OPTS=(-az --timeout=60 -e "ssh ${SSH_OPTS[*]}")

if rsync "${RSYNC_OPTS[@]}" "$REMOTE:$REMOTE_EXPORT" "$LOCAL_DB" 2>/dev/null; then
    log "OK: db synced $(stat -f%z "$LOCAL_DB" 2>/dev/null || echo '?') bytes"
else
    log "ERROR: db rsync failed"
    exit 1
fi

# Media: --update slaat bestaande bestanden over (veilig, media is append-only).
# status_broadcast overslaan: vluchtige WhatsApp-statussen.
if rsync "${RSYNC_OPTS[@]}" --update --exclude='status_broadcast/' \
    "$REMOTE:$REMOTE_MEDIA" "$LOCAL_MEDIA" 2>/dev/null; then
    log "OK: media synced"
else
    log "WARN: media rsync failed (non-fatal)"
fi
