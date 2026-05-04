# one-llm-router — top-level build orchestration.
#
# Conventions:
#
#   - `make build` — produce a release-grade `./one-llm-router` binary that has
#     the frontend baked in. Matches the 002 single-port model: the Go
#     binary serves both `/v1/*`, `/api/*`, and the SPA under `/admin/*`
#     and `/setup/*` from `frontend/dist/` embedded at compile time.
#
#   - `make test` / `make test-race` — all Go tests, with and without
#     the race detector.
#
#   - `make lint` / `make lint-fix` — `golangci-lint` for Go and
#     `biome check` for the frontend. Both gates MUST be green before a
#     release tag.
#
#   - `make fe-install` / `make fe-build` / `make fe-test` /
#     `make fe-typecheck` / `make fe-lint` — individual frontend
#     targets. Helpful when iterating on the SPA without touching Go.
#
# Tool versions are pinned in AGENTS.md (Go 1.25, Node 22.22.0 via
# .node-version, pnpm 10.30.3 via frontend/package.json).
.DEFAULT_GOAL := help

SHELL := /bin/bash
ROOT := $(shell pwd)
OPENAI_PYTHON_VERSION ?= 2.32.0

# Linker vars — override on the command line or via CI to stamp a real
# version. Keep the var names aligned with internal/app/buildinfo so
# the `-version` flag reports the same string as GET /api/admin/settings.
VERSION ?= dev
GIT_SHA ?= $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)
BUILT_AT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILDINFO_PKG := github.com/user/one-llm-router/internal/app/buildinfo
LDFLAGS := \
  -X $(BUILDINFO_PKG).Version=$(VERSION) \
  -X $(BUILDINFO_PKG).GitSHA=$(GIT_SHA) \
  -X $(BUILDINFO_PKG).BuiltAt=$(BUILT_AT)

.PHONY: help
help: ## List the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ---------- aggregate targets ----------

.PHONY: build
build: fe-install fe-build go-build ## Full release build (frontend + Go)

.PHONY: spa-stage
spa-stage: ## Copy frontend/dist → internal/spa/dist for go:embed
	@mkdir -p internal/spa/dist
	@find internal/spa/dist -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	@cp -R frontend/dist/. internal/spa/dist/
	@touch internal/spa/dist/.gitkeep
	@echo 'staged frontend/dist → internal/spa/dist (.gitkeep preserved)'

.PHONY: test
test: go-test fe-test ## Run every test suite

.PHONY: test-race
test-race: go-test-race fe-test ## Run Go tests under -race + frontend tests

.PHONY: lint
lint: go-lint fe-lint ## Lint Go + frontend code

.PHONY: lint-fix
lint-fix: go-fmt fe-lint-fix ## Auto-fix lint/format issues where safe

.PHONY: clean
clean: ## Remove build artefacts
	rm -f ./one-llm-router
	rm -rf frontend/dist frontend/node_modules/.cache

# ---------- Go targets ----------

.PHONY: go-build
go-build: spa-stage ## Build the Go binary with linker-stamped buildinfo + staged SPA assets
	go build -ldflags "$(LDFLAGS)" -o one-llm-router ./cmd/one-llm-router

.PHONY: go-test
go-test: ## Run the Go test suite
	go test ./...

.PHONY: go-test-race
go-test-race: ## Run the Go test suite with -race
	go test -race ./...

.PHONY: go-lint
go-lint: ## Run golangci-lint (falls back to go vet when missing)
	@if command -v golangci-lint >/dev/null 2>&1; then \
	  golangci-lint run; \
	else \
	  echo "golangci-lint not installed — falling back to go vet"; \
	  go vet ./...; \
	fi

.PHONY: go-fmt
go-fmt: ## Run gofmt on every Go file
	gofmt -w .

.PHONY: codegen-go
codegen-go: ## Regenerate internal/generated/adminapi/*.gen.go from openapi/admin.yaml
	bash scripts/codegen-go.sh

.PHONY: codegen-frontend
codegen-frontend: ## Regenerate frontend/src/generated/openapi/ from openapi/admin.yaml
	bash scripts/codegen-frontend.sh

.PHONY: codegen
codegen: codegen-go codegen-frontend ## Regenerate BOTH Go + TS admin-API artefacts from openapi/admin.yaml

.PHONY: check-openapi-freshness
check-openapi-freshness: ## Lint openapi/admin.yaml + regen + fail if generated trees drift (matches T-008 CI gate)
	bash scripts/check-openapi-freshness.sh

.PHONY: migration-rollback-test
migration-rollback-test: ## Run the 003 forward->rollback->forward migration invariance gate
	bash scripts/migration-rollback-test.sh

.PHONY: log-scrub
log-scrub: ## Scan test-output/ and Playwright artifacts for leaked token-like material
	mkdir -p test-output/ frontend/test-results/
	bash scripts/log-scrub.sh test-output/ frontend/test-results/

.PHONY: envelope-parity
envelope-parity: ## Run the per-operation admin envelope parity gate
	bash scripts/envelope-parity.sh

.PHONY: coverage-floor
coverage-floor: ## Enforce the 003 package coverage floor (>=80%)
	bash scripts/coverage-floor.sh

# ---------- frontend targets ----------

.PHONY: fe-install
fe-install: ## Install frontend dependencies (offline-friendly)
	pnpm -C frontend install --prefer-offline --frozen-lockfile

.PHONY: fe-build
fe-build: ## Build the frontend SPA into frontend/dist/
	pnpm -C frontend build

.PHONY: fe-test
fe-test: ## Run frontend unit tests (vitest)
	pnpm -C frontend test -- --run

.PHONY: fe-typecheck
fe-typecheck: ## Type-check the frontend
	pnpm -C frontend typecheck

.PHONY: fe-lint
fe-lint: ## Lint the frontend (biome)
	pnpm -C frontend lint

.PHONY: fe-lint-fix
fe-lint-fix: ## Auto-fix frontend lint/format issues
	pnpm -C frontend lint:fix

# ---------- E2E targets ----------

.PHONY: e2e-serve
e2e-serve: build ## Start the router for manual Playwright debugging (temp blank install)
	@tmpdir="$$(mktemp -d "$${TMPDIR:-/tmp}/one-llm-router-e2e-serve.XXXXXX")"; \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	echo "E2E runtime: $$tmpdir"; \
	unset ROUTER_ADMIN_AUTH_ENABLED ROUTER_CLIENT_KEYS_ENABLED \
	  ROUTER_CONFIG_PATH ROUTER_DB_DRIVER ROUTER_DB_URL \
	  ROUTER_LOG_LEVEL ROUTER_LOG_CLIENT_REQUEST_BODY \
	  ROUTER_LOG_UPSTREAM_REQUEST_BODY ROUTER_LOG_UPSTREAM_RESPONSE_BODY \
	  ROUTER_LOG_RETENTION_DAYS; \
	cd "$$tmpdir"; \
	"$(ROOT)/one-llm-router" -config ./config.json -listen localhost:18080

.PHONY: e2e
e2e: build ## Run Playwright E2E suite (fixture spawns a fresh ./one-llm-router-e2e per test)
	pnpm -C frontend test:e2e

.PHONY: data-plane-compat
data-plane-compat: build ## Run local mock-upstream data-plane compatibility E2E
	pnpm -C frontend test:e2e:compat

.PHONY: check-live-upstream-env
check-live-upstream-env: ## Fail fast when live upstream smoke env is incomplete
	@test "$$LIVE_UPSTREAM_SMOKE" = "1" || \
	  (echo "LIVE_UPSTREAM_SMOKE=1 is required"; exit 1)
	@test -n "$$LIVE_OPENAI_API_KEY" || \
	  (echo "LIVE_OPENAI_API_KEY is required"; exit 1)
	@test -n "$$LIVE_OPENAI_BASE_URL" || \
	  (echo "LIVE_OPENAI_BASE_URL is required"; exit 1)
	@test -n "$$LIVE_OPENAI_MODEL" || \
	  (echo "LIVE_OPENAI_MODEL is required"; exit 1)

.PHONY: live-upstream-compat
live-upstream-compat: check-live-upstream-env ## Opt-in live upstream compatibility smoke against a real API-key account
	$(MAKE) build
	pnpm -C frontend test:e2e:live
	OPENAI_PYTHON_VERSION="$(OPENAI_PYTHON_VERSION)" bash scripts/run-openai-sdk-compat-smoke.sh

.PHONY: live-upstream-smoke
live-upstream-smoke: live-upstream-compat ## Backwards-compatible alias for live-upstream-compat

# ---------- smoke ----------

.PHONY: smoke-brownfield
smoke-brownfield: ## Exercise the brownfield auto-materialization path
	bash scripts/smoke-brownfield.sh
