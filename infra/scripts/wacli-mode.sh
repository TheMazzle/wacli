#!/bin/bash
# wacli mode toggle — switch between primary (Mac Mini) and fallback (local) mode
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-mode.sh
# Installed to: ~/bin/wacli-mode.sh via `make install-sync`
#
# Usage:
#   wacli-mode.sh primary   — use Mac Mini wacli (sync + tunnel)
#   wacli-mode.sh fallback  — use local wacli (direct WhatsApp connection)
#   wacli-mode.sh status    — show current mode

set -euo pipefail

UID_NUM=$(id -u)
SYNC_LABEL="com.productfunction.wacli-sync"
TUNNEL_LABEL="com.productfunction.wacli-tunnel"
DAEMON_LABEL="com.productfunction.wacli"
PLIST_DIR="$HOME/Library/LaunchAgents"
SESSION_LOCAL="$HOME/.wacli/session-local.db"
SESSION_ACTIVE="$HOME/.wacli/session.db"

load_agent() {
    launchctl bootstrap "gui/$UID_NUM" "$PLIST_DIR/$1.plist" 2>/dev/null || true
}

unload_agent() {
    launchctl bootout "gui/$UID_NUM/$1" 2>/dev/null || true
}

is_loaded() {
    launchctl print "gui/$UID_NUM/$1" >/dev/null 2>&1
}

case "${1:-status}" in
    primary)
        echo "==> Switching to PRIMARY mode (Mac Mini)"
        # Stop local wacli daemon if running
        unload_agent "$DAEMON_LABEL"
        sleep 1
        # Clean up local socket (tunnel will create its own)
        rm -f "$HOME/.wacli/wacli.sock"
        rm -f "$HOME/.wacli/LOCK"
        # Start sync + tunnel
        load_agent "$SYNC_LABEL"
        load_agent "$TUNNEL_LABEL"
        echo "==> Done. Sync + tunnel active."
        ;;
    fallback)
        echo "==> Switching to FALLBACK mode (local wacli)"
        # Stop sync + tunnel
        unload_agent "$SYNC_LABEL"
        unload_agent "$TUNNEL_LABEL"
        sleep 1
        rm -f "$HOME/.wacli/wacli.sock"
        # Activate local session if available
        if [ -f "$SESSION_LOCAL" ]; then
            cp "$SESSION_LOCAL" "$SESSION_ACTIVE"
            echo "  Restored local session from session-local.db"
        else
            echo "  WARNING: No local session found at $SESSION_LOCAL"
            echo "  Run 'wacli auth' to link this device to WhatsApp"
        fi
        # Start local wacli daemon
        load_agent "$DAEMON_LABEL"
        echo "==> Done. Local wacli daemon active."
        ;;
    status)
        echo "=== wacli Mode Status ==="
        if is_loaded "$SYNC_LABEL" && is_loaded "$TUNNEL_LABEL"; then
            echo "Mode: PRIMARY (Mac Mini via sync + tunnel)"
        elif is_loaded "$DAEMON_LABEL"; then
            echo "Mode: FALLBACK (local wacli daemon)"
        else
            echo "Mode: UNKNOWN (no agents loaded)"
        fi
        echo ""
        echo "Agents:"
        is_loaded "$SYNC_LABEL" && echo "  sync:   LOADED" || echo "  sync:   not loaded"
        is_loaded "$TUNNEL_LABEL" && echo "  tunnel: LOADED" || echo "  tunnel: not loaded"
        is_loaded "$DAEMON_LABEL" && echo "  daemon: LOADED" || echo "  daemon: not loaded"
        echo ""
        echo "Local session: $([ -f "$SESSION_LOCAL" ] && echo "EXISTS" || echo "NOT FOUND")"
        ;;
    *)
        echo "Usage: $0 {primary|fallback|status}"
        exit 1
        ;;
esac
