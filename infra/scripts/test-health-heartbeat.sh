#!/bin/bash
# Bewijst dat de health-check een periodiek levensteken achterlaat, ook als
# alles in orde is. Zonder dat is stilte niet te onderscheiden van een monitor
# die zelf gestopt is.
#
# Hermetische fixtures (WACLI_BIN/SYNC_LOG/WACLI_STORE_DIR/dummy-proces/
# NOTIFY-stub/HA-sentinel/OSASCRIPT-stub) staan in health-test-common.sh,
# gedeeld met test-health-rotation.sh — zie dat bestand voor waarom elk van
# de vier checks én alle drie notificatiekanalen gestubd worden. Als deze
# opzet zelf geen OK-verdict oplevert, faalt de test meteen met een
# duidelijke melding ("test is niet hermetisch") in plaats van stilletjes
# een ander pad te testen. Scenario D hieronder is de uitzondering: die
# forceert bewust een echte CRITICAL (om het repush-mechanisme te testen) en
# stubt daarom zelf óók het HA-kanaal via run_health_crit — zie die functie.
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

# --- Scenario D: een aanhoudende CRITICAL met ONVERANDERDE DETAIL-tekst moet
# na REPUSH_HOURS opnieuw pushen, en NIET op elke tussentijdse run. Dit is de
# kern van de fix op regel ~186: vóór de fix was de enige uitzondering op
# "zelfde SIG? niet opnieuw pushen" een letterlijke kloktijd-match op
# "09:00", die de LaunchAgent (StartInterval 1800 vanaf laadmoment) nooit
# raakt — een statische DETAIL zoals "device is NIET gekoppeld aan
# WhatsApp" zou dan na de allereerste push nooit meer een tweede krijgen.
#
# Eigen CRIT-stub (i.p.v. de gedeelde WACLI_BIN_STUB uit health-test-common.sh,
# die altijd authenticated:true teruggeeft): authenticated:false forceert
# check 2 (device-koppeling) naar CRITICAL met een vaste tekst — geen
# tijd-afhankelijk element in DETAIL, dus SIG blijft constant over runs.
CRIT_BIN_STUB="$HEALTH_FIXTURES/fake-wacli-crit"
cat > "$CRIT_BIN_STUB" <<'STUB'
#!/bin/bash
echo '{"success":true,"data":{"authenticated":false,"connected":false,"lock_held":false,"fts_enabled":true},"error":null}'
STUB
chmod +x "$CRIT_BIN_STUB"

run_health_crit() {
    # $1 = werkmap (LOG_DIR), $2 = REPUSH_HOURS ("" = default van het script)
    #
    # Dit is de ENIGE plek in de hele test-suite die het script een echte
    # CRITICAL laat bereiken (nodig om het repush-mechanisme te testen), dus
    # de ENIGE plek waar kanaal 2 (Home Assistant) en kanaal 3 (osascript)
    # ooit daadwerkelijk geactiveerd worden. HA_URL/HA_TOKEN/OSASCRIPT komen
    # HIER expliciet mee — niet impliciet via ongezet laten. Tot 2026-08-26
    # gebeurde dat niet: HA_URL/HA_TOKEN bleven ongezet, en wacli-health.sh
    # las ze dan via env_value() gewoon uit de ECHTE ~/.env — elke run van
    # Scenario D stuurde zo een ECHTE Home Assistant-push naar de telefoon
    # van de gebruiker, met een verzonnen "device niet gekoppeld"-alarm.
    # Zie HA_URL_STUB in health-test-common.sh voor waarom noch "ongezet
    # laten" noch HA_URL="" hier veilig is (bash's `${VAR:-...}` behandelt
    # expliciet-leeg hetzelfde als ongezet).
    local work="$1" rh="${2:-}"
    if [[ -n "$rh" ]]; then
        LOG_DIR="$work" WACLI_STORE_DIR="$HEALTH_STORE" WACLI_BIN="$CRIT_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" NOTIFY="$NOTIFY_STUB" REPUSH_HOURS="$rh" \
            HA_URL="$HA_URL_STUB" HA_TOKEN="$HA_TOKEN_STUB" OSASCRIPT="$OSASCRIPT_STUB" \
            bash "$HEALTH" >/dev/null 2>&1
    else
        LOG_DIR="$work" WACLI_STORE_DIR="$HEALTH_STORE" WACLI_BIN="$CRIT_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" NOTIFY="$NOTIFY_STUB" \
            HA_URL="$HA_URL_STUB" HA_TOKEN="$HA_TOKEN_STUB" OSASCRIPT="$OSASCRIPT_STUB" \
            bash "$HEALTH" >/dev/null 2>&1
    fi
}

notify_calls_of() { grep -c "^CALLED:" "$NOTIFY_STUB_LOG" 2>/dev/null || echo 0; }
ha_mislukt_calls_of() { grep -c "push naar Home Assistant mislukt" "$1/wacli-health.log" 2>/dev/null || echo 0; }

# Bewijst dat het Home Assistant-kanaal (kanaal 2) daadwerkelijk de
# onbereikbare sentinel-URL gebruikte, en niet stiekem terugviel op de ECHTE
# ~/.env-credentials: de health-log moet de MISLUKTE pushpoging melden
# ("mislukt" — connection refused op het sentinel-adres), en NOOIT een
# GESLAAGDE ("push verstuurd"). Zonder deze assertie zou iemand de
# HA_URL_STUB/HA_TOKEN_STUB-override in run_health_crit stilletjes kunnen
# weglaten (bijv. bij een toekomstige refactor) zonder dat de test-suite dat
# opmerkt — en dan stuurt de eerstvolgende run weer een ECHTE push naar de
# telefoon van de gebruiker, exact het lek van 2026-08-26.
assert_ha_push_never_succeeded() {
    local work="$1" label="$2"
    grep -q "push verstuurd naar" "$work/wacli-health.log" 2>/dev/null \
        && fail "$label: health-log meldt een GESLAAGDE HA-push — de sentinel-override werkt niet, dit zou in productie een ECHTE push naar de telefoon zijn geweest"
    return 0
}

WORK_D=$(mktemp -d)
: > "$NOTIFY_STUB_LOG"

# Eerste run: nieuwe SIG (PREV was leeg) -> altijd pushen, ongeacht REPUSH_HOURS.
run_health_crit "$WORK_D" ""
[[ "$(health_verdict_of "$WORK_D")" == "CRITICAL" ]] \
    || fail "kon geen deterministische CRITICAL-verdict afdwingen (kreeg $(health_verdict_of "$WORK_D")) — test is niet hermetisch"
SIG_D=$(cat "$WORK_D/.wacli-health.state")
CALLS_1=$(notify_calls_of)
(( CALLS_1 == 1 )) \
    || fail "eerste CRITICAL-run pushte $CALLS_1 keer, verwacht precies 1 (eerste keer dit probleem)"
assert_ha_push_never_succeeded "$WORK_D" "Scenario D stap 1"
HA_MISLUKT_1=$(ha_mislukt_calls_of "$WORK_D")
(( HA_MISLUKT_1 == 1 )) \
    || fail "Scenario D stap 1: $HA_MISLUKT_1 mislukte HA-pushpogingen gelogd, verwacht precies 1 — het HA-pad werd niet (of niet via de sentinel) doorlopen"
echo "==> Scenario D stap 1 OK: eerste CRITICAL pusht (1x), HA-kanaal raakte alleen de sentinel"

# Tweede run, direct erna, exact dezelfde DETAIL-tekst (statisch, geen
# tijd-element) -> zelfde SIG, nog geen REPUSH_HOURS verstreken -> GEEN
# nieuwe push.
run_health_crit "$WORK_D" ""
[[ "$(cat "$WORK_D/.wacli-health.state")" == "$SIG_D" ]] \
    || fail "SIG veranderde tussen twee identieke CRITICAL-runs — test-aanname klopt niet"
CALLS_2=$(notify_calls_of)
(( CALLS_2 == CALLS_1 )) \
    || fail "aanhoudende CRITICAL pushte al vóór REPUSH_HOURS verstreken was ($CALLS_2 na run 2, verwacht $CALLS_1) — dit is precies de spam die de dedup moet voorkomen"
assert_ha_push_never_succeeded "$WORK_D" "Scenario D stap 2"
HA_MISLUKT_2=$(ha_mislukt_calls_of "$WORK_D")
(( HA_MISLUKT_2 == HA_MISLUKT_1 )) \
    || fail "Scenario D stap 2: aantal mislukte HA-pushpogingen liep op ($HA_MISLUKT_1 -> $HA_MISLUKT_2) terwijl de dedup nog geen push had mogen laten proberen"
echo "==> Scenario D stap 2 OK: geen herhaalde push vóór REPUSH_HOURS verstreken"

# REPUSH_HOURS default is 4u. Zet het laatste-push-tijdstip 5 uur terug
# (mtime, niet de systeemklok — zie scenario B) en run opnieuw: dezelfde
# onveranderde DETAIL-tekst moet nu WEL opnieuw pushen.
FIVE_HOURS_AGO=$(date -v-5H '+%Y%m%d%H%M.%S')
touch -t "$FIVE_HOURS_AGO" "$WORK_D/.wacli-health.last-push" \
    || fail "kon de mtime van PUSH_STATE_FILE niet terugzetten"
run_health_crit "$WORK_D" ""
[[ "$(cat "$WORK_D/.wacli-health.state")" == "$SIG_D" ]] \
    || fail "SIG veranderde ná het terugzetten van de mtime — test-aanname klopt niet"
CALLS_3=$(notify_calls_of)
(( CALLS_3 > CALLS_2 )) \
    || fail "geen herhaalde push na >REPUSH_HOURS aanhoudende CRITICAL met onveranderde detail-tekst — de kern van de fix werkt niet"
assert_ha_push_never_succeeded "$WORK_D" "Scenario D stap 3"
HA_MISLUKT_3=$(ha_mislukt_calls_of "$WORK_D")
(( HA_MISLUKT_3 > HA_MISLUKT_2 )) \
    || fail "Scenario D stap 3: geen extra mislukte HA-pushpoging na de repush — het HA-kanaal deed niet mee aan de repush-test"
echo "==> Scenario D stap 3 OK: aanhoudende CRITICAL pusht opnieuw na REPUSH_HOURS (default 4u), HA-kanaal raakte alleen de sentinel"

echo "==> Scenario D OK: repush-mechanisme voor aanhoudende CRITICAL met statische detail-tekst"

echo "==> Alle hartslag-scenario's geslaagd"
