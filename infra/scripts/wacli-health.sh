#!/bin/bash
# wacli-health.sh — health check voor de wacli sync daemon.
#
# Waarom dit bestaat: op 2026-06-17 weigerde WhatsApp onze verouderde client
# (405) en ontkoppelde later het device. De daemon bleef draaien en zag er in
# `launchctl list` gezond uit, maar syncte 68 dagen lang geen enkel bericht.
# "Draait het proces nog?" is dus GEEN health signal. Berichtversheid wel.
#
# Checks (in volgorde van betrouwbaarheid):
#   1. [FATAL] regels in de log      -> daemon weet zelf dat hij stuk is
#   2. wacli doctor: authenticated   -> device ontkoppeld, QR-scan nodig
#   3. leeftijd nieuwste bericht     -> het echte end-to-end signaal
#   4. daemon proces aanwezig        -> zwakste check, staat bewust laatst
#
# Env overrides: WARN_HOURS (12), CRIT_HOURS (24), WACLI_STORE_DIR (~/.wacli),
#                HA_NOTIFY_TARGET (mobile_app_wjjs_iphone), LOG_DIR
#                (~/Library/Logs/Claude), HEARTBEAT_HOURS (6), WACLI_BIN
#                (~/bin/wacli), SYNC_LOG (~/Library/Logs/Whatslack/wacli-sync.log)
#                — de laatste twee bestaan puur om checks 1 en 2 in
#                test-health-heartbeat.sh te kunnen stubben, zonder ooit
#                echte device-credentials te hoeven kopiëren.

set -uo pipefail

STORE_DIR="${WACLI_STORE_DIR:-$HOME/.wacli}"
DB="$STORE_DIR/wacli.db"
WACLI_BIN="${WACLI_BIN:-$HOME/bin/wacli}"
SYNC_LOG="${SYNC_LOG:-$HOME/Library/Logs/Whatslack/wacli-sync.log}"
LOG_DIR="${LOG_DIR:-$HOME/Library/Logs/Claude}"
LOG_FILE="$LOG_DIR/wacli-health.log"
STATE_FILE="$LOG_DIR/.wacli-health.state"
NOTIFY="$HOME/Projects/bjorn-supervisor/infra/scripts/notify-user.sh"
HA_NOTIFY_TARGET="${HA_NOTIFY_TARGET:-mobile_app_wjjs_iphone}"

WARN_HOURS="${WARN_HOURS:-12}"
CRIT_HOURS="${CRIT_HOURS:-24}"

# Ook bij "alles in orde" periodiek een regel schrijven. Zonder levensteken is
# een stille log niet te onderscheiden van een monitor die zelf gestopt is.
# Een vaste kloktijd werkt niet: de LaunchAgent draait elke 1800s vanaf het
# laadmoment, dus altijd op dezelfde minuut — een match op "09:00" raakt nooit.
HEARTBEAT_HOURS="${HEARTBEAT_HOURS:-6}"
MAX_LOG_BYTES=$(( 5 * 1024 * 1024 ))

mkdir -p "$LOG_DIR"

if [[ -f "$LOG_FILE" ]]; then
    SIZE=$(stat -f%z "$LOG_FILE" 2>/dev/null || echo 0)
    if (( SIZE > MAX_LOG_BYTES )); then
        mv -f "$LOG_FILE" "$LOG_FILE.1" 2>/dev/null || true
    fi
fi

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOG_FILE"; }

# Vlak na boot is alles nog aan het opstarten — geen vals alarm.
UPTIME_SEC=$(( $(date +%s) - $(sysctl -n kern.boottime | sed -n 's/.*sec = \([0-9]*\).*/\1/p') ))
if (( UPTIME_SEC < 600 )); then
    log "SKIP: systeem ${UPTIME_SEC}s up (<600s)"
    exit 0
fi

LEVEL="OK"
DETAIL=""
ACTION=""
FINDINGS=()

# escalate <LEVEL> <detail> <action>
# Elke bevinding wordt gelogd (forensiek: tijdens het incident was er niets om
# op terug te kijken). LEVEL/DETAIL/ACTION houden de zwaarste bevinding vast —
# dat is wat er in de notificatie belandt.
escalate() {
    local lvl="$1" detail="$2" action="$3"
    FINDINGS+=("$lvl: $detail | actie: $action")
    if [[ "$LEVEL" == "CRITICAL" ]]; then return; fi
    if [[ "$lvl" == "CRITICAL" || "$LEVEL" == "OK" ]]; then
        LEVEL="$lvl"; DETAIL="$detail"; ACTION="$action"
    fi
}

# --- 1. Heeft de daemon zichzelf fataal verklaard? ---
if [[ -f "$SYNC_LOG" ]]; then
    FATAL_LINE=$(tail -c 200000 "$SYNC_LOG" 2>/dev/null | grep '\[FATAL\]' | tail -1)
    if [[ -n "$FATAL_LINE" ]]; then
        escalate CRITICAL "daemon meldt fatale staat: ${FATAL_LINE#*\[FATAL\] }" \
                 "zie $SYNC_LOG"
    fi
fi

# --- 2. Is het device nog gekoppeld? ---
if [[ -x "$WACLI_BIN" ]]; then
    DOCTOR=$("$WACLI_BIN" doctor --json 2>/dev/null)
    if [[ -n "$DOCTOR" ]]; then
        AUTHED=$(echo "$DOCTOR" | /usr/bin/python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["authenticated"])' 2>/dev/null)
        if [[ "$AUTHED" == "False" ]]; then
            escalate CRITICAL "device is NIET gekoppeld aan WhatsApp" \
                     "koppel via het statusbolletje in Whatslack, of run wacli-pair-web.sh"
        fi
    else
        escalate WARN "wacli doctor gaf geen output" "check of ~/bin/wacli werkt"
    fi
else
    escalate CRITICAL "wacli binary ontbreekt op $WACLI_BIN" "rebuild: make -C ~/Projects/wacli/infra build"
fi

# --- 3. Het echte signaal: hoe oud is het nieuwste bericht? ---
AGE_HOURS="?"
if [[ -f "$DB" ]]; then
    # .timeout is een dot-command en print niets; `PRAGMA busy_timeout` zou zijn
    # waarde in de resultset zetten en de leeftijdsberekening onzin maken.
    # mode=ro faalt met SQLITE_CANTOPEN op een WAL-db zonder bestaande -shm
    # (bijv. een verse .backup kopie), vandaar de read-write fallback.
    LAST_TS=$(sqlite3 -cmd ".timeout 5000" "file:$DB?mode=ro" "SELECT COALESCE(MAX(ts),0) FROM messages;" 2>/dev/null)
    if [[ ! "$LAST_TS" =~ ^[0-9]+$ ]]; then
        LAST_TS=$(sqlite3 -cmd ".timeout 5000" "$DB" "SELECT COALESCE(MAX(ts),0) FROM messages;" 2>/dev/null)
    fi
    if [[ "$LAST_TS" =~ ^[0-9]+$ && "$LAST_TS" != "0" ]]; then
        AGE_HOURS=$(( ( $(date +%s) - LAST_TS ) / 3600 ))
        LAST_HUMAN=$(date -r "$LAST_TS" '+%Y-%m-%d %H:%M')
        if (( AGE_HOURS >= CRIT_HOURS )); then
            escalate CRITICAL "geen nieuw bericht sinds $LAST_HUMAN (${AGE_HOURS}u, drempel ${CRIT_HOURS}u)" \
                     "check $SYNC_LOG"
        elif (( AGE_HOURS >= WARN_HOURS )); then
            escalate WARN "geen nieuw bericht sinds $LAST_HUMAN (${AGE_HOURS}u, drempel ${WARN_HOURS}u)" \
                     "check $SYNC_LOG"
        fi
    else
        escalate WARN "geen berichten in $DB" "check of sync ooit gedraaid heeft"
    fi
else
    escalate CRITICAL "database ontbreekt: $DB" "check WACLI_STORE_DIR"
fi

# --- 3b. Preventief: verouderde build. WhatsApp weigerde in juni een client
#         van 4 maanden oud. Een build ouder dan STALE_DAYS is een tikkende bom. ---
STALE_DAYS="${STALE_DAYS:-90}"
if [[ -f "$WACLI_BIN" ]]; then
    BIN_AGE_DAYS=$(( ( $(date +%s) - $(stat -f%m "$WACLI_BIN") ) / 86400 ))
    if (( BIN_AGE_DAYS > STALE_DAYS )); then
        escalate WARN "wacli build is ${BIN_AGE_DAYS} dagen oud (drempel ${STALE_DAYS}); WhatsApp weigert verouderde clients" \
                 "cd ~/Projects/wacli && go get go.mau.fi/whatsmeow@latest && make -C infra build reinstall"
    fi
fi

# --- 4. Draait het proces (zwakste check, staat bewust laatst) ---
if ! pgrep -f 'wacli sync' >/dev/null 2>&1; then
    escalate CRITICAL "sync daemon draait niet" \
             "launchctl kickstart -k gui/$(id -u)/com.productfunction.wacli"
fi

# --- Rapporteren, met de-duplicatie tegen alert fatigue ---
SIG="$LEVEL|$DETAIL"
PREV=$(cat "$STATE_FILE" 2>/dev/null || echo "")
echo "$SIG" > "$STATE_FILE"

if [[ "$LEVEL" == "OK" ]]; then
    if [[ "$PREV" != "$SIG" ]]; then
        log "OK: nieuwste bericht ${AGE_HOURS}u oud (hersteld)"
    else
        # Levensteken op basis van verstreken tijd, niet van een kloktijd.
        LAST_MOD=$(stat -f%m "$LOG_FILE" 2>/dev/null || echo 0)
        AGE_SECONDS=$(( $(date +%s) - LAST_MOD ))
        if (( AGE_SECONDS >= HEARTBEAT_HOURS * 3600 )); then
            log "OK: nieuwste bericht ${AGE_HOURS}u oud"
        fi
    fi
    exit 0
fi

for f in "${FINDINGS[@]}"; do log "$f"; done

# Zelfde probleem als vorige run? Niet opnieuw pushen (behalve om 09:00).
if [[ "$PREV" == "$SIG" && "$(date '+%H:%M')" != "09:00" ]]; then
    exit 0
fi

MSG="wacli sync $LEVEL: $DETAIL — $ACTION"

# Drie kanalen, bewust met verschillende afhankelijkheden. Tijdens de storing van
# juni 2026 wees notify-user.sh naar een verouderd Tailscale-IP; elf meldingen
# belandden ongezien in /tmp/notify-suppressed.log. Een alarm met één kanaal is
# een alarm dat je niet hoort.

# 1. macOS-notificatie op de MacBook (werkt alleen als die aan staat)
if [[ -x "$NOTIFY" ]]; then
    FORCE_NOTIFY=$([[ "$LEVEL" == "CRITICAL" ]] && echo 1 || echo 0) "$NOTIFY" "$MSG" >/dev/null 2>&1
fi

# 2. Push naar de telefoon via Home Assistant — alleen bij CRITICAL, en
#    onafhankelijk van of er een Mac aan staat.
if [[ "$LEVEL" == "CRITICAL" ]]; then
    # ~/.env NIET sourcen: dat voert de inhoud uit als shell-code. Een waarde
    # met spaties wordt dan een commando ("Should: command not found"). Alleen
    # de twee sleutels lezen die we nodig hebben.
    env_value() {
        [[ -f "$HOME/.env" ]] || return
        sed -n "s/^[[:space:]]*$1=//p" "$HOME/.env" | head -1 | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'\$//"
    }
    HA_URL="${HA_URL:-$(env_value HA_URL)}"
    HA_TOKEN="${HA_TOKEN:-$(env_value HA_TOKEN)}"

    if [[ -n "${HA_URL:-}" && -n "${HA_TOKEN:-}" ]]; then
        /usr/bin/curl -s -m 10 -o /dev/null \
            -H "Authorization: Bearer $HA_TOKEN" \
            -H "Content-Type: application/json" \
            -d "$(/usr/bin/python3 -c '
import json, sys
print(json.dumps({
    "title": "WhatsApp sync gestopt",
    "message": sys.argv[1],
    "data": {"push": {"interruption-level": "time-sensitive"}},
}))' "$DETAIL — $ACTION")" \
            "$HA_URL/api/services/notify/$HA_NOTIFY_TARGET" 2>/dev/null \
            && log "push verstuurd naar $HA_NOTIFY_TARGET" \
            || log "push naar Home Assistant mislukt"
    else
        log "geen HA_URL/HA_TOKEN in ~/.env; push overgeslagen"
    fi
fi

# 3. Lokale notificatie op de Mac Mini (geen scherm, maar wel zichtbaar bij VNC)
osascript -e "display notification \"$(echo "$MSG" | sed 's/"/\\"/g')\" with title \"wacli sync\" subtitle \"$LEVEL\" sound name \"Sosumi\"" >/dev/null 2>&1

exit 0
