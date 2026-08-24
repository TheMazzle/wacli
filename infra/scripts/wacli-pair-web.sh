#!/bin/bash
# wacli-pair-web.sh — koppel wacli aan WhatsApp via een QR in je browser.
#
# De Mac Mini is headless en WhatsApp-QR-codes verlopen elke ~20s; `wacli auth`
# geeft na een handvol codes op met "QR code timed out". Dit script houdt dat
# achter de schermen draaiend en serveert de actuele code op een pagina die
# zichzelf ververst: open de URL, scan wanneer het jou uitkomt, klaar.
#
# Gebruik:  wacli-pair-web.sh [poort]        (default 8765)
#           DEADLINE_MIN=30 wacli-pair-web.sh   (default 15 minuten)

set -uo pipefail

PORT="${1:-8765}"
DEADLINE_MIN="${DEADLINE_MIN:-15}"
LABEL="com.productfunction.wacli"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
UID_NUM=$(id -u)
WACLI="$HOME/bin/wacli"

# Kort pad: Unix-sockets en tijdelijke bestanden houden niet van diepe paden.
WORK=$(mktemp -d /tmp/wacli-pair.XXXXXX)
QR_TXT="$WORK/qr.txt"
AUTH_LOG="$WORK/auth.log"
STATE="$WORK/state"          # bevat: pairing | ok | failed
echo pairing > "$STATE"

cleanup() {
    [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null
    [[ -n "${LOOP_PID:-}" ]] && kill "$LOOP_PID" 2>/dev/null
    pkill -f 'wacli auth --qr-file' 2>/dev/null
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

command -v qrencode >/dev/null || { echo "!! qrencode ontbreekt: brew install qrencode" >&2; exit 1; }

is_authed() {
    "$WACLI" doctor --json 2>/dev/null \
        | /usr/bin/python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["authenticated"])' 2>/dev/null
}

echo "==> Daemon stoppen (geeft de store lock vrij)..."
launchctl bootout "gui/$UID_NUM/$LABEL" 2>/dev/null
for _ in $(seq 1 10); do pgrep -f 'wacli sync' >/dev/null 2>&1 || break; sleep 1; done
if pgrep -f 'wacli sync' >/dev/null 2>&1; then
    echo "!! sync draait nog; stop hem handmatig en probeer opnieuw." >&2
    exit 1
fi
pkill -f 'wacli auth --qr-file' 2>/dev/null   # restanten van een eerdere poging

echo "==> Koppelen starten (blijft ${DEADLINE_MIN} min actief)..."

# Herstart `wacli auth` telkens als de QR-serie verloopt, zodat er altijd een
# verse code klaarstaat. Succes wordt vastgesteld via `wacli doctor`, niet via
# de exit-code van een achtergrondproces — dat is onbetrouwbaar vanuit een subshell.
(
    END=$(( $(date +%s) + DEADLINE_MIN * 60 ))
    while (( $(date +%s) < END )); do
        : > "$QR_TXT"
        "$WACLI" auth --qr-file "$QR_TXT" >> "$AUTH_LOG" 2>&1
        if [[ "$(is_authed)" == "True" ]]; then
            echo ok > "$STATE"
            exit 0
        fi
        sleep 2
    done
    echo failed > "$STATE"
) &
LOOP_PID=$!

/usr/bin/python3 - "$PORT" "$QR_TXT" "$STATE" <<'PY' &
import http.server, socketserver, subprocess, sys, os, base64

port, qr_txt, state_file = int(sys.argv[1]), sys.argv[2], sys.argv[3]

PAGE = """<!doctype html><meta charset=utf-8>
<meta http-equiv=refresh content=3>
<title>wacli koppelen</title>
<style>
 body{{font:16px -apple-system,system-ui,sans-serif;display:flex;min-height:100vh;margin:0;
      align-items:center;justify-content:center;background:#f5f5f7;color:#1d1d1f}}
 .card{{background:#fff;padding:32px 40px;border-radius:16px;text-align:center;
        box-shadow:0 2px 20px rgba(0,0,0,.08);max-width:400px}}
 h1{{font-size:20px;margin:0 0 4px}} p{{color:#6e6e73;margin:4px 0 20px;line-height:1.45}}
 img{{width:280px;height:280px;image-rendering:pixelated}}
 .ok{{color:#0a7d28;font-size:44px;margin-bottom:8px}}
 .err{{color:#c00;font-size:44px;margin-bottom:8px}}
 code{{background:#f0f0f2;padding:2px 6px;border-radius:4px;font-size:14px}}
</style>
<div class=card>{body}</div>"""

def state():
    try:
        return open(state_file).read().strip()
    except OSError:
        return "pairing"

def render():
    st = state()
    if st == "ok":
        return PAGE.format(body=
            "<div class=ok>&#10004;</div><h1>Gekoppeld</h1>"
            "<p>WhatsApp is weer verbonden. De sync-daemon wordt gestart; "
            "je kunt dit tabblad sluiten.</p>")
    if st == "failed":
        return PAGE.format(body=
            "<div class=err>&#10005;</div><h1>Tijd verstreken</h1>"
            "<p>Er is binnen de tijdslimiet niet gescand. "
            "Start opnieuw met <code>wacli-pair-web.sh</code>.</p>")
    try:
        code = open(qr_txt).read().strip()
    except OSError:
        code = ""
    if not code:
        return PAGE.format(body=
            "<h1>Even geduld</h1><p>Nieuwe QR-code wordt opgehaald&hellip;</p>")
    png = subprocess.run(["qrencode", "-o", "-", "-s", "8", "-m", "2", code],
                         capture_output=True).stdout
    b64 = base64.b64encode(png).decode()
    return PAGE.format(body=
        "<h1>Scan met WhatsApp</h1>"
        "<p>Instellingen &rarr; Gekoppelde apparaten &rarr; Apparaat koppelen</p>"
        f"<img src='data:image/png;base64,{b64}'>"
        "<p style='margin-top:16px;font-size:13px'>De code ververst vanzelf, "
        "ook als hij verloopt. Neem gerust de tijd.</p>")

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = render().encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass

socketserver.TCPServer.allow_reuse_address = True
with socketserver.TCPServer(("0.0.0.0", port), H) as httpd:
    httpd.serve_forever()
PY
SERVER_PID=$!

sleep 1
HOSTIP=$(ipconfig getifaddr en0 2>/dev/null || echo localhost)
TS_IP=$(/usr/local/bin/tailscale ip -4 2>/dev/null || /opt/homebrew/bin/tailscale ip -4 2>/dev/null || true)

echo
echo "==> Open in je browser:"
echo "      http://$HOSTIP:$PORT"
[[ -n "$TS_IP" ]] && echo "      http://$TS_IP:$PORT   (Tailscale)"
echo
echo "    Scan wanneer het jou uitkomt — verlopen codes worden vanzelf vervangen."
echo

wait "$LOOP_PID"

if [[ "$(cat "$STATE")" != "ok" ]]; then
    echo "!! Niet gekoppeld binnen ${DEADLINE_MIN} minuten. Daemon blijft gestopt." >&2
    sleep 15
    exit 1
fi

echo "==> Gekoppeld. Daemon starten..."
launchctl bootstrap "gui/$UID_NUM" "$PLIST" || echo "!! bootstrap mislukt; check $PLIST" >&2
sleep 5
pgrep -fl 'wacli sync' || echo "!! daemon draait niet"
"$WACLI" doctor --json 2>/dev/null
echo
echo "Klaar. Volg de sync met: tail -f ~/Library/Logs/Whatslack/wacli-sync.log"
sleep 15
