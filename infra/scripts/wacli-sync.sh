#!/bin/bash
# wacli sync daemon — started by LaunchAgent
# Source of truth: ~/Projects/wacli/infra/scripts/wacli-sync.sh
# Installed to: ~/bin/wacli-sync.sh via `make install`

exec /Users/wjj/bin/wacli sync --download-media --follow --backfill-gaps
