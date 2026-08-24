#!/bin/bash
# wacli-pair-web.sh — koppel wacli aan WhatsApp via een QR in je browser.
#
# Praat met de draaiende sync-daemon over de IPC-socket. De daemon blijft in
# pairing-mode leven zolang het device niet gekoppeld is en genereert daar de
# QR-codes; dit script toont ze alleen. Geen daemon stoppen, geen lock-gedoe,
# en opnieuw proberen kost één IPC-aanroep.
#
# Dit is exact het pad dat Whatslack ook gebruikt — werkt dit, dan werkt de app.
#
# Gebruik:  wacli-pair-web.sh [poort]        (default 8765)

set -uo pipefail

PORT="${1:-8765}"
LABEL="com.productfunction.wacli"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
SOCK="${WACLI_STORE_DIR:-$HOME/.wacli}/wacli.sock"

command -v qrencode >/dev/null || { echo "!! qrencode ontbreekt: brew install qrencode" >&2; exit 1; }

if ! pgrep -f 'wacli sync' >/dev/null 2>&1; then
    echo "==> Daemon draait niet; starten..."
    launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>/dev/null
    for _ in $(seq 1 15); do [[ -S "$SOCK" ]] && break; sleep 1; done
fi
if [[ ! -S "$SOCK" ]]; then
    echo "!! Geen wacli-socket op $SOCK. Draait de daemon?" >&2
    exit 1
fi

HOSTIP=$(ipconfig getifaddr en0 2>/dev/null || echo localhost)
TS_IP=$(/usr/local/bin/tailscale ip -4 2>/dev/null || /opt/homebrew/bin/tailscale ip -4 2>/dev/null || true)
echo
echo "==> Open in je browser:"
echo "      http://$HOSTIP:$PORT"
[[ -n "$TS_IP" ]] && echo "      http://$TS_IP:$PORT   (Tailscale)"
echo
echo "    Scan wanneer het jou uitkomt — verlopen codes worden vanzelf vervangen."
echo "    Stoppen: Ctrl+C (de daemon blijft draaien)."
echo

exec /usr/bin/python3 - "$PORT" "$SOCK" <<'PY'
import base64, http.server, json, socket, socketserver, subprocess, sys

port, sock_path = int(sys.argv[1]), sys.argv[2]

def ipc(command):
    """Eén JSON-regel heen, één terug — hetzelfde protocol dat Whatslack gebruikt."""
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(10)
    try:
        s.connect(sock_path)
        s.sendall((json.dumps({"command": command}) + "\n").encode())
        buf = b""
        while not buf.endswith(b"\n"):
            chunk = s.recv(4096)
            if not chunk:
                break
            buf += chunk
        resp = json.loads(buf.decode())
        return resp.get("data") if resp.get("success") else None
    except Exception:
        return None
    finally:
        s.close()

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
</style>
<div class=card>{body}</div>"""

def render():
    status = ipc("connection_status")
    if status is None:
        return PAGE.format(body=
            "<div class=err>&#10005;</div><h1>Daemon niet bereikbaar</h1>"
            "<p>De wacli sync-daemon reageert niet.</p>")

    state = status.get("state")

    if state == "syncing":
        return PAGE.format(body=
            "<div class=ok>&#10004;</div><h1>Gekoppeld</h1>"
            "<p>WhatsApp is weer verbonden. Berichten komen nu binnen; "
            "je kunt dit tabblad sluiten.</p>")

    # De daemon wacht op een koppelverzoek, of een eerdere poging is verlopen.
    # Opnieuw vragen is goedkoop en idempotent.
    if state == "needs_pairing":
        ipc("start_pairing")
        return PAGE.format(body=
            "<h1>Even geduld</h1><p>Koppelen wordt gestart&hellip;</p>")

    qr = ipc("pairing_qr") or {}
    code = qr.get("code", "")
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
