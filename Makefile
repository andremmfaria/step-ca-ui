.DEFAULT_GOAL := help

# ──────────────────────────────────────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────────────────────────────────────
SECRETS_DIR  := secrets
GO_DIR       := backend
BACKUP_DIR   := backups
COMPOSE      := docker compose

# ──────────────────────────────────────────────────────────────────────────────
# Help — self-documenting via ## comments
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

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
	@# Secret files are 644, not 600: plain (non-Swarm) `docker compose`
	@# bind-mounts file-based secrets preserving the host file's own
	@# owner/mode, it does not remap them to root:root/0444 the way Swarm
	@# secrets do. step-ui's container runs as a non-root uid (10001), so a
	@# 600 file owned by the host user is unreadable inside the container;
	@# host-level protection against *other host users* still comes from
	@# $(SECRETS_DIR) itself being 700.
	@# Generate postgres_password
	@if [ ! -f $(SECRETS_DIR)/postgres_password ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > $(SECRETS_DIR)/postgres_password; \
		chmod 644 $(SECRETS_DIR)/postgres_password; \
		echo "  created  $(SECRETS_DIR)/postgres_password"; \
	else \
		echo "  skipped  $(SECRETS_DIR)/postgres_password already exists (FORCE=1 to regenerate)"; \
	fi
	@# Generate secret_key
	@if [ ! -f $(SECRETS_DIR)/secret_key ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48 > $(SECRETS_DIR)/secret_key; \
		chmod 644 $(SECRETS_DIR)/secret_key; \
		echo "  created  $(SECRETS_DIR)/secret_key"; \
	else \
		echo "  skipped  $(SECRETS_DIR)/secret_key already exists (FORCE=1 to regenerate)"; \
	fi
	@# Generate ca_password
	@if [ ! -f $(SECRETS_DIR)/ca_password ] || [ "$(FORCE)" = "1" ]; then \
		openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32 > $(SECRETS_DIR)/ca_password; \
		chmod 644 $(SECRETS_DIR)/ca_password; \
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
	cd $(GO_DIR) && go test -coverprofile=coverage.out ./... >/dev/null
	cd $(GO_DIR) && COVERPROFILE=coverage.out THRESHOLD=15 bash scripts/coverage-gate.sh

.PHONY: openapi
openapi: ## Regenerate backend/openapi/openapi.json from the huma-registered operations
	cd $(GO_DIR) && go run ./cmd/openapi -out openapi/openapi.json

# ──────────────────────────────────────────────────────────────────────────────
# End-to-end suite (plans/e2e-tests.md)
# ──────────────────────────────────────────────────────────────────────────────
E2E_DIR     := test/e2e
E2E_PROJECT := $(if $(COMPOSE_PROJECT_NAME),$(COMPOSE_PROJECT_NAME),$(notdir $(CURDIR)))

.PHONY: e2e-install
e2e-install: ## Install the e2e harness deps (plus chromium for a host-side run)
	cd $(E2E_DIR) && npm ci
	cd $(E2E_DIR) && npx playwright install chromium

.PHONY: e2e-fresh
e2e-fresh: ## Destroy every e2e volume and bring the stack back up healthy
	$(COMPOSE) down -v
	$(COMPOSE) up -d --wait

.PHONY: e2e-main
e2e-main: ## PR-tier suite: api then ui, against the long-lived stack
	cd $(E2E_DIR) && npx playwright test --project=api
	cd $(E2E_DIR) && npx playwright test --project=ui

.PHONY: e2e-quick
e2e-quick: ## Pre-push subset (Section 2.8), api only, ~2 minutes
	cd $(E2E_DIR) && npx playwright test --project=api \
		-g 'E2E-AUTH-01|E2E-AUTH-11|E2E-CSRF-01|E2E-RBAC-01|E2E-RBAC-02|E2E-CERT-01|E2E-CERT-09|E2E-HLTH-02|E2E-ADM-01'

.PHONY: e2e-bootstrap
e2e-bootstrap: ## Run one bootstrap scenario: make e2e-bootstrap SCENARIO=fingerprint
	@test -n "$(SCENARIO)" || { echo "usage: make e2e-bootstrap SCENARIO=<selfsigned|provided|ca-down|fingerprint|fatals>"; exit 2; }
	./$(E2E_DIR)/scenario.sh $(SCENARIO)

# Both rate limiters are process-local maps, so a restart clears them — turning
# a multi-minute real-time wait into ~20-30s back to healthy.
.PHONY: e2e-restart-ui
e2e-restart-ui: ## Restart step-ui, clearing both process-local rate limiters
	$(COMPOSE) restart step-ui
	$(COMPOSE) up -d --wait step-ui

.PHONY: e2e-reset-ssl
e2e-reset-ssl: ## Remove the step-ui-ssl volume only, leaving users and CA state
	$(COMPOSE) stop step-ui
	$(COMPOSE) rm -f step-ui
	docker volume rm $(E2E_PROJECT)_step-ui-ssl
	$(COMPOSE) up -d --wait step-ui

.PHONY: e2e-seed-history
e2e-seed-history: ## Insert N synthetic cert_history rows: make e2e-seed-history N=25
	@test -n "$(N)" || { echo "usage: make e2e-seed-history N=<count>"; exit 2; }
	$(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U stepui -d stepui -c \
		"INSERT INTO cert_history (action, cert_name, domain, details, username, role) \
		 SELECT 'issue', 'e2e-seed-' || g, 'seed' || g || '.e2e.invalid', 'synthetic row seeded by make e2e-seed-history', 'e2e-seed', 'admin' \
		 FROM generate_series(1, $(N)) AS g;"

.PHONY: e2e-le-certs
e2e-le-certs: ## Generate the local ACME server's TLS material for the LE leg
	./$(E2E_DIR)/fixtures/pebble/gen-certs.sh

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
