#!/bin/bash
# Bewijst dat de health-check een periodiek levensteken achterlaat, ook als
# alles in orde is. Zonder dat is stilte niet te onderscheiden van een monitor
# die zelf gestopt is.
#
# Hermetische fixtures (WACLI_BIN/SYNC_LOG/WACLI_STORE_DIR/dummy-proces/
# NOTIFY-stub) staan in health-test-common.sh, gedeeld met
# test-health-rotation.sh — zie dat bestand voor waarom elk van de vier
# checks gestubd wordt. Als deze opzet zelf geen OK-verdict oplevert, faalt
# de test meteen met een duidelijke melding ("test is niet hermetisch") in
# plaats van stilletjes een ander pad te testen.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/health-test-common.sh"

fail() { echo "!! $1" >&2; exit 1; }

trap cleanup_health_fixtures EXIT
setup_health_fixtures

# --- Scenario A: geen spam op twee identieke, ongewijzigde OK-runs. ---
# De eerste run zet de baseline (de "(hersteld)" transitie vanaf een lege
# state-file bestond al vóór deze taak en levert altijd één regel op — dat
# alleen bewijst dus niets over de fix). De tweede run, met exact dezelfde
# staat, mag GEEN extra regel schrijven: de hartslag is nog niet verstreken.
WORK_A=$(mktemp -d)
run_health "$WORK_A" 6
[[ "$(health_verdict_of "$WORK_A")" == "OK" ]] \
    || fail "kon geen deterministische OK-verdict afdwingen (kreeg $(health_verdict_of "$WORK_A")) — test is niet hermetisch"
run_health "$WORK_A" 6
LINES_A=$(health_lines_of "$WORK_A")
(( LINES_A == 1 )) || fail "$LINES_A regel(s) na twee identieke OK-runs; verwacht precies 1 (geen spam)"
echo "==> Scenario A OK: geen dubbele regel bij twee identieke OK-runs ($LINES_A regel)"

# --- Scenario B: de kern van de fix. Onder de DEFAULT HEARTBEAT_HOURS (6,
# hier bewust NIET expliciet gezet) krijgt een log wiens laatste regel meer
# dan 6 uur oud is, bij de eerstvolgende ongewijzigde run een vers
# levensteken — zonder dat we echt 6 uur hoeven te wachten: de logregel
# wordt teruggezet in de tijd (touch -t), niet de klok van het systeem. Dit
# is bewust vóór scenario C geplaatst: HEARTBEAT_HOURS=0 in scenario C is
# een triviale grenswaarde (0 uur is per definitie al verstreken) en zou een
# gebroken default-drempel kunnen maskeren als hij later faalt.
#
# Bekende beperking (niet aangepakt, zoals afgesproken): `touch -t` gebruikt
# de lokale wandklok, dus een testrun die toevallig een DST-omslag
# overspant kan de 7-uur-terugzetting met een uur laten afwijken. Op deze
# machine (CEST, geen omslag in zicht) is dat nu geen probleem.
WORK_B=$(mktemp -d)
run_health "$WORK_B" ""
[[ "$(health_verdict_of "$WORK_B")" == "OK" ]] \
    || fail "verdict is $(health_verdict_of "$WORK_B"), niet OK — scenario B vereist een OK-baseline"
BEFORE_B=$(health_lines_of "$WORK_B")
SEVEN_HOURS_AGO=$(date -v-7H '+%Y%m%d%H%M.%S')
touch -t "$SEVEN_HOURS_AGO" "$WORK_B/wacli-health.log" \
    || fail "kon de mtime van de logregel niet terugzetten"
run_health "$WORK_B" ""
AFTER_B=$(health_lines_of "$WORK_B")
(( AFTER_B > BEFORE_B )) \
    || fail "geen levensteken na >6 uur stilte onder de default HEARTBEAT_HOURS — de kern van de fix werkt niet"
echo "==> Scenario B OK: default HEARTBEAT_HOURS (6) geeft een levensteken na >6 uur stilte"

# --- Scenario C: HEARTBEAT_HOURS=0 forceert altijd een regel (triviale
# grenswaarde, aanvullend op scenario B). ---
WORK_C=$(mktemp -d)
run_health "$WORK_C" 6
BEFORE_C=$(health_lines_of "$WORK_C")
run_health "$WORK_C" 0
AFTER_C=$(health_lines_of "$WORK_C")
(( AFTER_C > BEFORE_C )) || fail "HEARTBEAT_HOURS=0 schreef geen regel"
echo "==> Scenario C OK: HEARTBEAT_HOURS=0 forceert een regel"

echo "==> Alle hartslag-scenario's geslaagd"
