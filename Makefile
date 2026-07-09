# vnprox Makefile — contract defined in docs/development.md.
#
# Targets whose subjects don't exist yet (frontend, mock PVE server, deb
# packaging) succeed as no-ops with a clear notice, per T-001. As later
# tasks (T-002, T-004, T-005, T-006) land real code, the gating checks
# below flip on automatically — no Makefile changes required.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

GO          ?= go
BIN_DIR     := bin
DIST_DIR    := dist
WEB_DIR     := web
PACKAGING_DIR := packaging

GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION   := v1.5.0

.PHONY: build dev test lint check deb mockpve

# --- readiness gates -----------------------------------------------------
# Each *_READY variable is non-empty once the task that owns that piece has
# landed real code, at which point the corresponding target does real work
# instead of printing a "not yet implemented" notice.

WEB_READY     := $(shell test -f $(WEB_DIR)/package.json && echo yes)
DAEMON_READY  := $(shell find internal/api -maxdepth 1 -name '*.go' ! -name 'doc.go' 2>/dev/null | head -1)
MOCKPVE_READY := $(shell find internal/pvemock -maxdepth 1 -name '*.go' ! -name 'doc.go' 2>/dev/null | head -1)
DEB_READY     := $(shell find $(PACKAGING_DIR) -maxdepth 2 \( -name 'nfpm.yaml' -o -name '*.control' -o -name 'debian' \) 2>/dev/null | head -1)

# --- build ---------------------------------------------------------------

build: ## vnproxd binary with embedded SPA (runs web build first)
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: building SPA"; \
		cd $(WEB_DIR) && npm ci && npm run build; \
	else \
		echo ">> web: not yet implemented (T-005), skipping web build"; \
	fi
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/vnproxd ./cmd/vnproxd
	@echo ">> built $(BIN_DIR)/vnproxd"

# --- dev -------------------------------------------------------------------

dev: ## backend against pvemock + Vite dev server, hot reload
	@if [ -n "$(WEB_READY)" ] && [ -n "$(DAEMON_READY)" ] && [ -n "$(MOCKPVE_READY)" ]; then \
		echo ">> dev: starting mock PVE, vnproxd, and the Vite dev server"; \
		trap 'kill 0' EXIT; \
		( $(GO) run ./cmd/vnproxd --config testdata/dev.toml & ) ; \
		( cd $(WEB_DIR) && npm run dev ) ; \
	else \
		echo ">> dev: not yet implemented, skipping"; \
		echo "   waiting on: cmd/vnproxd+internal/api daemon (T-002), internal/pvemock server (T-004), web/package.json (T-005)"; \
	fi

# --- test --------------------------------------------------------------

test: ## go test ./... && vitest run
	$(GO) test ./...
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: running vitest"; \
		cd $(WEB_DIR) && npm run test; \
	else \
		echo ">> web: not yet implemented (T-005), skipping vitest"; \
	fi

# --- lint ----------------------------------------------------------------

lint: ## golangci-lint + eslint + tsc --noEmit
	@echo ">> go: gofmt check"
	@fmtout="$$(gofmt -l $$(find . -name '*.go' -not -path './web/*'))"; \
	if [ -n "$$fmtout" ]; then \
		echo "the following files are not gofmt-formatted:"; \
		echo "$$fmtout"; \
		exit 1; \
	fi
	@echo ">> go: go vet ./..."
	$(GO) vet ./...
	@echo ">> go: golangci-lint run"
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		$(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...; \
	fi
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: eslint"; \
		(cd $(WEB_DIR) && npx eslint .); \
		echo ">> web: tsc --noEmit"; \
		(cd $(WEB_DIR) && npx tsc --noEmit); \
	else \
		echo ">> web: not yet implemented (T-005), skipping eslint/tsc --noEmit"; \
	fi

# --- check ---------------------------------------------------------------

check: lint test ## lint + test + govulncheck + npm audit --audit-level=high
	@echo ">> go: govulncheck ./..."
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...; \
	fi
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: npm audit --audit-level=high"; \
		cd $(WEB_DIR) && npm audit --audit-level=high; \
	else \
		echo ">> web: not yet implemented (T-005), skipping npm audit"; \
	fi

# --- deb -------------------------------------------------------------------

deb: ## build the .deb into dist/
	@if [ -n "$(DEB_READY)" ]; then \
		mkdir -p $(DIST_DIR); \
		$(MAKE) -C $(PACKAGING_DIR) deb DIST_DIR=$(abspath $(DIST_DIR)); \
	else \
		echo ">> packaging: not yet implemented (T-006), skipping deb build"; \
	fi

# --- mockpve ---------------------------------------------------------------

mockpve: ## run the mock PVE server standalone on :8006
	@if [ -n "$(MOCKPVE_READY)" ]; then \
		echo ">> internal/pvemock: implementation present but this target is a T-001"; \
		echo "   placeholder — T-004 owns wiring the real entry point here."; \
	else \
		echo ">> internal/pvemock: not yet implemented (T-004), skipping"; \
	fi
