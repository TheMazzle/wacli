#!/bin/bash
# Bewijst dat de stale-run guard in wacli-db-sync.sh een notificatie stuurt
# wanneer hij een vastgelopen run opruimt — niet alleen een logregel schrijft
# (die logregel bestond al vóór deze taak en zegt niets over de notify-call).
#
# Deze test raakte eerder per ongeluk de ECHTE database aan: na de guard
# loopt het script door naar de bereikbaarheidscheck met het hardcoded
# REMOTE="macmini", en als dat toevallig oploste (self-loop, mDNS, een
# toekomstige /etc/hosts-wijziging) volgde een echte `sqlite3 .backup` en
# `rsync` over ~/.wacli/wacli.db — terwijl de daemon die open houdt in WAL-
# modus. Dat is nooit de bedoeling van deze test: het onderwerp is de guard
# en zijn notificatie, niet de sync zelf. Daarom nu twee dingen:
#   1. REMOTE wordt overschreven naar een gegarandeerd onbereikbare host
#      (een `.invalid`-domein, RFC 2606 — resolvt nergens, nooit, ongeacht
#      lokale DNS/mDNS-eigenaardigheden). Het script stopt dan zelf bij zijn
#      eigen bereikbaarheidscheck, vóór de backup/rsync.
#   2. Een expliciete safety-net-assertie ná de run: de mtime, bytegrootte
#      én het berichtenaantal van de ECHTE ~/.wacli/wacli.db, en het
#      regelaantal van de ECHTE ~/Library/Logs/Whatslack/wacli-sync.log,
#      moeten voor en na de test exact gelijk zijn. Dit is geen aanname maar
#      een test op zichzelf — als een toekomstige wijziging dit ooit weer
#      doorbreekt, faalt de test daarop, niet pas bij een handmatige controle.
#
# Gebruikt verder een tijdelijk LOCK-bestand (niet het echte
# ~/.wacli/.db-sync.lock) en een stub-NOTIFY die alleen zijn argumenten
# wegschrijft in plaats van een echte macOS-notificatie te versturen.
set -uo pipefail

DBSYNC="$HOME/Projects/wacli/infra/scripts/wacli-db-sync.sh"
REAL_DB="$HOME/.wacli/wacli.db"
REAL_SYNC_LOG="$HOME/Library/Logs/Whatslack/wacli-sync.log"
WORK=$(mktemp -d /tmp/dbsync-stale-test.XXXXXX)
DUMMY_PID=""
cleanup() {
    rm -rf "$WORK"
    [[ -n "$DUMMY_PID" ]] && kill -9 "$DUMMY_PID" 2>/dev/null
    [[ -n "$DUMMY_PID" ]] && wait "$DUMMY_PID" 2>/dev/null
    return 0
}
trap cleanup EXIT

fail() { echo "!! $1" >&2; exit 1; }

# --- Safety-net: snapshot van de ECHTE bestanden vóór de run. ---
db_snapshot() {
    [[ -f "$REAL_DB" ]] || { echo "geen-bestand"; return; }
    local mtime size count
    mtime=$(stat -f%m "$REAL_DB" 2>/dev/null || echo "?")
    size=$(stat -f%z "$REAL_DB" 2>/dev/null || echo "?")
    count=$(sqlite3 -cmd ".timeout 5000" "file:$REAL_DB?mode=ro" \
        "SELECT COUNT(*) FROM messages;" 2>/dev/null || echo "?")
    echo "mtime=$mtime size=$size count=$count"
}
log_lines() { [[ -f "$1" ]] && grep -c "" "$1" || echo 0; }

DB_BEFORE=$(db_snapshot)
SYNCLOG_LINES_BEFORE=$(log_lines "$REAL_SYNC_LOG")

# Stub-notifier: schrijft alleen weg dat en waarmee hij aangeroepen werd.
NOTIFY_STUB="$WORK/fake-notify.sh"
cat > "$NOTIFY_STUB" <<'STUB'
#!/bin/bash
echo "CALLED:$*" >> "$NOTIFY_LOG"
exit 0
STUB
chmod +x "$NOTIFY_STUB"
NOTIFY_LOG="$WORK/notify.log"
export NOTIFY_LOG

# Fake vastgelopen run: een echt (kortlevend, onschadelijk) sleep-proces
# waarvan de PID en een starttijd ver in het verleden in het LOCK-bestand
# staan, zodat AGE > MAX_RUN_SECONDS.
sleep 60 &
DUMMY_PID=$!
disown "$DUMMY_PID" 2>/dev/null || true
LOCK_FILE="$WORK/db-sync.lock"
printf '%s\n%s\n' "$DUMMY_PID" "$(( $(date +%s) - 400 ))" > "$LOCK_FILE"

MAX_RUN_SECONDS=300 LOCK="$LOCK_FILE" NOTIFY="$NOTIFY_STUB" \
    NOTIFY_LOG="$NOTIFY_LOG" REMOTE="dbsync-test-unreachable.invalid" \
    bash "$DBSYNC" >"$WORK/stdout.log" 2>&1

grep -q "vorige run (pid $DUMMY_PID) hangt al" "$WORK/stdout.log" \
    || fail "de guard heeft de stale run niet als zodanig herkend (log ontbreekt)"

kill -0 "$DUMMY_PID" 2>/dev/null \
    && fail "de stale run is niet opgeruimd; het dummy-proces leeft nog"

[[ -f "$NOTIFY_LOG" ]] \
    || fail "de stub-notifier is niet aangeroepen; de stale-run guard ruimt weer stilzwijgend op"

grep -q "opgeruimd" "$NOTIFY_LOG" \
    || fail "de notificatie kwam wel binnen, maar de inhoud klopt niet: $(cat "$NOTIFY_LOG" 2>/dev/null)"

# Bewijs dat het script echt bij de bereikbaarheidscheck stopte en nooit een
# echte sync probeerde.
grep -q "unreachable" "$WORK/stdout.log" \
    || fail "geen 'unreachable'-melding gezien; onduidelijk of het script bij de bereikbaarheidscheck stopte"
grep -qE "db synced|media synced" "$WORK/stdout.log" \
    && fail "het script meldt een geslaagde db/media-sync — dat had nooit mogen gebeuren in deze test"

echo "==> Stale-run notificatie OK: $(cat "$NOTIFY_LOG")"

# --- Safety-net: de ECHTE bestanden mogen niet zijn aangeraakt. ---
DB_AFTER=$(db_snapshot)
SYNCLOG_LINES_AFTER=$(log_lines "$REAL_SYNC_LOG")

[[ "$DB_BEFORE" == "$DB_AFTER" ]] \
    || fail "de ECHTE ~/.wacli/wacli.db is veranderd tijdens de test! vóór: [$DB_BEFORE] na: [$DB_AFTER]"
[[ "$SYNCLOG_LINES_BEFORE" == "$SYNCLOG_LINES_AFTER" ]] \
    || fail "er is een regel bijgekomen in de ECHTE wacli-sync.log ($SYNCLOG_LINES_BEFORE -> $SYNCLOG_LINES_AFTER)"

echo "==> Safety-net OK: echte db ongewijzigd ($DB_AFTER), sync-log ongewijzigd ($SYNCLOG_LINES_AFTER regels)"
