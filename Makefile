.DEFAULT_GOAL := help

# ──────────────────────────────────────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────────────────────────────────────
SECRETS_DIR  := secrets
GO_DIR       := step-ui-go
BACKUP_DIR   := backups
COMPOSE      := docker compose

# ──────────────────────────────────────────────────────────────────────────────
# Help — self-documenting via ## comments
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ──────────────────────────────────────────────────────────────────────────────
# Bootstrap
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: setup
setup: ## Bootstrap a fresh install: copy .env.example and generate secrets/
	@# Copy .env if absent
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "  created  .env (from .env.example — edit HOST_IP, PROVISIONER, TZ)"; \
	else \
		echo "  skipped  .env already exists"; \
	fi
	@# Create secrets directory
	@mkdir -p $(SECRETS_DIR)
	@chmod 700 $(SECRETS_DIR)
	@# Generate postgres_password
	@if [ ! -f $(SECRETS_DIR)/postgres_password ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > $(SECRETS_DIR)/postgres_password; \
		chmod 600 $(SECRETS_DIR)/postgres_password; \
		echo "  created  $(SECRETS_DIR)/postgres_password"; \
	else \
		echo "  skipped  $(SECRETS_DIR)/postgres_password already exists (FORCE=1 to regenerate)"; \
	fi
	@# Generate secret_key
	@if [ ! -f $(SECRETS_DIR)/secret_key ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48 > $(SECRETS_DIR)/secret_key; \
		chmod 600 $(SECRETS_DIR)/secret_key; \
		echo "  created  $(SECRETS_DIR)/secret_key"; \
	else \
		echo "  skipped  $(SECRETS_DIR)/secret_key already exists (FORCE=1 to regenerate)"; \
	fi
	@# Generate ca_password
	@if [ ! -f $(SECRETS_DIR)/ca_password ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > $(SECRETS_DIR)/ca_password; \
		chmod 600 $(SECRETS_DIR)/ca_password; \
		echo "  created  $(SECRETS_DIR)/ca_password"; \
	else \
		echo "  skipped  $(SECRETS_DIR)/ca_password already exists (FORCE=1 to regenerate)"; \
	fi
	@echo ""
	@echo "Next steps:"
	@echo "  1. Edit .env — set HOST_IP, UI_HTTPS_PORT, PROVISIONER, TZ"
	@echo "  2. make up"
	@echo ""
	@echo "To regenerate secrets (existing deployment): make setup FORCE=1"

# ──────────────────────────────────────────────────────────────────────────────
# Docker Compose lifecycle
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: up
up: ## Build images and start all services in detached mode
	$(COMPOSE) up -d --build

.PHONY: down
down: ## Stop and remove containers (volumes are preserved)
	$(COMPOSE) down

.PHONY: restart
restart: down up ## Stop then start all services

.PHONY: logs
logs: ## Stream logs from all services (Ctrl-C to stop)
	$(COMPOSE) logs -f

.PHONY: ps
ps: ## Show container status
	$(COMPOSE) ps

.PHONY: update
update: ## Pull latest images and rebuild
	$(COMPOSE) pull
	$(COMPOSE) up -d --build

# ──────────────────────────────────────────────────────────────────────────────
# Backup
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: backup
backup: ## Dump the database and named volumes into backups/<timestamp>/
	@TS=$$(date +%Y%m%d_%H%M%S); \
	DIR=$(BACKUP_DIR)/$$TS; \
	mkdir -p $$DIR/volumes; \
	echo "  backup dir: $$DIR"; \
	\
	echo "  dumping PostgreSQL …"; \
	$(COMPOSE) exec -T postgres \
		pg_dump -U stepui stepui > $$DIR/postgres-stepui.sql || true; \
	\
	echo "  archiving named volumes …"; \
	for VOL in postgres-data step-ca-data step-ui-certs step-ui-ssl step-ui-data step-ui-uploads; do \
		MOUNT=$$(docker volume inspect step-ca-ui_$$VOL \
			--format '{{.Mountpoint}}' 2>/dev/null || true); \
		if [ -n "$$MOUNT" ] && [ -d "$$MOUNT" ]; then \
			tar -czf $$DIR/volumes/$$VOL.tgz -C $$MOUNT . && \
			echo "    $$VOL.tgz"; \
		fi; \
	done; \
	\
	echo "  writing manifest …"; \
	( \
		printf '{\n'; \
		printf '  "format": "step-ca-ui-makefile-backup-v1",\n'; \
		printf '  "created_at": "%s",\n' "$$(date -Is)"; \
		printf '  "components": [\n'; \
		FIRST=1; \
		for F in $$(find $$DIR -type f ! -name manifest.json | sort); do \
			REL=$${F#$$DIR/}; \
			SIZE=$$(wc -c < $$F | tr -d ' '); \
			SUM=$$(sha256sum $$F | awk '{print $$1}'); \
			if [ "$$FIRST" = "0" ]; then printf ',\n'; fi; \
			FIRST=0; \
			printf '    {"path": "%s", "size": %s, "sha256": "%s"}' "$$REL" "$$SIZE" "$$SUM"; \
		done; \
		printf '\n  ]\n}\n'; \
	) > $$DIR/manifest.json; \
	\
	echo "  done: $$DIR"

# ──────────────────────────────────────────────────────────────────────────────
# Go development
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: test
test: ## Run Go tests with race detector
	cd $(GO_DIR) && go test -race ./...

.PHONY: build
build: ## Build the Go binary
	cd $(GO_DIR) && go build ./...

.PHONY: fmt
fmt: ## Format Go source with gofumpt
	cd $(GO_DIR) && gofumpt -w .

.PHONY: lint
lint: ## Run golangci-lint and check formatting
	cd $(GO_DIR) && golangci-lint run
	cd $(GO_DIR) && gofumpt -l .

.PHONY: cover
cover: ## Run coverage gate
	bash scripts/coverage-gate.sh

# ──────────────────────────────────────────────────────────────────────────────
# Cleanup
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: clean
clean: ## Remove build artifacts and old backups (secrets/ and .env are untouched)
	cd $(GO_DIR) && go clean ./...
	@if [ -d $(BACKUP_DIR) ]; then \
		echo "  removing $(BACKUP_DIR)/"; \
		rm -rf $(BACKUP_DIR); \
	fi
