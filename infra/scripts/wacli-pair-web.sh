#!/bin/bash
# wacli-pair-web.sh — koppel wacli aan WhatsApp via een QR in je browser.
#
# De Mac Mini is headless en QR-codes verlopen elke ~20s, waardoor koppelen via
# een terminal of via een chat-heen-en-weer onhandig is. Dit script serveert de
# QR op een pagina die zichzelf ververst: open de URL, scan, klaar.
#
# Gebruik:  wacli-pair-web.sh [poort]        (default 8765)

set -uo pipefail

PORT="${1:-8765}"
LABEL="com.productfunction.wacli"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
UID_NUM=$(id -u)
WORK=$(mktemp -d /tmp/wacli-pair.XXXXXX)
QR_TXT="$WORK/qr.txt"
AUTH_LOG="$WORK/auth.log"
DONE_FLAG="$WORK/done"

cleanup() {
    [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null
    [[ -n "${AUTH_PID:-}" ]] && kill "$AUTH_PID" 2>/dev/null
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

command -v qrencode >/dev/null || { echo "!! qrencode ontbreekt: brew install qrencode" >&2; exit 1; }

echo "==> Daemon stoppen (geeft de store lock vrij)..."
launchctl bootout "gui/$UID_NUM/$LABEL" 2>/dev/null
for _ in $(seq 1 10); do pgrep -f 'wacli sync' >/dev/null 2>&1 || break; sleep 1; done
if pgrep -f 'wacli sync' >/dev/null 2>&1; then
    echo "!! sync draait nog; stop hem handmatig en probeer opnieuw." >&2
    exit 1
fi

echo "==> Koppelen starten..."
"$HOME/bin/wacli" auth --qr-file "$QR_TXT" > "$AUTH_LOG" 2>&1 &
AUTH_PID=$!

# Zet een vlag zodra het koppelen gelukt is, zodat de pagina dat kan tonen.
( wait "$AUTH_PID"; echo $? > "$DONE_FLAG" ) &

/usr/bin/python3 - "$PORT" "$QR_TXT" "$DONE_FLAG" "$AUTH_LOG" <<'PY' &
import http.server, socketserver, subprocess, sys, os, base64, html

port, qr_txt, done_flag, auth_log = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]

PAGE = """<!doctype html><meta charset=utf-8>
<meta http-equiv=refresh content=3>
<title>wacli koppelen</title>
<style>
 body{{font:16px -apple-system,system-ui,sans-serif;display:flex;min-height:100vh;margin:0;
      align-items:center;justify-content:center;background:#f5f5f7;color:#1d1d1f}}
 .card{{background:#fff;padding:32px 40px;border-radius:16px;text-align:center;
        box-shadow:0 2px 20px rgba(0,0,0,.08);max-width:420px}}
 h1{{font-size:20px;margin:0 0 4px}} p{{color:#6e6e73;margin:4px 0 20px;line-height:1.45}}
 img{{width:280px;height:280px;image-rendering:pixelated}}
 .ok{{color:#0a7d28;font-size:44px;margin-bottom:8px}}
 .err{{color:#c00;font-size:44px;margin-bottom:8px}}
 code{{background:#f0f0f2;padding:2px 6px;border-radius:4px;font-size:14px}}
</style>
<div class=card>{body}</div>"""

def render():
    if os.path.exists(done_flag):
        rc = open(done_flag).read().strip()
        if rc == "0":
            return PAGE.format(body=
                "<div class=ok>&#10004;</div><h1>Gekoppeld</h1>"
                "<p>WhatsApp is weer verbonden. De sync-daemon wordt gestart; "
                "je kunt dit tabblad sluiten.</p>")
        tail = html.escape("".join(open(auth_log, errors="replace").readlines()[-6:]))
        return PAGE.format(body=
            f"<div class=err>&#10005;</div><h1>Koppelen mislukt</h1>"
            f"<p>Start opnieuw met <code>wacli-pair-web.sh</code>.</p>"
            f"<pre style='text-align:left;font-size:12px;color:#6e6e73'>{tail}</pre>")
    try:
        code = open(qr_txt).read().strip()
    except OSError:
        code = ""
    if not code:
        return PAGE.format(body="<h1>Even geduld</h1><p>QR-code wordt opgehaald&hellip;</p>")
    png = subprocess.run(["qrencode", "-o", "-", "-s", "8", "-m", "2", code],
                         capture_output=True).stdout
    b64 = base64.b64encode(png).decode()
    return PAGE.format(body=
        f"<h1>Scan met WhatsApp</h1>"
        f"<p>Instellingen &rarr; Gekoppelde apparaten &rarr; Apparaat koppelen</p>"
        f"<img src='data:image/png;base64,{b64}'>"
        f"<p style='margin-top:16px;font-size:13px'>De code ververst vanzelf. "
        f"Laat dit tabblad open tot je &#10004; ziet.</p>")

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
HOSTIP=$(ipconfig getifaddr en0 2>/dev/null || echo "localhost")
TS_IP=$(/usr/local/bin/tailscale ip -4 2>/dev/null || /opt/homebrew/bin/tailscale ip -4 2>/dev/null || true)

echo
echo "==> Open in je browser:"
echo "      http://$HOSTIP:$PORT"
[[ -n "$TS_IP" ]] && echo "      http://$TS_IP:$PORT   (Tailscale, werkt ook buitenshuis)"
echo
echo "    Scan de QR met WhatsApp -> Instellingen -> Gekoppelde apparaten."
echo "    De pagina ververst zichzelf; wachten hoeft niet snel."
echo

wait "$AUTH_PID"
AUTH_RC=$?

if (( AUTH_RC != 0 )); then
    echo "!! Koppelen mislukt (exit $AUTH_RC). Daemon blijft gestopt." >&2
    tail -5 "$AUTH_LOG" >&2
    sleep 20   # laat de pagina de fout nog even tonen
    exit 1
fi

echo "==> Gekoppeld. Daemon starten..."
launchctl bootstrap "gui/$UID_NUM" "$PLIST" || echo "!! bootstrap mislukt; check $PLIST" >&2
sleep 5
pgrep -fl 'wacli sync' || echo "!! daemon draait niet"
"$HOME/bin/wacli" doctor --json 2>/dev/null
echo
echo "Klaar. Volg de sync met: tail -f ~/Library/Logs/Whatslack/wacli-sync.log"
sleep 10   # laat de bevestigingspagina nog even staan
