#!/bin/bash
# Bewijst dat wacli-health.log roteert bij 5 MB, net als wacli-sync.log
# (20 MB) en wacli-db-sync.log (10 MB) dat al deden. Rotatie gebeurt vóór
# elke statuscheck, dus deze test hangt niet af van live systeemstaat —
# alleen LOG_DIR wordt geïsoleerd naar een temp-map.
set -uo pipefail

HEALTH="$HOME/Projects/wacli/infra/scripts/wacli-health.sh"
WORK=$(mktemp -d /tmp/health-rotation-test.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

fail() { echo "!! $1" >&2; exit 1; }

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

LOG_DIR="$WORK" HEARTBEAT_HOURS=0 bash "$HEALTH" >/dev/null 2>&1

[[ -f "$WORK/wacli-health.log.1" ]] || fail "geen .1-bestand na een oversized log; rotatie draaide niet"
grep -q "$MARKER" "$WORK/wacli-health.log.1" \
    || fail "de oude inhoud staat niet in .1; rotatie verplaatste het verkeerde bestand"

[[ -f "$WORK/wacli-health.log" ]] || fail "geen verse wacli-health.log na rotatie"
NEW_SIZE=$(stat -f%z "$WORK/wacli-health.log")
(( NEW_SIZE < 1024 )) || fail "de nieuwe log is nog ${NEW_SIZE} bytes; verwacht een verse, kleine log"
grep -q "$MARKER" "$WORK/wacli-health.log" \
    && fail "de oude marker staat nog in de nieuwe log; rotatie sneed niet schoon"

# De eigen regel van déze run mag niet verloren zijn gegaan (HEARTBEAT_HOURS=0
# forceert er sowieso één als de verdict OK is; als de verdict niet OK is
# schrijft het WARN/CRITICAL-pad sowieso minstens één bevinding). Ofwel: na
# rotatie moet de nieuwe log niet leeg zijn.
LINES=$(grep -c "" "$WORK/wacli-health.log" 2>/dev/null || echo 0)
(( LINES >= 1 )) || fail "de nieuwe log is leeg; de eigen regel van deze run ging verloren"

echo "==> Rotatie OK: oude inhoud (${ORIG_SIZE} bytes) naar .1, verse log met ${LINES} regel(s)"
