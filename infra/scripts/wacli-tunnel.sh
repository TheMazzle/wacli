#!/bin/bash
# wacli IPC socket tunnel — forwards local socket to Mac Mini
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-tunnel.sh
# Installed to: ~/bin/wacli-tunnel.sh via `make install-tunnel`
#
# Runs on MacBook as LaunchAgent (KeepAlive).
# SSH forwards the Unix domain socket so whatslack can send
# IPC commands (send_text, mark_read, etc.) to Mac Mini's wacli.

SOCK="$HOME/.wacli/wacli.sock"
REMOTE="macmini"

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
