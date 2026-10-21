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

#
# 2026-10-21: de sync lag 9 dagen stil omdat `macmini` alleen naar het
# Tailscale-adres wees en Tailscale op de MacBook uit stond. Elke run logde
# "unreachable" en eindigde met exit 0; niemand zag het. Vandaar de
# LAN-fallback (REMOTES) en de melding als er te lang niets is gesynct.

# Kandidaten in volgorde: Tailscale (werkt ook buitenshuis), dan het LAN.
# REMOTE (enkelvoud) blijft overridable zodat test-db-sync-stale-notify.sh
# een gegarandeerd onbereikbare host kan invullen — de test stopt dan bij de
# bereikbaarheidscheck hieronder, vóór de echte backup/rsync.
REMOTES="${REMOTES:-${REMOTE:-macmini macmini-m4.fritz.box}}"
REMOTE_DB="$HOME/.wacli/wacli.db"
REMOTE_EXPORT="$HOME/.wacli/wacli-export.db"
REMOTE_MEDIA="$HOME/.wacli/media/"
LOCAL_DB="$HOME/.wacli/wacli.db"
LOCAL_MEDIA="$HOME/.wacli/media/"
# Overridable zodat test-db-sync-stale-notify.sh de rotatiecheck (die vóór
# de guard en de bereikbaarheidscheck loopt, dus buiten hun "stop vroeg"
# bescherming valt) tegen een temp-log kan laten draaien in plaats van de
# echte productie-sync-log te stat'en/roteren.
LOG_FILE="${LOG_FILE:-$HOME/Library/Logs/Whatslack/wacli-sync.log}"
# LOCK is overridable zodat test-db-sync-stale-notify.sh de stale-run guard
# kan testen zonder het echte lockbestand aan te raken.
LOCK="${LOCK:-$HOME/.wacli/.db-sync.lock}"
LOG_PREFIX="[wacli-db-sync]"
# Tijdstip van de laatste geslaagde db-sync, en van de laatste melding.
OK_STAMP="${OK_STAMP:-$HOME/.wacli/.db-sync.last-ok}"
ALERT_STAMP="${ALERT_STAMP:-$HOME/.wacli/.db-sync.last-alert}"
STALE_ALERT_SECONDS="${STALE_ALERT_SECONDS:-7200}"
ALERT_REPEAT_SECONDS="${ALERT_REPEAT_SECONDS:-21600}"

# Een run die hier langer over doet is per definitie vastgelopen: de DB is
# ~40 MB over LAN en media zijn incrementeel.
MAX_RUN_SECONDS="${MAX_RUN_SECONDS:-300}"
MAX_LOG_BYTES=$(( 10 * 1024 * 1024 ))

# Detecteert een dode peer in ~30s in plaats van nooit.
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=5
          -o ServerAliveInterval=10 -o ServerAliveCountMax=3)

log() { echo "$LOG_PREFIX $(date '+%Y-%m-%d %H:%M:%S') $1"; }

# Op de MacBook bestaat notify-user.sh (bjorn-supervisor) niet; dan een
# gewone macOS-notificatie. NOTIFY is overridable voor de tests.
notify() {
    local msg="$1"
    NOTIFY="${NOTIFY:-$HOME/Projects/bjorn-supervisor/infra/scripts/notify-user.sh}"
    if [[ -x "$NOTIFY" ]]; then
        FORCE_NOTIFY=1 "$NOTIFY" "$msg" >/dev/null 2>&1
    else
        /usr/bin/osascript -e "display notification \"$msg\" with title \"Whatslack sync\" sound name \"Basso\"" >/dev/null 2>&1
    fi
}

age_of() { [[ -f "$1" ]] && echo $(( $(date +%s) - $(stat -f%m "$1") )) || echo 999999999; }

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
            notify "wacli db-sync hing ${AGE}s en is opgeruimd. Berichten liepen zolang achter."
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

# --- Bereikbaarheid: eerste kandidaat die antwoordt ---
REMOTE=""
for cand in $REMOTES; do
    if ssh "${SSH_OPTS[@]}" "$cand" true 2>/dev/null; then
        REMOTE="$cand"
        break
    fi
done
if [[ -z "$REMOTE" ]]; then
    log "WARN: Mac Mini unreachable via $REMOTES, skipping sync"
    STALE=$(age_of "$OK_STAMP")
    if (( STALE > STALE_ALERT_SECONDS )) && (( $(age_of "$ALERT_STAMP") > ALERT_REPEAT_SECONDS )); then
        HOURS=$(( STALE / 3600 ))
        (( STALE >= 999999999 )) && HOURS="?"
        notify "Mac Mini al ${HOURS} uur onbereikbaar; Whatslack toont oude berichten. Staat Tailscale aan?"
        touch "$ALERT_STAMP"
        log "ALERT: gemeld na ${HOURS} uur zonder sync"
    fi
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
    log "OK: db synced $(stat -f%z "$LOCAL_DB" 2>/dev/null || echo '?') bytes via $REMOTE"
    touch "$OK_STAMP"
    rm -f "$ALERT_STAMP"
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
