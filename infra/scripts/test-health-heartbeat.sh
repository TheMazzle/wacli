#!/bin/bash
# Bewijst dat de health-check een periodiek levensteken achterlaat, ook als
# alles in orde is. Zonder dat is stilte niet te onderscheiden van een monitor
# die zelf gestopt is.
#
# De verdict van wacli-health.sh (OK/WARN/CRITICAL) hangt af van live
# systeemstaat: is het device gekoppeld, draait de daemon nog, hoe oud is het
# nieuwste bericht. Deze test neemt daarom NIET aan dat de verdict OK is.
# (Een neppe WACLI_STORE_DIR om dat af te dwingen is geprobeerd en verworpen:
# een minimale nep-database breekt `wacli doctor` op een ontbrekende kolom,
# en een echte database kopiëren zonder de sessie erbij geeft "niet
# gekoppeld" — om dat op te lossen moeten ook de echte device-credentials
# (session.db) mee de temp-dir in, wat we niet willen voor een test.)
#
# In plaats daarvan draait de test tegen de echte systeemstaat (alleen
# LOG_DIR wordt geïsoleerd naar een temp-map) en kijkt na afloop wat de
# verdict was:
#   - altijd waar, ongeacht verdict: (a) er komt minstens één regel, en
#     (c) HEARTBEAT_HOURS=0 forceert er altijd één.
#   - alleen gecontroleerd als de verdict OK bleek: (b) twee identieke runs
#     kort na elkaar schrijven niet allebei een regel — bij WARN/CRITICAL
#     logt het script bewust élke bevinding op élke run (forensisch, geen
#     bug), dus die spam-check is daar niet zinvol.
set -uo pipefail

HEALTH="$HOME/Projects/wacli/infra/scripts/wacli-health.sh"
WORK=$(mktemp -d /tmp/health-test.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

fail() { echo "!! $1" >&2; exit 1; }

run() {
    LOG_DIR="$WORK" HEARTBEAT_HOURS="$1" bash "$HEALTH" >/dev/null 2>&1
}

# Twee runs vlak na elkaar. Bij een ongewijzigde staat mag de tweede geen
# dubbele regel schrijven, maar er moet er minstens één staan.
run 6
run 6

LINES=$(grep -c "" "$WORK/wacli-health.log" 2>/dev/null || echo 0)
(( LINES >= 1 )) || fail "geen enkele regel gelogd; stilte is niet te onderscheiden van een dode monitor"

VERDICT=$(cut -d'|' -f1 "$WORK/.wacli-health.state" 2>/dev/null || echo "?")
if [[ "$VERDICT" == "OK" ]]; then
    (( LINES <= 2 )) || fail "$LINES regels na twee OK-runs; de hartslag spamt"
    echo "==> Hartslag OK ($LINES regel(s) na twee OK-runs)"
else
    echo "==> Verdict op dit systeem is $VERDICT, niet OK (device/daemon-staat buiten onze controle)."
    echo "    Spam-check op het OK-pad overgeslagen; 'schrijft minstens één regel' wel bevestigd."
fi

# Derde run met een hartslag van 0 uur moet altijd loggen, ongeacht de verdict.
BEFORE=$LINES
run 0
AFTER=$(grep -c "" "$WORK/wacli-health.log")
(( AFTER > BEFORE )) || fail "HEARTBEAT_HOURS=0 schreef geen regel"

echo "==> Geforceerde hartslag OK"
