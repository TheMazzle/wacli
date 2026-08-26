#!/bin/bash
# Bewijst dat wacli-health.log roteert bij 5 MB, net als wacli-sync.log
# (20 MB) en wacli-db-sync.log (10 MB) dat al deden. Rotatie zelf gebeurt
# vóór elke statuscheck en hangt dus niet af van de verdict — maar als de
# verdict niet OK zou zijn (bijv. omdat het script tegen live systeemstaat
# draait) zou het script wél een ECHTE notificatie kunnen versturen
# (macOS-melding, Home Assistant-push naar de telefoon). Daarom gebruikt
# deze test dezelfde hermetische fixtures als test-health-heartbeat.sh (zie
# health-test-common.sh) om een deterministische OK-verdict af te dwingen,
# in plaats van alleen LOG_DIR te isoleren.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/health-test-common.sh"

fail() { echo "!! $1" >&2; exit 1; }

WORK=$(mktemp -d /tmp/health-rotation-test.XXXXXX)
cleanup() { cleanup_health_fixtures; rm -rf "$WORK"; }
trap cleanup EXIT
setup_health_fixtures

# Oversized log neerzetten (> 5 MB) met een herkenbare marker aan het begin,
# zodat we kunnen bevestigen dat precies DIE inhoud naar .1 verhuisde.
MARKER="OUDE-REGEL-$(date +%s)"
python3 -c "
import sys
with open('$WORK/wacli-health.log', 'wb') as f:
    f.write(('$MARKER\n').encode())
    f.write(b'x' * (5 * 1024 * 1024 + 100))
"
ORIG_SIZE=$(stat -f%z "$WORK/wacli-health.log")
(( ORIG_SIZE > 5 * 1024 * 1024 )) || fail "test-fixture is niet groot genoeg (${ORIG_SIZE} bytes)"

run_health "$WORK" 0
[[ "$(health_verdict_of "$WORK")" == "OK" ]] \
    || fail "kon geen deterministische OK-verdict afdwingen (kreeg $(health_verdict_of "$WORK")) — test is niet hermetisch, zou een echte notificatie kunnen versturen"

[[ -f "$WORK/wacli-health.log.1" ]] || fail "geen .1-bestand na een oversized log; rotatie draaide niet"
grep -q "$MARKER" "$WORK/wacli-health.log.1" \
    || fail "de oude inhoud staat niet in .1; rotatie verplaatste het verkeerde bestand"

[[ -f "$WORK/wacli-health.log" ]] || fail "geen verse wacli-health.log na rotatie"
NEW_SIZE=$(stat -f%z "$WORK/wacli-health.log")
(( NEW_SIZE < 1024 )) || fail "de nieuwe log is nog ${NEW_SIZE} bytes; verwacht een verse, kleine log"
grep -q "$MARKER" "$WORK/wacli-health.log" \
    && fail "de oude marker staat nog in de nieuwe log; rotatie sneed niet schoon"

# De eigen regel van déze run mag niet verloren zijn gegaan (HEARTBEAT_HOURS=0
# forceert er sowieso één op het OK-pad, dat hierboven al is afgedwongen).
LINES=$(health_lines_of "$WORK")
(( LINES >= 1 )) || fail "de nieuwe log is leeg; de eigen regel van deze run ging verloren"

# Geen enkele notificatie mag onderweg zijn geweest — verdict was OK, dus de
# NOTIFY-stub had sowieso niet aangeroepen moeten worden, maar controleer het
# expliciet in plaats van het aan te nemen.
[[ -f "$NOTIFY_STUB_LOG" ]] \
    && fail "de NOTIFY-stub is aangeroepen tijdens een rotatietest die OK hoorde te zijn: $(cat "$NOTIFY_STUB_LOG")"

echo "==> Rotatie OK: oude inhoud (${ORIG_SIZE} bytes) naar .1, verse log met ${LINES} regel(s), geen notificatie"
