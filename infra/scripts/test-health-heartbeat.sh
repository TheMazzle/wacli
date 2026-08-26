#!/bin/bash
# Bewijst dat de health-check een periodiek levensteken achterlaat, ook als
# alles in orde is. Zonder dat is stilte niet te onderscheiden van een monitor
# die zelf gestopt is.
#
# De verdict van wacli-health.sh (OK/WARN/CRITICAL) hangt af van vier losse
# checks (zie header van het script). Om deze test hermetisch en
# deterministisch te maken — zodat hij niet toevallig slaagt omdat het
# systeem *nu* toevallig gezond is — worden alle vier gestubd, ZONDER ooit
# echte device-credentials (session.db) aan te raken:
#   1. SYNC_LOG   -> lege temp-file, dus nooit een [FATAL]-regel.
#   2. WACLI_BIN  -> stub die altijd `{"authenticated":true,...}` teruggeeft.
#   3. WACLI_STORE_DIR -> temp store met alléén de "messages"-tabel die het
#      script zelf query't (niet het volledige wacli-schema — dat is nu niet
#      nodig, want `wacli doctor` zelf is gestubd, dus de echte migraties
#      worden nooit aangeroepen).
#   4. Het daemon-procesargument ('wacli sync') -> een kortlevend `sleep`
#      met een omgedoopte cmdline (`exec -a`), zodat `pgrep -f 'wacli sync'`
#      iets vindt zonder de echte daemon nodig te hebben.
#
# Als deze opzet zelf geen OK-verdict oplevert, faalt de test meteen met een
# duidelijke melding ("test is niet hermetisch") in plaats van stilletjes een
# ander pad te testen.
set -uo pipefail

HEALTH="$HOME/Projects/wacli/infra/scripts/wacli-health.sh"
FIXTURES=$(mktemp -d /tmp/health-test-fixtures.XXXXXX)
DUMMY_PID=""
cleanup() {
    rm -rf "$FIXTURES"
    [[ -n "$DUMMY_PID" ]] && kill "$DUMMY_PID" 2>/dev/null
    [[ -n "$DUMMY_PID" ]] && wait "$DUMMY_PID" 2>/dev/null
    return 0
}
trap cleanup EXIT

fail() { echo "!! $1" >&2; exit 1; }

command -v sqlite3 >/dev/null || fail "sqlite3 niet gevonden"

# --- Fixture 1+2: stub voor `wacli doctor --json` en een lege sync-log. ---
WACLI_BIN_STUB="$FIXTURES/fake-wacli"
cat > "$WACLI_BIN_STUB" <<'STUB'
#!/bin/bash
echo '{"success":true,"data":{"authenticated":true,"connected":true,"lock_held":false,"fts_enabled":true},"error":null}'
STUB
chmod +x "$WACLI_BIN_STUB"

SYNC_LOG_STUB="$FIXTURES/wacli-sync.log"
: > "$SYNC_LOG_STUB"

# --- Fixture 3: verse database met alleen wat het script zelf leest. ---
STORE="$FIXTURES/store"
mkdir -p "$STORE"
sqlite3 "$STORE/wacli.db" \
    "CREATE TABLE messages (ts INTEGER); INSERT INTO messages (ts) VALUES ($(date +%s));" \
    || fail "kon test-database niet aanmaken"

# --- Fixture 4: dummy proces met 'wacli sync' in de cmdline. ---
bash -c 'exec -a "wacli sync (test-stub)" sleep 120' &
DUMMY_PID=$!
sleep 0.3   # geef het even tijd om in de procestabel te verschijnen

run() {
    # $1 = werkmap voor deze scenario-run, $2 = HEARTBEAT_HOURS ("" = default van het script)
    local work="$1" hh="${2:-}"
    if [[ -n "$hh" ]]; then
        LOG_DIR="$work" WACLI_STORE_DIR="$STORE" WACLI_BIN="$WACLI_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" HEARTBEAT_HOURS="$hh" bash "$HEALTH" >/dev/null 2>&1
    else
        LOG_DIR="$work" WACLI_STORE_DIR="$STORE" WACLI_BIN="$WACLI_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" bash "$HEALTH" >/dev/null 2>&1
    fi
}
verdict_of() { cut -d'|' -f1 "$1/.wacli-health.state" 2>/dev/null || echo "?"; }
lines_of()   { grep -c "" "$1/wacli-health.log" 2>/dev/null || echo 0; }

# --- Scenario A: geen spam op twee identieke, ongewijzigde OK-runs. ---
# De eerste run zet de baseline (de "(hersteld)" transitie vanaf een lege
# state-file bestond al vóór deze taak en levert altijd één regel op — dat
# alleen bewijst dus niets over de fix). De tweede run, met exact dezelfde
# staat, mag GEEN extra regel schrijven: de hartslag is nog niet verstreken.
WORK_A=$(mktemp -d)
run "$WORK_A" 6
[[ "$(verdict_of "$WORK_A")" == "OK" ]] \
    || fail "kon geen deterministische OK-verdict afdwingen (kreeg $(verdict_of "$WORK_A")) — test is niet hermetisch"
run "$WORK_A" 6
LINES_A=$(lines_of "$WORK_A")
(( LINES_A == 1 )) || fail "$LINES_A regel(s) na twee identieke OK-runs; verwacht precies 1 (geen spam)"
echo "==> Scenario A OK: geen dubbele regel bij twee identieke OK-runs ($LINES_A regel)"

# --- Scenario B: de kern van de fix. Onder de DEFAULT HEARTBEAT_HOURS (6,
# hier bewust NIET expliciet gezet) krijgt een log wiens laatste regel meer
# dan 6 uur oud is, bij de eerstvolgende ongewijzigde run een vers
# levensteken — zonder dat we echt 6 uur hoeven te wachten: de logregel
# wordt teruggezet in de tijd (touch -t), niet de klok van het systeem. Dit
# is bewust vóór scenario C geplaatst: HEARTBEAT_HOURS=0 in scenario C is
# een triviale grenswaarde (0 uur is per definitie al verstreken) en zou een
# gebroken default-drempel kunnen maskeren als hij later faalt. ---
WORK_B=$(mktemp -d)
run "$WORK_B" ""
[[ "$(verdict_of "$WORK_B")" == "OK" ]] \
    || fail "verdict is $(verdict_of "$WORK_B"), niet OK — scenario B vereist een OK-baseline"
BEFORE_B=$(lines_of "$WORK_B")
SEVEN_HOURS_AGO=$(date -v-7H '+%Y%m%d%H%M.%S')
touch -t "$SEVEN_HOURS_AGO" "$WORK_B/wacli-health.log" \
    || fail "kon de mtime van de logregel niet terugzetten"
run "$WORK_B" ""
AFTER_B=$(lines_of "$WORK_B")
(( AFTER_B > BEFORE_B )) \
    || fail "geen levensteken na >6 uur stilte onder de default HEARTBEAT_HOURS — de kern van de fix werkt niet"
echo "==> Scenario B OK: default HEARTBEAT_HOURS (6) geeft een levensteken na >6 uur stilte"

# --- Scenario C: HEARTBEAT_HOURS=0 forceert altijd een regel (triviale
# grenswaarde, aanvullend op scenario B). ---
WORK_C=$(mktemp -d)
run "$WORK_C" 6
BEFORE_C=$(lines_of "$WORK_C")
run "$WORK_C" 0
AFTER_C=$(lines_of "$WORK_C")
(( AFTER_C > BEFORE_C )) || fail "HEARTBEAT_HOURS=0 schreef geen regel"
echo "==> Scenario C OK: HEARTBEAT_HOURS=0 forceert een regel"

echo "==> Alle hartslag-scenario's geslaagd"
