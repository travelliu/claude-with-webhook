VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X main.version=$(VERSION) -X main.buildTime=$(BUILD)
BIN_DIR  := $(HOME)/.local/bin
WORK_DIR := $(HOME)/.claude-webhook

.PHONY: build install uninstall clean restart

build:
	go build -ldflags "$(LDFLAGS)" -o claude-webhook-server .

install: build
	@mkdir -p $(BIN_DIR)
	@cp claude-webhook-server $(BIN_DIR)/claude-webhook-server
	@chmod +x $(BIN_DIR)/claude-webhook-server
	@mkdir -p $(WORK_DIR)
	@echo "$(CURDIR)" > $(WORK_DIR)/source-repo
	@# Generate .env if missing
	@if [ ! -f $(WORK_DIR)/.env ]; then \
		SECRET=$$(openssl rand -hex 20); \
		USER=$$(gh api user --jq '.login'); \
		printf 'GITHUB_WEBHOOK_SECRET=%s\nALLOWED_USERS=%s\nPORT=8080\n' "$$SECRET" "$$USER" > $(WORK_DIR)/.env; \
		echo "Generated .env (user=$$USER)"; \
	fi
	@echo
	@echo "Installed $(VERSION)"
	@echo
	@echo "  Binary:    $(BIN_DIR)/claude-webhook-server"
	@echo "  Work dir:  $(WORK_DIR)/"
	@echo
	@echo "  Commands:"
	@echo "    claude-webhook-server bot add"
	@echo "    claude-webhook-server register"
	@echo "    claude-webhook-server start"
	@echo "    claude-webhook-server status"
	@echo

restart: install
	@claude-webhook-server stop 2>/dev/null || true
	@claude-webhook-server start
	@echo "Server restarted."

uninstall:
	@claude-webhook-server stop 2>/dev/null || true
	@rm -f $(BIN_DIR)/claude-webhook-server
	@echo "Removed $(BIN_DIR)/claude-webhook-server"
	@echo "Work dir $(WORK_DIR)/ preserved (contains your config)"

clean:
	@rm -f claude-webhook-server
