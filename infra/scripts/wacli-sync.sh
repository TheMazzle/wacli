#!/bin/bash
# wacli sync daemon — started by LaunchAgent
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-sync.sh
# Installed to: ~/bin/wacli-sync.sh via `make install`
#
# Twee dingen die tijdens het 2026-06-17 incident pijn deden en hier zijn opgelost:
#   1. De log had alleen HH:MM:SS, geen datum. Het was daardoor niet vast te
#      stellen wanneer WhatsApp de client begon te weigeren.
#   2. De log roteerde nooit (1,4 MB single file, maanden aan regels).

set -o pipefail

LOG_DIR="$HOME/Library/Logs/Whatslack"
LOG_FILE="$LOG_DIR/wacli-sync.log"
MAX_BYTES=$(( 20 * 1024 * 1024 ))
KEEP=5

mkdir -p "$LOG_DIR"

# Roteer bij start als de log te groot geworden is.
if [[ -f "$LOG_FILE" ]]; then
    SIZE=$(stat -f%z "$LOG_FILE" 2>/dev/null || echo 0)
    if (( SIZE > MAX_BYTES )); then
        for (( i = KEEP - 1; i >= 1; i-- )); do
            [[ -f "$LOG_FILE.$i" ]] && mv "$LOG_FILE.$i" "$LOG_FILE.$((i+1))"
        done
        mv "$LOG_FILE" "$LOG_FILE.1"
        rm -f "$LOG_FILE.$((KEEP+1))"
    fi
fi

# Elke regel krijgt een ISO-8601 datumstempel. stdout gaat via launchd
# (StandardOutPath) naar $LOG_FILE, dus hier niet zelf naar het bestand schrijven.
exec /Users/wjj/bin/wacli sync --download-media --follow --backfill-gaps 2>&1 \
  | /usr/bin/perl -MPOSIX -pe 'BEGIN { $| = 1 } $_ = POSIX::strftime("%Y-%m-%dT%H:%M:%S%z ", localtime) . $_'
