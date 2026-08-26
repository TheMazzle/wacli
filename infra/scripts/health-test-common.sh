#!/bin/bash
# Gedeelde hermetische fixtures voor de wacli-health.sh tests
# (test-health-heartbeat.sh, test-health-rotation.sh). Geen standalone test:
# wordt gesourced, niet direct uitgevoerd.
#
# Forceert een deterministische OK-verdict af zonder ooit echte
# device-credentials (session.db) aan te raken, en stubt alle DRIE
# notificatiekanalen zodat een onverwacht niet-OK-pad — bijvoorbeeld door
# een toekomstige regressie, of een test die bewust CRITICAL forceert —
# nooit een echte macOS-notificatie of Home Assistant-push kan versturen:
#   1. SYNC_LOG   -> lege temp-file, dus nooit een [FATAL]-regel.
#   2. WACLI_BIN  -> stub die altijd `{"authenticated":true,...}` teruggeeft.
#   3. WACLI_STORE_DIR -> temp store met alléén de "messages"-tabel die het
#      script zelf query't (niet het volledige wacli-schema — dat is nu niet
#      nodig, want `wacli doctor` zelf is gestubd, dus de echte migraties
#      worden nooit aangeroepen).
#   4. Het daemon-procesargument ('wacli sync') -> een kortlevend `sleep`
#      met een omgedoopte cmdline (`exec -a`), zodat `pgrep -f 'wacli sync'`
#      iets vindt zonder de echte daemon nodig te hebben.
#   5. NOTIFY -> stub die alleen zijn argumenten wegschrijft (defense in
#      depth: als de hermetische opzet ooit toch een niet-OK-verdict
#      oplevert, mag er nog steeds geen echte notificatie uit).
#   6. HA_URL/HA_TOKEN -> gezet naar een gegarandeerd onbereikbaar
#      localhost-sentinel (poort 1, connection refused, geen timeout) plus
#      een evident nep-token. NOOIT leeg laten (""): wacli-health.sh leest
#      HA_URL/HA_TOKEN via `${VAR-...}` (zie de toelichting daar) — een
#      HELEMAAL niet gezette variabele valt terug op de ECHTE ~/.env, een
#      wél (ook leeg) gezette variabele niet. Een niet-lege sentinel-waarde
#      wint dus altijd, ongeacht of die val-terug-logica ooit weer stuk gaat.
#      Dit was tot 2026-08-26 het lek waardoor Scenario D in
#      test-health-heartbeat.sh een ECHTE Home Assistant-push naar de
#      telefoon stuurde.
#   7. OSASCRIPT -> stub die alleen wegschrijft dat hij aangeroepen is, zodat
#      een test-run nooit een echte desktop-notificatie kan laten verschijnen.
#
# Gebruik in een test:
#   source "$(dirname "${BASH_SOURCE[0]}")/health-test-common.sh"
#   trap cleanup_health_fixtures EXIT
#   setup_health_fixtures
#   run_health "$WORK_DIR" "$HEARTBEAT_HOURS_OR_LEEG"
#   health_verdict_of "$WORK_DIR"   # OK/WARN/CRITICAL/?
#   health_lines_of "$WORK_DIR"     # aantal regels in wacli-health.log

HEALTH="${HEALTH:-$HOME/Projects/wacli/infra/scripts/wacli-health.sh}"

setup_health_fixtures() {
    command -v sqlite3 >/dev/null || { echo "!! sqlite3 niet gevonden" >&2; exit 1; }

    HEALTH_FIXTURES=$(mktemp -d /tmp/health-test-fixtures.XXXXXX)

    WACLI_BIN_STUB="$HEALTH_FIXTURES/fake-wacli"
    cat > "$WACLI_BIN_STUB" <<'STUB'
#!/bin/bash
echo '{"success":true,"data":{"authenticated":true,"connected":true,"lock_held":false,"fts_enabled":true},"error":null}'
STUB
    chmod +x "$WACLI_BIN_STUB"

    SYNC_LOG_STUB="$HEALTH_FIXTURES/wacli-sync.log"
    : > "$SYNC_LOG_STUB"

    NOTIFY_STUB="$HEALTH_FIXTURES/fake-notify.sh"
    cat > "$NOTIFY_STUB" <<'STUB'
#!/bin/bash
echo "CALLED:$*" >> "$NOTIFY_STUB_LOG"
exit 0
STUB
    chmod +x "$NOTIFY_STUB"
    NOTIFY_STUB_LOG="$HEALTH_FIXTURES/notify.log"
    export NOTIFY_STUB_LOG

    # Onbereikbaar localhost-sentinel (poort 1: connection refused, geen
    # 10s-timeout) + een evident nep-token. Nooit "" — zie de toelichting
    # bovenaan dit bestand voor waarom leeg NIET hetzelfde is als veilig.
    HA_URL_STUB="http://127.0.0.1:1"
    HA_TOKEN_STUB="test-sentinel-token-not-real"

    OSASCRIPT_STUB="$HEALTH_FIXTURES/fake-osascript"
    cat > "$OSASCRIPT_STUB" <<'STUB'
#!/bin/bash
echo "CALLED:$*" >> "$OSASCRIPT_STUB_LOG"
exit 0
STUB
    chmod +x "$OSASCRIPT_STUB"
    OSASCRIPT_STUB_LOG="$HEALTH_FIXTURES/osascript.log"
    export OSASCRIPT_STUB_LOG

    HEALTH_STORE="$HEALTH_FIXTURES/store"
    mkdir -p "$HEALTH_STORE"
    sqlite3 "$HEALTH_STORE/wacli.db" \
        "CREATE TABLE messages (ts INTEGER); INSERT INTO messages (ts) VALUES ($(date +%s));" \
        || { echo "!! kon test-database niet aanmaken" >&2; exit 1; }

    # Dummy proces met een cmdline die 'wacli sync' bevat, voor de
    # procescheck (bewust de zwakste check, staat laatst in het script).
    bash -c 'exec -a "wacli sync (test-stub)" sleep 120' &
    HEALTH_DUMMY_PID=$!
    sleep 0.3   # geef het even tijd om in de procestabel te verschijnen
}

cleanup_health_fixtures() {
    [[ -n "${HEALTH_FIXTURES:-}" ]] && rm -rf "$HEALTH_FIXTURES"
    [[ -n "${HEALTH_DUMMY_PID:-}" ]] && kill "$HEALTH_DUMMY_PID" 2>/dev/null
    [[ -n "${HEALTH_DUMMY_PID:-}" ]] && wait "$HEALTH_DUMMY_PID" 2>/dev/null
    return 0
}

run_health() {
    # $1 = werkmap voor deze scenario-run (wordt LOG_DIR), $2 = HEARTBEAT_HOURS ("" = default van het script)
    local work="$1" hh="${2:-}"
    if [[ -n "$hh" ]]; then
        LOG_DIR="$work" WACLI_STORE_DIR="$HEALTH_STORE" WACLI_BIN="$WACLI_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" NOTIFY="$NOTIFY_STUB" HEARTBEAT_HOURS="$hh" \
            HA_URL="$HA_URL_STUB" HA_TOKEN="$HA_TOKEN_STUB" OSASCRIPT="$OSASCRIPT_STUB" \
            bash "$HEALTH" >/dev/null 2>&1
    else
        LOG_DIR="$work" WACLI_STORE_DIR="$HEALTH_STORE" WACLI_BIN="$WACLI_BIN_STUB" \
            SYNC_LOG="$SYNC_LOG_STUB" NOTIFY="$NOTIFY_STUB" \
            HA_URL="$HA_URL_STUB" HA_TOKEN="$HA_TOKEN_STUB" OSASCRIPT="$OSASCRIPT_STUB" \
            bash "$HEALTH" >/dev/null 2>&1
    fi
}

health_verdict_of() { cut -d'|' -f1 "$1/.wacli-health.state" 2>/dev/null || echo "?"; }
health_lines_of()   { grep -c "" "$1/wacli-health.log" 2>/dev/null || echo 0; }
