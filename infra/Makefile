# wacli fork infrastructure
# Source of truth: ~/Projects/wacli/infra/
# Pattern: bjorn-de-borg infra (copy, not symlink)

LABEL = com.productfunction.wacli
UID := $(shell id -u)
PLIST_SRC = launchagents/$(LABEL).plist
PLIST_DST = $(HOME)/Library/LaunchAgents/$(LABEL).plist
SCRIPT_SRC = scripts/wacli-sync.sh
SCRIPT_DST = $(HOME)/bin/wacli-sync.sh
LOG_DIR = $(HOME)/Library/Logs/Whatslack
LOG_FILE = $(LOG_DIR)/wacli-sync.log

.PHONY: install uninstall reinstall status logs build

install: $(LOG_DIR)
	@echo "==> Installing wacli-sync script..."
	cp $(SCRIPT_SRC) $(SCRIPT_DST)
	chmod +x $(SCRIPT_DST)
	@echo "==> Installing LaunchAgent..."
	cp $(PLIST_SRC) $(PLIST_DST)
	launchctl bootstrap gui/$(UID) $(PLIST_DST)
	@echo "==> Done. Check: make status"

$(LOG_DIR):
	mkdir -p $(LOG_DIR)

uninstall:
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
