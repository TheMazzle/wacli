#!/bin/bash
# Bewijst dat wacli-db-sync.sh alle kandidaat-hosts probeert en een melding
# geeft als er te lang niet gesynct is — en niet bij elke run opnieuw.
#
# Aanleiding (2026-10-21): de sync lag 9 dagen stil omdat `macmini` alleen
# via Tailscale liep en Tailscale op de MacBook uit stond. 10.378 runs logden
# "unreachable" met exit 0, zonder dat iemand het zag.
#
# Alle hosts zijn `.invalid` (RFC 2606, resolvt nooit), dus het script stopt
# bij de bereikbaarheidscheck en raakt de echte database niet aan. LOG_FILE,
# LOCK en de stempels wijzen naar een tempmap.
set -uo pipefail

DBSYNC="$HOME/Projects/wacli/infra/scripts/wacli-db-sync.sh"
WORK=$(mktemp -d /tmp/dbsync-alert-test.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

fail() { echo "!! $1" >&2; exit 1; }

NOTIFY_STUB="$WORK/fake-notify.sh"
cat > "$NOTIFY_STUB" <<'STUB'
#!/bin/bash
echo "CALLED:$*" >> "$NOTIFY_LOG"
STUB
chmod +x "$NOTIFY_STUB"
export NOTIFY_LOG="$WORK/notify.log"

set_age() { python3 -c "import os,sys,time; t=time.time()-int(sys.argv[2]); os.utime(sys.argv[1],(t,t))" "$1" "$2"; }

run() {
    REMOTES="first-host.invalid second-host.invalid" NOTIFY="$NOTIFY_STUB" \
        LOG_FILE="$WORK/sync.log" LOCK="$WORK/lock" \
        OK_STAMP="$WORK/last-ok" ALERT_STAMP="$WORK/last-alert" \
        bash "$DBSYNC" >>"$WORK/stdout.log" 2>&1
}
calls() { [[ -f "$NOTIFY_LOG" ]] && grep -c CALLED "$NOTIFY_LOG" || echo 0; }

# 1. Laatste sync 3 uur geleden, nog niet gemeld → melding.
touch "$WORK/last-ok"; set_age "$WORK/last-ok" 10800
run
grep -q "first-host.invalid second-host.invalid" "$WORK/stdout.log" \
    || fail "de WARN-regel noemt niet beide kandidaat-hosts"
[[ $(calls) == 1 ]] || fail "na 3 uur zonder sync verwacht 1 melding, kreeg $(calls)"
grep -q "3 uur onbereikbaar" "$NOTIFY_LOG" || fail "melding noemt de duur niet: $(cat "$NOTIFY_LOG")"
[[ -f "$WORK/last-alert" ]] || fail "ALERT_STAMP niet gezet"
echo "==> Melding na 3 uur zonder sync: OK"

# 2. Direct nog een run → geen tweede melding.
run
[[ $(calls) == 1 ]] || fail "melding herhaald binnen ALERT_REPEAT_SECONDS ($(calls) meldingen)"
echo "==> Geen herhaling binnen 6 uur: OK"

# 3. Vorige melding 7 uur geleden → opnieuw melden.
set_age "$WORK/last-alert" 25200
run
[[ $(calls) == 2 ]] || fail "na 7 uur verwacht een herhaalde melding, kreeg $(calls) totaal"
echo "==> Herhaling na 6 uur: OK"

# 4. Recente geslaagde sync (10 min) → geen melding, ook zonder alert-stempel.
rm -f "$WORK/last-alert"; touch "$WORK/last-ok"; set_age "$WORK/last-ok" 600
run
[[ $(calls) == 2 ]] || fail "melding terwijl de laatste sync 10 min oud is"
echo "==> Geen melding bij een recente sync: OK"

grep -qE "db synced|media synced" "$WORK/stdout.log" \
    && fail "het script meldt een geslaagde sync; dat had niet mogen gebeuren"
echo "==> Alle checks OK"
