# wacli fork infrastructure
# Source of truth: ~/Projects/wacli/infra/
# Pattern: bjorn-de-borg infra (copy, not symlink)
#
# Targets:
#   Mac Mini (wacli daemon):  install, uninstall, reinstall, status, logs, build
#   MacBook (sync + tunnel):  install-sync, install-tunnel, uninstall-sync, uninstall-tunnel
#   Mode toggle:              mode-primary, mode-fallback, mode-status

LABEL = com.productfunction.wacli
LABEL_SYNC = com.productfunction.wacli-sync
LABEL_TUNNEL = com.productfunction.wacli-tunnel
LABEL_HEALTH = com.productfunction.wacli-health
UID := $(shell id -u)
LOG_DIR = $(HOME)/Library/Logs/Whatslack

# === Daemon (Mac Mini) ===

PLIST_SRC = launchagents/$(LABEL).plist
PLIST_DST = $(HOME)/Library/LaunchAgents/$(LABEL).plist
SCRIPT_SRC = scripts/wacli-sync.sh
SCRIPT_DST = $(HOME)/bin/wacli-sync.sh
LOG_FILE = $(LOG_DIR)/wacli-sync.log

.PHONY: install uninstall reinstall status logs build
.PHONY: install-sync uninstall-sync install-tunnel uninstall-tunnel
.PHONY: mode-primary mode-fallback mode-status
.PHONY: install-health uninstall-health health

install: $(LOG_DIR) install-health
	@echo "==> Installing wacli-sync script..."
	cp $(SCRIPT_SRC) $(SCRIPT_DST)
	chmod +x $(SCRIPT_DST)
	@echo "==> Installing LaunchAgent..."
	cp $(PLIST_SRC) $(PLIST_DST)
	launchctl bootstrap gui/$(UID) $(PLIST_DST)
	@echo "==> Done. Check: make status"

$(LOG_DIR):
	mkdir -p $(LOG_DIR)

uninstall: uninstall-health
	@echo "==> Stopping LaunchAgent..."
	-launchctl bootout gui/$(UID)/$(LABEL) 2>/dev/null
	@echo "==> Removing files..."
	-rm -f $(PLIST_DST)
	-rm -f $(SCRIPT_DST)
	@echo "==> Done."

reinstall: uninstall
	@sleep 2
	$(MAKE) install

status:
	@echo "=== LaunchAgent Status ==="
	@launchctl print gui/$(UID)/$(LABEL) 2>/dev/null | head -20 || echo "NOT LOADED"
	@echo ""
	@echo "=== Process ==="
	@pgrep -fl 'wacli sync' || echo "NOT RUNNING"
	@echo ""
	@echo "=== Drift Detection ==="
	@diff -q $(PLIST_SRC) $(PLIST_DST) 2>/dev/null && echo "plist: OK" || echo "plist: DRIFT (repo != installed)"
	@diff -q $(SCRIPT_SRC) $(SCRIPT_DST) 2>/dev/null && echo "script: OK" || echo "script: DRIFT (repo != installed)"
	@diff -q $(HEALTH_SCRIPT_SRC) $(HEALTH_SCRIPT_DST) 2>/dev/null && echo "health script: OK" || echo "health script: DRIFT (repo != installed)"
	@diff -q $(HEALTH_PLIST_SRC) $(HEALTH_PLIST_DST) 2>/dev/null && echo "health plist: OK" || echo "health plist: DRIFT (repo != installed)"
	@echo ""
	@echo "=== Last Log Lines ==="
	@tail -5 $(LOG_FILE) 2>/dev/null || echo "No logs yet"

logs:
	tail -f $(LOG_FILE)

build:
	@echo "==> Building wacli fork..."
	cd .. && CGO_ENABLED=1 go build -tags sqlite_fts5 -o $(HOME)/bin/wacli ./cmd/wacli
	@echo "==> Running tests..."
	cd .. && go test -tags sqlite_fts5 ./...
	@echo "==> Done. Binary: ~/bin/wacli"

# === Health monitor (Mac Mini) ===
#
# "Draait het proces?" is geen health signal — dat bleek op 2026-06-17, toen de
# daemon 68 dagen draaide zonder ook maar één bericht te syncen. Deze agent
# controleert berichtversheid, koppelstatus en [FATAL] regels.

HEALTH_PLIST_SRC = launchagents/$(LABEL_HEALTH).plist
HEALTH_PLIST_DST = $(HOME)/Library/LaunchAgents/$(LABEL_HEALTH).plist
HEALTH_SCRIPT_SRC = scripts/wacli-health.sh
HEALTH_SCRIPT_DST = $(HOME)/bin/wacli-health.sh

install-health: $(LOG_DIR)
	@echo "==> Installing wacli-health script..."
	cp $(HEALTH_SCRIPT_SRC) $(HEALTH_SCRIPT_DST)
	chmod +x $(HEALTH_SCRIPT_DST)
	@echo "==> Installing health LaunchAgent..."
	cp $(HEALTH_PLIST_SRC) $(HEALTH_PLIST_DST)
	-launchctl bootout gui/$(UID)/$(LABEL_HEALTH) 2>/dev/null
	launchctl bootstrap gui/$(UID) $(HEALTH_PLIST_DST)
	@echo "==> Done. Health check draait elke 30 min."

uninstall-health:
	@echo "==> Stopping health monitor..."
	-launchctl bootout gui/$(UID)/$(LABEL_HEALTH) 2>/dev/null
	-rm -f $(HEALTH_PLIST_DST)
	-rm -f $(HEALTH_SCRIPT_DST)

health:
	@$(HEALTH_SCRIPT_DST) || true
	@echo "=== Laatste health regels ==="
	@tail -10 $(HOME)/Library/Logs/Claude/wacli-health.log 2>/dev/null || echo "geen log"

# === DB Sync (MacBook) ===

SYNC_PLIST_SRC = launchagents/$(LABEL_SYNC).plist
SYNC_PLIST_DST = $(HOME)/Library/LaunchAgents/$(LABEL_SYNC).plist
SYNC_SCRIPT_SRC = scripts/wacli-db-sync.sh
SYNC_SCRIPT_DST = $(HOME)/bin/wacli-db-sync.sh

install-sync: $(LOG_DIR)
	@echo "==> Installing wacli-db-sync script..."
	cp $(SYNC_SCRIPT_SRC) $(SYNC_SCRIPT_DST)
	chmod +x $(SYNC_SCRIPT_DST)
	@echo "==> Installing DB sync LaunchAgent..."
	cp $(SYNC_PLIST_SRC) $(SYNC_PLIST_DST)
	launchctl bootstrap gui/$(UID) $(SYNC_PLIST_DST)
	@echo "==> Done. DB sync active (every 30s)."

uninstall-sync:
	@echo "==> Stopping DB sync..."
	-launchctl bootout gui/$(UID)/$(LABEL_SYNC) 2>/dev/null
	-rm -f $(SYNC_PLIST_DST)
	-rm -f $(SYNC_SCRIPT_DST)
	@echo "==> Done."

# === IPC Socket Tunnel (MacBook) ===

TUNNEL_PLIST_SRC = launchagents/$(LABEL_TUNNEL).plist
TUNNEL_PLIST_DST = $(HOME)/Library/LaunchAgents/$(LABEL_TUNNEL).plist
TUNNEL_SCRIPT_SRC = scripts/wacli-tunnel.sh
TUNNEL_SCRIPT_DST = $(HOME)/bin/wacli-tunnel.sh

install-tunnel: $(LOG_DIR)
	@echo "==> Installing wacli-tunnel script..."
	cp $(TUNNEL_SCRIPT_SRC) $(TUNNEL_SCRIPT_DST)
	chmod +x $(TUNNEL_SCRIPT_DST)
	@echo "==> Installing tunnel LaunchAgent..."
	cp $(TUNNEL_PLIST_SRC) $(TUNNEL_PLIST_DST)
	launchctl bootstrap gui/$(UID) $(TUNNEL_PLIST_DST)
	@echo "==> Done. SSH socket tunnel active."

uninstall-tunnel:
	@echo "==> Stopping tunnel..."
	-launchctl bootout gui/$(UID)/$(LABEL_TUNNEL) 2>/dev/null
	-rm -f $(TUNNEL_PLIST_DST)
	-rm -f $(TUNNEL_SCRIPT_DST)
	-rm -f $(HOME)/.wacli/wacli.sock
	@echo "==> Done."

# === Mode Toggle (MacBook) ===

MODE_SCRIPT_SRC = scripts/wacli-mode.sh
MODE_SCRIPT_DST = $(HOME)/bin/wacli-mode.sh

install-mode: $(LOG_DIR)
	@echo "==> Installing mode toggle script..."
	cp $(MODE_SCRIPT_SRC) $(MODE_SCRIPT_DST)
	chmod +x $(MODE_SCRIPT_DST)
	@echo "==> Done. Use: wacli-mode.sh {primary|fallback|status}"

mode-primary:
	$(MODE_SCRIPT_DST) primary

mode-fallback:
	$(MODE_SCRIPT_DST) fallback

mode-status:
	$(MODE_SCRIPT_DST) status

# === MacBook Full Setup (sync + tunnel + mode) ===

install-macbook: install-sync install-tunnel install-mode
	@echo "==> MacBook setup complete (sync + tunnel + mode toggle)."

uninstall-macbook: uninstall-sync uninstall-tunnel
	-rm -f $(MODE_SCRIPT_DST)
	@echo "==> MacBook cleanup complete."
