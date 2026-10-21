#!/bin/bash
# wacli IPC socket tunnel — forwards local socket to Mac Mini
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-tunnel.sh
# Installed to: ~/bin/wacli-tunnel.sh via `make install-tunnel`
#
# Runs on MacBook as LaunchAgent (KeepAlive).
# SSH forwards the Unix domain socket so whatslack can send
# IPC commands (send_text, mark_read, etc.) to Mac Mini's wacli.

SOCK="$HOME/.wacli/wacli.sock"
# Tailscale eerst, dan het LAN; zie wacli-db-sync.sh (2026-10-21) waarom.
REMOTES="${REMOTES:-macmini macmini-m4.fritz.box}"

REMOTE=""
for cand in $REMOTES; do
    if ssh -o BatchMode=yes -o ConnectTimeout=5 "$cand" true 2>/dev/null; then
        REMOTE="$cand"
        break
    fi
done
if [[ -z "$REMOTE" ]]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') Mac Mini unreachable via $REMOTES"
    # KeepAlive start ons opnieuw; niet elke 10s de log volschrijven.
    sleep 60
    exit 1
fi
echo "$(date '+%Y-%m-%d %H:%M:%S') tunnel via $REMOTE"

# Clean up stale socket from previous tunnel
rm -f "$SOCK"

# Forward local socket to Mac Mini's wacli socket
# -N: no remote command
# -o ExitOnForwardFailure=yes: fail if socket can't be forwarded
# ServerAliveInterval/CountMax: detect dead connections
exec ssh -N \
    -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=30 \
    -o ServerAliveCountMax=3 \
    -o BatchMode=yes \
    -L "$SOCK:$SOCK" \
    "$REMOTE"
