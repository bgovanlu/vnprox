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

FUZZTIME ?= 60s
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION   := v1.5.0

.PHONY: build dev test lint check deb mockpve openapi soak

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
		echo ">> dev: starting mock PVE ($(MOCKPVE_FIXTURE)), vnproxd, and the Vite dev server"; \
		trap 'kill 0' EXIT; \
		( $(GO) run ./cmd/pvemock --addr $(MOCKPVE_ADDR) --fixture $(MOCKPVE_FIXTURE) & ) ; \
		( $(GO) run ./cmd/vnproxd --config testdata/dev.toml & ) ; \
		( cd $(WEB_DIR) && npm run dev ) ; \
	else \
		echo ">> dev: not yet implemented, skipping"; \
		echo "   waiting on: cmd/vnproxd+internal/api daemon (T-002), internal/pvemock server (T-004), web/package.json (T-005)"; \
	fi

# --- test --------------------------------------------------------------

# T-1004: internal/flow/hostsample/ebpf.go (the real eBPF kernel-feature
# probe + sampler) is gated behind the "ebpf" Go build tag and is
# deliberately EXCLUDED from this default `go test ./...` matrix — no CI
# environment is assumed to support real eBPF program attachment
# (docs/development.md, internal/flow/hostsample's package doc comment).
# Without -tags ebpf, ebpf_stub.go's build-tag-complementary EBPFSampler
# compiles instead, whose Probe always fails "not compiled into this
# binary" — that negative path (AC3) is exactly what this default matrix
# exercises. To build/test the real probe on a Linux dev host, run
# `go test -tags ebpf ./internal/flow/hostsample/...` explicitly; it is
# not part of `make build`/`make test`/`make check`.
test: ## go test ./... && vitest run
	$(GO) test ./...
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: running vitest"; \
		cd $(WEB_DIR) && npm run test; \
	else \
		echo ">> web: not yet implemented (T-005), skipping vitest"; \
	fi

# --- soak ------------------------------------------------------------------

# T-2504: the resource-leak gate. Runs the real daemon against pvemock under
# seeded synthetic churn, samples goroutines/heap/RSS/open fds/every table's
# row count, and fails on a positive TREND over the second half of the run
# rather than on any absolute threshold. Artifacts (samples.csv +
# report.json, the latter carrying the seed) land in SOAK_ARTIFACTS.
#
# SOAK_DURATION defaults to a short run on purpose: `make soak` has to be
# usable as a "does this still work" check. The nightly run is
# `make soak SOAK_DURATION=8h`; a longer local run is `SOAK_DURATION=30m`.
#
# LEAK selects one of the deliberate leak fixtures (cmd/vnproxd/soakleak.go),
# which are compiled in ONLY under the `soakleak` build tag this target adds
# for them — no shipped build contains them. goroutine/table must FAIL the
# gate; flat must PASS it.
SOAK_DURATION  ?= 3m
SOAK_INTERVAL  ?= 5s
SOAK_CHURN     ?= 2s
SOAK_SEED      ?= 0
SOAK_FIXTURE   ?= three-node-vlan.yaml
SOAK_ARTIFACTS ?=
LEAK           ?=
# `go test -timeout 0` = no limit. The run is already bounded by
# SOAK_DURATION plus a bounded shutdown, and an 8-hour nightly would
# otherwise need this raised in lockstep with SOAK_DURATION every time.
SOAK_TIMEOUT   ?= 0

soak: ## resource-leak gate: real daemon + pvemock under seeded churn, fails on trend (LEAK=goroutine|table|flat)
	@echo ">> soak: $(SOAK_DURATION) run, sampling every $(SOAK_INTERVAL), churn every $(SOAK_CHURN), fixture $(SOAK_FIXTURE)"
	@if [ -n "$(LEAK)" ]; then echo ">> soak: LEAK FIXTURE '$(LEAK)' ACTIVE (build tag: soakleak) — goroutine/table must FAIL, flat must PASS"; fi
	VNPROX_SOAK_LEAK=$(LEAK) $(GO) test ./cmd/vnproxd/ \
		$(if $(LEAK),-tags soakleak,) \
		-run '^TestSoak$$' -count=1 -v \
		-timeout $(SOAK_TIMEOUT) \
		-soak.duration=$(SOAK_DURATION) \
		-soak.interval=$(SOAK_INTERVAL) \
		-soak.churn-interval=$(SOAK_CHURN) \
		-soak.seed=$(SOAK_SEED) \
		-soak.fixture=$(SOAK_FIXTURE) \
		-soak.artifacts=$(SOAK_ARTIFACTS)

# --- openapi -------------------------------------------------------------

openapi: ## regenerate docs/openapi.json from the daemon's registered routes
	@echo ">> openapi: bringing the daemon up and reading its generated document"
	$(GO) test ./cmd/vnproxd/ -run TestOpenAPI_MatchesTheCommittedDocument -update -count=1
	@echo ">> openapi: docs/openapi.json is up to date"

# --- lint ----------------------------------------------------------------

lint: ## golangci-lint + eslint + tsc --noEmit
	@echo ">> go: gofmt check"
	@fmtout="$$(gofmt -l $$(find . -name '*.go' -not -path './web/*' -not -path './.claude/*'))"; \
	if [ -n "$$fmtout" ]; then \
		echo "the following files are not gofmt-formatted:"; \
		echo "$$fmtout"; \
		exit 1; \
	fi
	@echo ">> go: go vet ./..."
	$(GO) vet ./...
	@echo ">> go: golangci-lint run"
	@# --allow-serial-runners (T-1806): golangci-lint acquires a file lock on
	@# start and, by default, exits with a hard error ("parallel golangci-lint
	@# is running") if another instance holds it, rather than waiting. This
	@# repo's own orchestration convention (planning/implementation-plan-
	@# proven.md) runs concurrent tasks in separate git worktrees, each with
	@# its own `make check` — a legitimate, sanctioned way to collide with
	@# this lock, not a bug in the task under test. Serializing around the
	@# lock instead of erroring turns that collision into "wait your turn"
	@# instead of a spurious CI/local failure.
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --allow-serial-runners ./...; \
	else \
		$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --allow-serial-runners ./...; \
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

check: lint test ## lint + test + govulncheck + npm audit (gated by web/audit-allowlist.json)
	@echo ">> go: govulncheck ./..."
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...; \
	fi
	@if [ -n "$(WEB_READY)" ]; then \
		echo ">> web: npm audit (gated by web/audit-allowlist.json — see docs/development.md)"; \
		(cd $(WEB_DIR) && node scripts/check-audit-allowlist.mjs); \
	else \
		echo ">> web: not yet implemented (T-005), skipping npm audit"; \
	fi

# --- deb -------------------------------------------------------------------

deb: ## build the .deb into dist/ (builds the frontend first — see below)
	@if [ -n "$(DEB_READY)" ]; then \
		if [ -n "$(WEB_READY)" ]; then \
			echo ">> web: building SPA"; \
			(cd $(WEB_DIR) && npm ci && npm run build); \
		else \
			echo ">> web: not yet implemented (T-005), skipping web build"; \
		fi; \
		mkdir -p $(DIST_DIR); \
		$(MAKE) -C $(PACKAGING_DIR) deb DIST_DIR=$(abspath $(DIST_DIR)); \
	else \
		echo ">> packaging: not yet implemented (T-006), skipping deb build"; \
	fi

# --- mockpve ---------------------------------------------------------------

MOCKPVE_ADDR    ?= :8006
MOCKPVE_FIXTURE ?= testdata/clusters/single-node.yaml

ci: ## everything GitHub Actions would run, locally (check + cross-arm64 + fuzz + package)
	@echo ">> ci: this is the CI-equivalent gate. GitHub Actions is currently"
	@echo ">> ci: unfunded for this repository, so THIS is the gate that matters."
	@echo ">> ci: [1/4] make check"
	@$(MAKE) check
	@echo ">> ci: [2/4] cross-compile for linux/arm64 (build-only, matches the cross-arm64 job)"
	GOOS=linux GOARCH=arm64 $(GO) build ./...
	@echo ">> ci: [3/4] fuzz every untrusted-input parser ($(FUZZTIME) each, matches the fuzz job)"
	$(GO) test -run='^$$' -fuzz='^FuzzParse$$'           -fuzztime=$(FUZZTIME) ./internal/host/
	$(GO) test -run='^$$' -fuzz='^FuzzPeerAuth$$'        -fuzztime=$(FUZZTIME) ./internal/peer/
	$(GO) test -run='^$$' -fuzz='^FuzzParseBGPSummary$$' -fuzztime=$(FUZZTIME) ./internal/host/
	$(GO) test -run='^$$' -fuzz='^FuzzParseEVPNVNI$$'    -fuzztime=$(FUZZTIME) ./internal/host/
	$(GO) test -run='^$$' -fuzz='^FuzzParseLLDP$$'       -fuzztime=$(FUZZTIME) ./internal/host/
	$(GO) test -run='^$$' -fuzz='^FuzzParseDHCPLeases$$' -fuzztime=$(FUZZTIME) ./internal/host/
	$(GO) test -run='^$$' -fuzz='^FuzzParseAll$$'        -fuzztime=$(FUZZTIME) ./internal/fwlog/
	@echo ">> ci: [4/4] package"
	@$(MAKE) deb
	@echo ">> ci: PASSED. Note: make e2e is NOT included — the Playwright"
	@echo ">> ci: suite is currently red (29 failed / 59 passed, see T-2108)."

e2e: ## Playwright end-to-end suite against pvemock + vnproxd + the production SPA
	@echo ">> e2e: building the SPA (vnproxd embeds web/dist) and running Playwright"
	cd $(WEB_DIR) && npm run e2e

ports: ## show every port this repo's tooling binds, and what is holding them now
	@. packaging/test/lib/ports.sh && ports_report

mockpve: ## run the mock PVE server standalone on :8006
	@if [ -n "$(MOCKPVE_READY)" ]; then \
		echo ">> internal/pvemock: starting mock PVE server on $(MOCKPVE_ADDR) (fixture: $(MOCKPVE_FIXTURE))"; \
		$(GO) run ./cmd/pvemock --addr $(MOCKPVE_ADDR) --fixture $(MOCKPVE_FIXTURE); \
	else \
		echo ">> internal/pvemock: not yet implemented (T-004), skipping"; \
	fi
