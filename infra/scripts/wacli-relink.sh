#!/bin/bash
# wacli-relink.sh — koppel wacli opnieuw aan WhatsApp via QR.
#
# Nodig wanneer `wacli doctor --json` `authenticated: false` geeft, of wanneer de
# health monitor "device is NIET gekoppeld" meldt. WhatsApp laat gekoppelde
# apparaten na ~14 dagen inactiviteit vervallen.
#
# Dit script regelt de volgorde die makkelijk fout gaat: de daemon houdt de
# store lock, dus die moet eerst weg, en daarna weer terug.
#
# Je berichten blijven staan — wacli.db wordt niet aangeraakt.

set -uo pipefail

LABEL="com.productfunction.wacli"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
UID_NUM=$(id -u)

echo "==> Daemon stoppen (geeft de store lock vrij)..."
launchctl bootout "gui/$UID_NUM/$LABEL" 2>/dev/null
for _ in $(seq 1 10); do
    pgrep -f 'wacli sync' >/dev/null 2>&1 || break
    sleep 1
done
if pgrep -f 'wacli sync' >/dev/null 2>&1; then
    echo "!! sync draait nog; stop hem handmatig en probeer opnieuw." >&2
    exit 1
fi

echo
echo "==> Scan de QR met WhatsApp op je telefoon:"
echo "    Instellingen -> Gekoppelde apparaten -> Apparaat koppelen"
echo "    (de code ververst elke ~20s, dat is normaal)"
echo
if ! "$HOME/bin/wacli" auth; then
    echo
    echo "!! Koppelen mislukt. Daemon blijft gestopt." >&2
    echo "   Probeer opnieuw met: $0" >&2
    exit 1
fi

echo
echo "==> Koppelstatus verifiëren..."
AUTHED=$("$HOME/bin/wacli" doctor --json 2>/dev/null \
    | /usr/bin/python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["authenticated"])' 2>/dev/null)
if [[ "$AUTHED" != "True" ]]; then
    echo "!! Nog steeds niet geauthenticeerd. Daemon blijft gestopt." >&2
    exit 1
fi
echo "    OK — device gekoppeld."

echo "==> Daemon starten..."
launchctl bootstrap "gui/$UID_NUM" "$PLIST" || {
    echo "!! bootstrap mislukt; check $PLIST" >&2
    exit 1
}
sleep 5

echo
echo "==> Status:"
pgrep -fl 'wacli sync' || echo "    !! daemon draait niet"
"$HOME/bin/wacli" doctor --json 2>/dev/null

echo
echo "Volg de sync met:"
echo "  tail -f ~/Library/Logs/Whatslack/wacli-sync.log"
echo "Health check:"
echo "  ~/bin/wacli-health.sh && tail ~/Library/Logs/Claude/wacli-health.log"
