#!/bin/bash
# Bewijst dat de stale-run guard in wacli-db-sync.sh een notificatie stuurt
# wanneer hij een vastgelopen run opruimt — niet alleen een logregel schrijft
# (die logregel bestond al vóór deze taak en zegt niets over de notify-call).
#
# Gebruikt een tijdelijk LOCK-bestand (niet het echte ~/.wacli/.db-sync.lock)
# en een stub-NOTIFY die alleen zijn argumenten wegschrijft in plaats van een
# echte macOS-notificatie te versturen. Geen echte processen worden geraakt
# behalve een eigen `sleep`, die de test zelf opruimt.
set -uo pipefail

DBSYNC="$HOME/Projects/wacli/infra/scripts/wacli-db-sync.sh"
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
    NOTIFY_LOG="$NOTIFY_LOG" bash "$DBSYNC" >"$WORK/stdout.log" 2>&1

grep -q "vorige run (pid $DUMMY_PID) hangt al" "$WORK/stdout.log" \
    || fail "de guard heeft de stale run niet als zodanig herkend (log ontbreekt)"

kill -0 "$DUMMY_PID" 2>/dev/null \
    && fail "de stale run is niet opgeruimd; het dummy-proces leeft nog"

[[ -f "$NOTIFY_LOG" ]] \
    || fail "de stub-notifier is niet aangeroepen; de stale-run guard ruimt weer stilzwijgend op"

grep -q "opgeruimd" "$NOTIFY_LOG" \
    || fail "de notificatie kwam wel binnen, maar de inhoud klopt niet: $(cat "$NOTIFY_LOG" 2>/dev/null)"

echo "==> Stale-run notificatie OK: $(cat "$NOTIFY_LOG")"
