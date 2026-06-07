# mcp-data-platform Makefile

# Variables
BINARY_NAME := mcp-data-platform
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GO_VERSION := $(shell go version | cut -d ' ' -f 3)
LDFLAGS := -ldflags "-X github.com/txn2/mcp-data-platform/internal/server.Version=$(VERSION)"

# Directories
CMD_DIR := ./cmd/mcp-data-platform
BUILD_DIR := ./build
DIST_DIR := ./dist
UI_DIR := ./ui
UI_EMBED_DIR := ./internal/ui/dist
CV_EMBED_DIR := ./internal/contentviewer/dist

# Tool versions — keep in sync with .github/workflows/ci.yml
GOLANGCI_LINT_VERSION := v2.11.4
GOSEC_VERSION := v2.22.0

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := gofmt
GOLINT := golangci-lint

.PHONY: all build test lint lint-full fmt clean install help docs-serve docs-build verify verify-release \
	tools-check dead-code mutate patch-coverage doc-check swagger swagger-check \
	semgrep codeql sast embed-clean migrate-check \
	frontend-install frontend-build frontend-build-content-viewer \
	frontend-dev frontend-mock frontend-test \
	e2e-up e2e-down e2e-seed e2e-test e2e e2e-logs e2e-clean \
	dev dev-info dev-up dev-down mock-check \
	preview-apps preview-platform-info

## all: Build and test
all: build test lint

## build: Build the binary
build: swagger
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Binary built: $(BUILD_DIR)/$(BINARY_NAME)"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@echo "Tests complete."

## test-short: Run tests without race detection (faster)
test-short:
	@echo "Running tests (short)..."
	$(GOTEST) -v ./...

## test-integration: Run integration tests
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -tags=integration ./...

# Real-Postgres migration gate. Applies every embedded migration + the dev seed
# to a disposable pgvector instance (up -> seed -> down -> up), catching SQL the
# planner only rejects against a live engine (e.g. a non-IMMUTABLE function in an
# index expression), down-migration dependency-order bugs, and dev-seed rot.
# sqlmock and the embedded-file presence checks cannot catch these. Provisions
# its own container on a non-default port so it never touches the dev DB.
MIGRATE_PG_IMAGE := pgvector/pgvector:pg16@sha256:00ba258a66dac104fd5171074a0084462a64a1369d8513f3d0a634e2f24d15bc
MIGRATE_PG_CONTAINER := mcp-migrate-check-pg
MIGRATE_PG_PORT := 55432

## migrate-check: Apply all migrations + seed to a throwaway real Postgres
migrate-check:
	@echo "Running real-Postgres migration gate..."
	@docker rm -f $(MIGRATE_PG_CONTAINER) >/dev/null 2>&1 || true
	@docker run -d --name $(MIGRATE_PG_CONTAINER) \
		-e POSTGRES_USER=migrate -e POSTGRES_PASSWORD=migrate -e POSTGRES_DB=migrate_check \
		-p $(MIGRATE_PG_PORT):5432 $(MIGRATE_PG_IMAGE) >/dev/null
	@trap 'docker rm -f $(MIGRATE_PG_CONTAINER) >/dev/null 2>&1 || true' EXIT; \
		echo "  waiting for Postgres on :$(MIGRATE_PG_PORT)..."; \
		for i in $$(seq 1 30); do \
			docker exec $(MIGRATE_PG_CONTAINER) pg_isready -U migrate -d migrate_check >/dev/null 2>&1 && break; \
			if [ "$$i" = "30" ]; then echo "FAIL: Postgres did not become ready" >&2; exit 1; fi; \
			sleep 1; \
		done; \
		MIGRATE_TEST_DSN="postgres://migrate:migrate@localhost:$(MIGRATE_PG_PORT)/migrate_check?sslmode=disable" \
			$(GOTEST) -count=1 -run TestMigrationsAgainstRealPostgres ./pkg/database/migrate/
	@echo "Migration gate passed."

## coverage: Generate coverage report
coverage: test
	@echo "Generating coverage report..."
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run patch-scoped linter (matches CI's only-new-issues=true exactly)
##
## CI's golangci-lint-action runs with only-new-issues=true on every PR,
## reporting findings only on lines changed in the PR. This target
## mirrors that scope so local fails when CI would fail.
##
## CRITICAL: golangci-lint's --new-from-rev flag only sees COMMITTED
## changes, so before any commits the patch is empty and lint
## early-exits as a no-op — letting bad code reach the commit gate.
## This target generates a unified-diff patch from the merge-base
## that includes BOTH committed changes AND working-tree changes
## (staged + unstaged), then passes it via --new-from-patch. The
## patch-based path catches the same issues CI would, AND issues in
## uncommitted code, so `make verify` is a true pre-commit gate.
##
## Merge-base resolution: prefer origin/main (matches CI's PR base
## ref). Falls back to local main only if origin/main is not
## reachable (detached HEAD, fresh clone before fetch). If neither
## is reachable the patch lint warns and skips rather than silently
## passing.
##
## Use `make lint-full` to scan the entire codebase (housekeeping;
## not part of `make verify`).
lint:
	@echo "Running patch-scoped lint (matches CI only-new-issues, includes uncommitted changes)..."
	@# Auto-fetch so a fresh clone or a stale local mirror doesn't bypass
	@# the gate. The fetch is shallow + quiet and tolerates network
	@# absence; if BOTH origin/main and main remain unreachable, we
	@# HARD FAIL rather than silently skip — silent skipping is exactly
	@# how a clean local make verify let lint issues reach CI in #393.
	@git fetch --quiet origin main 2>/dev/null || true
	@if git rev-parse origin/main >/dev/null 2>&1; then \
		BASE=origin/main; \
	elif git rev-parse main >/dev/null 2>&1; then \
		BASE=main; \
	else \
		echo "ERROR: neither origin/main nor main is reachable."; \
		echo "       Run \`git fetch origin main\` and retry."; \
		echo "       (lint MUST run against a base; silent-skip is a CI-parity hole.)"; \
		exit 1; \
	fi; \
	MERGE_BASE=$$(git merge-base $$BASE HEAD 2>/dev/null); \
	if [ -z "$$MERGE_BASE" ]; then \
		echo "ERROR: could not compute merge-base against $$BASE."; \
		echo "       Ensure the current branch shares history with $$BASE."; \
		exit 1; \
	fi; \
	PATCH=$$(mktemp -t mcpdp-lint-patch.XXXXXX); \
	trap "rm -f $$PATCH" EXIT; \
	git diff $$MERGE_BASE > $$PATCH; \
	if [ ! -s $$PATCH ]; then \
		echo "No changes vs merge-base ($$BASE); nothing to lint."; \
		echo "       (If you expected changes, confirm \`git log $$BASE..HEAD\` is non-empty.)"; \
		exit 0; \
	fi; \
	echo "Linting against merge-base $$MERGE_BASE (from $$BASE) — includes uncommitted changes"; \
	$(GOLINT) run --new-from-patch=$$PATCH ./...

## lint-full: Run linter against the ENTIRE codebase (not chained into verify)
##
## CI does not enforce findings on pre-existing code, so neither does
## `make verify`. This target exists for housekeeping passes.
lint-full:
	@echo "Running full-codebase linter (informational; not enforced by CI)..."
	$(GOLINT) run ./...

## lint-fix: Run linter with auto-fix
lint-fix:
	@echo "Running linter with auto-fix..."
	$(GOLINT) run --fix ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR) $(DIST_DIR)
	@rm -f coverage.out coverage.html
	@rm -rf $(UI_DIR)/dist $(UI_DIR)/dist-content-viewer $(UI_DIR)/node_modules
	@# Reset embed dirs but keep .gitkeep
	@find $(UI_EMBED_DIR) -not -name '.gitkeep' -not -path $(UI_EMBED_DIR) -delete 2>/dev/null || true
	@find $(CV_EMBED_DIR) -not -name '.gitkeep' -not -path $(CV_EMBED_DIR) -delete 2>/dev/null || true
	@echo "Clean complete."

## install: Install the binary
install: build
	@echo "Installing $(BINARY_NAME)..."
	$(GO) install $(LDFLAGS) $(CMD_DIR)
	@echo "Installed."

## mod-tidy: Tidy go modules
mod-tidy:
	@echo "Tidying modules..."
	$(GOMOD) tidy

## mod-download: Download modules
mod-download:
	@echo "Downloading modules..."
	$(GOMOD) download

## mod-verify: Verify modules
mod-verify:
	@echo "Verifying modules..."
	$(GOMOD) verify

## security: Run security checks (gosec + govulncheck)
security:
	@echo "Running gosec..."
	gosec -quiet ./...
	@echo "Running govulncheck..."
	govulncheck ./...

## semgrep: Run Semgrep SAST with standard and custom rules
semgrep:
	@echo "Running Semgrep..."
	semgrep scan --config p/golang --config .semgrep/ --error --quiet .

## codeql: Run CodeQL analysis (requires codeql CLI)
codeql:
	@echo "Running CodeQL analysis..."
	@rm -rf /tmp/mcp-dp-codeql-db
	codeql database create /tmp/mcp-dp-codeql-db --language=go --source-root=. --overwrite
	@codeql database analyze /tmp/mcp-dp-codeql-db \
		--format=sarif-latest --output=codeql-results.sarif \
		codeql/go-queries:codeql-suites/go-security-and-quality.qls
	@# Gate logic lives in scripts/codeql-gate.py — it counts results
	@# with sarif level=error OR security-severity >= 7.0. The
	@# security-severity check matches what GitHub Code Scanning
	@# treats as a blocking alert in CI: without it, low-confidence
	@# taint findings (go/request-forgery, go/sql-injection,
	@# go/log-injection) surface as `level=note` locally but block
	@# the CodeQL step in CI. Local CI parity is the whole point of
	@# `make verify`.
	@python3 scripts/codeql-gate.py codeql-results.sarif

## sast: Run all SAST scanners (semgrep + codeql)
sast: semgrep codeql

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	docker build -t txn2/mcp-data-platform:$(VERSION) .
	docker tag txn2/mcp-data-platform:$(VERSION) txn2/mcp-data-platform:latest

## run: Run the server
run: build
	@echo "Running $(BINARY_NAME)..."
	$(BUILD_DIR)/$(BINARY_NAME)

## version: Show version
version:
	@echo "Version: $(VERSION)"
	@echo "Go Version: $(GO_VERSION)"
	@echo "Build Time: $(BUILD_TIME)"

## dead-code: Report unreachable functions (informational, not blocking)
dead-code:
	@echo "Checking for dead code..."
	@OUTPUT=$$(deadcode ./... 2>&1 | grep -v "^$$") || true; \
	if [ -n "$$OUTPUT" ]; then \
		echo "Dead code detected (review for false positives):"; \
		echo "$$OUTPUT"; \
	else \
		echo "No dead code found."; \
	fi

## mutate: Run mutation testing with 60% efficacy threshold
mutate:
	@echo "Running mutation testing..."
	gremlins unleash --workers 1 --timeout-coefficient 3 --threshold-efficacy 60 ./pkg/...

## coverage-report: Print coverage summary (fails if total <80%)
coverage-report: test
	@echo ""
	@echo "=== Coverage Summary ==="
	@$(GO) tool cover -func=coverage.out | tail -1
	@echo ""
	@TOTAL=$$($(GO) tool cover -func=coverage.out | tail -1 | awk '{gsub(/%/,"",$$3); print $$3}'); \
	if [ "$$(echo "$$TOTAL < 80.0" | bc -l)" = "1" ]; then \
		echo "FAIL: Total coverage $$TOTAL% is below 80% threshold"; \
		exit 1; \
	fi
	@echo "Functions with 0% coverage:"
	@$(GO) tool cover -func=coverage.out | awk '{gsub(/%/,"",$$3); if ($$3+0 == 0 && $$1 != "total:") print $$0}' || true
	@echo ""
	@echo "Functions below 80% coverage:"
	@$(GO) tool cover -func=coverage.out | awk '{gsub(/%/,"",$$3); if ($$3+0 < 80.0 && $$3+0 > 0 && $$1 != "total:") print $$0}' || true
	@echo "=== End Coverage ==="

## patch-coverage: Check coverage of changed lines vs main (fails if <80%)
patch-coverage:
	@echo "Checking patch coverage..."
	@./scripts/patch-coverage.sh

## doc-check: Warn if documentation-worthy changes lack doc updates (soft warning)
doc-check:
	@./scripts/doc-check.sh

## release-check: Validate build, Docker, and release config
release-check:
	@echo "Running GoReleaser dry-run..."
	goreleaser release --snapshot --clean --skip=publish,sign,sbom

## swagger: Generate OpenAPI/Swagger documentation from annotations
swagger:
	@echo "Generating Swagger docs..."
	@rm -f internal/apidocs/docs.go internal/apidocs/swagger.json internal/apidocs/swagger.yaml
	swag init --generalInfo pkg/admin/handler.go --dir . --output internal/apidocs --parseDependency
	@echo "Injecting tag descriptions and x-tagGroups..."
	@python3 scripts/swagger-tag-groups.py internal/apidocs
	@echo "Swagger docs generated in internal/apidocs/"

## swagger-check: Verify Swagger docs are up to date
swagger-check: swagger
	@if git diff --quiet internal/apidocs/; then \
		echo "Swagger docs are up to date"; \
	else \
		echo "ERROR: Swagger docs are out of date. Run 'make swagger' and commit."; \
		exit 1; \
	fi

## tools-check: Verify all required tools are installed AND pinned to CI versions
##
## Local-vs-CI tool version drift is the most insidious parity gap: different
## golangci-lint or gosec versions enable different rules with different
## defaults, so `make verify` can pass locally while CI rejects the same
## diff. Concrete incident on 2026-05-08: local gosec 2.26.1 silently dropped
## the G704 SSRF taint rule that CI's pinned v2.22.0 enforces, letting an
## actual SSRF bug ship to PR #377. See feedback_gate_metric.md.
tools-check:
	@echo "Checking required tools (presence AND pinned versions)..."
	@missing=""; mismatch=""; \
	if ! which golangci-lint > /dev/null 2>&1; then \
		missing="$$missing  golangci-lint: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)\n"; \
	else \
		v=$$(go version -m $$(which golangci-lint) 2>/dev/null | awk '$$1=="mod" && $$2 ~ /golangci-lint/ {print $$3}'); \
		if [ -z "$$v" ] || [ "$$v" = "(devel)" ]; then \
			v=$$(golangci-lint version 2>&1 | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
			case "$$v" in v*) ;; *) v="v$$v";; esac; \
		fi; \
		if [ "$$v" != "$(GOLANGCI_LINT_VERSION)" ]; then \
			mismatch="$$mismatch  golangci-lint: have $$v, want $(GOLANGCI_LINT_VERSION) — go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)\n"; \
		fi; \
	fi; \
	if ! which gosec > /dev/null 2>&1; then \
		missing="$$missing  gosec: go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)\n"; \
	else \
		v=$$(go version -m $$(which gosec) 2>/dev/null | awk '$$1=="mod" && $$2 ~ /gosec/ {print $$3}'); \
		if [ -z "$$v" ] || [ "$$v" = "(devel)" ]; then \
			v=$$(gosec --version 2>&1 | grep -oE 'Version: v?[0-9]+\.[0-9]+\.[0-9]+' | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
			case "$$v" in v*) ;; *) v="v$$v";; esac; \
		fi; \
		if [ "$$v" != "$(GOSEC_VERSION)" ]; then \
			mismatch="$$mismatch  gosec: have $$v, want $(GOSEC_VERSION) — go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)\n"; \
		fi; \
	fi; \
	which govulncheck > /dev/null 2>&1   || missing="$$missing  govulncheck: go install golang.org/x/vuln/cmd/govulncheck@latest\n"; \
	which semgrep > /dev/null 2>&1       || missing="$$missing  semgrep: pip3 install semgrep\n"; \
	which codeql > /dev/null 2>&1        || missing="$$missing  codeql: brew install codeql\n"; \
	which deadcode > /dev/null 2>&1      || missing="$$missing  deadcode: go install golang.org/x/tools/cmd/deadcode@latest\n"; \
	which gremlins > /dev/null 2>&1      || missing="$$missing  gremlins: go install github.com/go-gremlins/gremlins/cmd/gremlins@latest\n"; \
	which goreleaser > /dev/null 2>&1    || missing="$$missing  goreleaser: brew install goreleaser\n"; \
	which swag > /dev/null 2>&1          || missing="$$missing  swag: go install github.com/swaggo/swag/cmd/swag@latest\n"; \
	if [ -n "$$missing" ]; then \
		echo ""; \
		echo "FAIL: Missing required tools:"; \
		printf '%b' "$$missing"; \
		echo ""; \
		echo "Install all missing tools before running make verify."; \
		exit 1; \
	fi; \
	if [ -n "$$mismatch" ]; then \
		echo ""; \
		echo "FAIL: Tool version mismatch (local differs from CI-pinned)."; \
		echo "Local versions that drift from CI's create silent parity gaps:"; \
		echo "make verify can pass while CI rejects the same diff."; \
		echo ""; \
		printf '%b' "$$mismatch"; \
		echo ""; \
		echo "Pin local tools to the CI versions before running make verify."; \
		echo "(Override with TOOLS_CHECK_STRICT=0 only if you know what you are doing.)"; \
		if [ "$(TOOLS_CHECK_STRICT)" != "0" ]; then exit 1; fi; \
		echo "WARN: proceeding with mismatched tool versions (TOOLS_CHECK_STRICT=0)."; \
	else \
		echo "All required tools found at pinned CI versions."; \
	fi

## embed-clean: Reset UI embed dirs to .gitkeep only (matches CI clean checkout)
embed-clean:
	@echo "Cleaning UI embed directories..."
	@find $(UI_EMBED_DIR) -not -name '.gitkeep' -not -path $(UI_EMBED_DIR) -delete 2>/dev/null || true
	@find $(CV_EMBED_DIR) -not -name '.gitkeep' -not -path $(CV_EMBED_DIR) -delete 2>/dev/null || true

## verify-release: Full verify PLUS mutation testing — run only before cutting a release
## Mutation testing (gremlins) is expensive and must NOT run per-revision.
verify-release: verify mutate
	@echo ""
	@echo "=== Release verification complete (incl. mutation testing) ==="

## verify: Run the CI-equivalent per-commit suite (test, lint, security, SAST, coverage, release)
## NOTE: mutation testing is intentionally excluded — it lives in verify-release.
## Do not add `mutate` back to this per-commit target.
verify: tools-check fmt swagger-check embed-clean test migrate-check lint security semgrep codeql coverage-report patch-coverage doc-check dead-code release-check
	@echo ""
	@echo "=== All checks passed ==="
	@# Write the gate sentinel: the short SHA-256 of the working-tree diff
	@# (staged + unstaged) at the moment verify completed. The pre-commit
	@# review gate (~/.claude/hooks/review-gate.sh) compares this hash to
	@# the live diff at commit time — if they match, this verify run is
	@# proof CI-equivalent checks passed on the exact code being committed.
	@# Hash computation MUST stay byte-identical to compute_diff_hash() in
	@# review-gate.sh, otherwise the gate will reject every commit.
	@mkdir -p .claude
	@{ git diff --cached HEAD 2>/dev/null; git diff 2>/dev/null; } \
		| shasum -a 256 | cut -c1-16 > .claude/.last-verify-passed
	@echo "Wrote .claude/.last-verify-passed (gate sentinel)"

## docs-serve: Serve documentation locally
docs-serve:
	@echo "Serving documentation at http://localhost:8000..."
	python3 -m mkdocs serve

## docs-build: Build documentation
docs-build:
	@echo "Building documentation..."
	python3 -m mkdocs build

## help: Show this help message
help:
	@echo "mcp-data-platform Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

# =============================================================================
# Frontend Targets (unified portal UI)
# =============================================================================

## frontend-install: Install UI dependencies
frontend-install:
	@echo "Installing UI dependencies..."
	cd $(UI_DIR) && npm ci
	@echo "UI dependencies installed."

## frontend-build-content-viewer: Build standalone content viewer JS bundle (CSS comes from SPA build)
frontend-build-content-viewer: frontend-install
	@echo "Building content viewer (JS only)..."
	cd $(UI_DIR) && npx vite build --config vite.content-viewer.config.ts
	@mkdir -p $(CV_EMBED_DIR)
	@cp $(UI_DIR)/dist-content-viewer/content-viewer.js $(CV_EMBED_DIR)/
	@echo "Content viewer JS built and embedded."

## frontend-build: Build SPA first (produces CSS), then content viewer (JS only), copy SPA CSS as content-viewer CSS
frontend-build: frontend-install
	@echo "Building SPA..."
	cd $(UI_DIR) && npm run build
	@echo "Copying SPA dist to embed directory..."
	@rm -rf $(UI_EMBED_DIR)/*
	@cp -r $(UI_DIR)/dist/* $(UI_EMBED_DIR)/
	@rm -f $(UI_EMBED_DIR)/mockServiceWorker.js
	@echo "SPA built and embedded."
	cd $(UI_DIR) && npx vite build --config vite.content-viewer.config.ts
	@mkdir -p $(CV_EMBED_DIR)
	@cp $(UI_DIR)/dist-content-viewer/content-viewer.js $(CV_EMBED_DIR)/
	@echo "Copying SPA CSS as content-viewer CSS..."
	@SPA_CSS=$$(find $(UI_DIR)/dist/assets -maxdepth 1 -name '*.css' -print -quit 2>/dev/null); \
	if [ -z "$$SPA_CSS" ]; then echo "ERROR: SPA CSS not found in $(UI_DIR)/dist/assets/"; exit 1; fi; \
	cp "$$SPA_CSS" $(CV_EMBED_DIR)/content-viewer.css
	@echo "Frontend build complete."

## frontend-dev: Run UI dev server (hot reload)
frontend-dev:
	cd $(UI_DIR) && npm run dev

## frontend-mock: Run UI dev server with mock data (no backend needed)
frontend-mock:
	cd $(UI_DIR) && VITE_MSW=true npm run dev

## frontend-test: Run UI tests
frontend-test:
	cd $(UI_DIR) && npm run test

## build-with-ui: Build Go binary with embedded UI
build-with-ui: frontend-build build

# =============================================================================
# E2E Testing Targets
# =============================================================================

E2E_COMPOSE := docker compose -f docker-compose.e2e.yml

## e2e-up: Start E2E test environment (PostgreSQL, Trino, MinIO)
e2e-up:
	@echo "Starting E2E test environment..."
	@echo "NOTE: For full E2E tests, also run 'datahub docker quickstart' separately"
	$(E2E_COMPOSE) up -d postgres trino minio
	@echo "Waiting for services to be healthy..."
	@./scripts/wait-for-services.sh
	@echo "Running setup containers..."
	$(E2E_COMPOSE) up minio-setup trino-setup
	@echo "E2E environment is ready!"

## e2e-down: Stop E2E test environment
e2e-down:
	@echo "Stopping E2E test environment..."
	$(E2E_COMPOSE) down -v
	@echo "E2E environment stopped."

## e2e-seed: Seed DataHub with test data (requires DataHub running)
e2e-seed:
	@echo "Seeding DataHub with test data..."
	@if ! docker ps --format '{{.Names}}' | grep -q "datahub-gms"; then \
		echo "ERROR: DataHub is not running. Start it with: datahub docker quickstart"; \
		exit 1; \
	fi
	@echo "Ingesting datasets..."
	@datahub put --file test/e2e/testdata/datahub/domains.json 2>/dev/null || \
		echo "Note: datahub CLI not found or ingestion failed - manual seeding may be required"
	@datahub put --file test/e2e/testdata/datahub/tags.json 2>/dev/null || true
	@datahub put --file test/e2e/testdata/datahub/owners.json 2>/dev/null || true
	@datahub put --file test/e2e/testdata/datahub/datasets.json 2>/dev/null || true
	@echo "DataHub seeding complete."

## e2e-test: Run E2E tests (requires services running)
e2e-test:
	@echo "Running E2E tests..."
	$(GOTEST) -v -race -tags=integration ./test/e2e/...
	@echo "E2E tests complete."

## e2e: Full E2E cycle (up, seed, test, down)
e2e: e2e-up
	@echo ""
	@echo "To run full E2E tests with DataHub:"
	@echo "  1. In another terminal: datahub docker quickstart"
	@echo "  2. Run: make e2e-seed"
	@echo "  3. Run: make e2e-test"
	@echo "  4. Run: make e2e-down"
	@echo ""
	@echo "Or run partial tests without DataHub:"
	@echo "  make e2e-test"

## e2e-logs: Show E2E service logs
e2e-logs:
	$(E2E_COMPOSE) logs -f

## e2e-clean: Remove all E2E artifacts and volumes
e2e-clean: e2e-down
	@echo "Cleaning E2E artifacts..."
	@docker volume rm -f mcp-data-platform_postgres_data mcp-data-platform_minio_data 2>/dev/null || true
	@echo "E2E cleanup complete."

# =============================================================================
# Local Dev Environment (ACME Corporation)
# =============================================================================

DEV_COMPOSE := docker compose -f dev/docker-compose.yml

## dev-up: Start ACME dev environment (PostgreSQL)
dev-up:
	@echo "Starting ACME dev environment..."
	$(DEV_COMPOSE) up -d
	@echo "Waiting for PostgreSQL to be healthy..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if docker exec acme-dev-postgres pg_isready -U platform -d mcp_platform -q 2>/dev/null; then \
			echo "PostgreSQL is ready."; \
			break; \
		fi; \
		if [ $$i -eq 10 ]; then echo "ERROR: PostgreSQL failed to start"; exit 1; fi; \
		sleep 1; \
	done
	@echo ""
	@echo "=== ACME Dev Environment Ready ==="
	@echo ""
	@echo "Start the Go server:"
	@echo "  go run ./cmd/mcp-data-platform --config dev/platform.yaml"
	@echo ""
	@echo "(Optional) Seed historical data:"
	@echo "  psql -h localhost -U platform -d mcp_platform -f dev/seed.sql"
	@echo ""
	@echo "Start the portal UI:"
	@echo "  cd ui && npm run dev"
	@echo ""
	@echo "Or use MSW mode (no backend needed):"
	@echo "  cd ui && VITE_MSW=true npm run dev"
	@echo ""
	@echo "API Key: acme-dev-key-2024"
	@echo ""

## dev-down: Stop ACME dev environment and remove volumes
dev-down:
	@echo "Stopping ACME dev environment..."
	$(DEV_COMPOSE) down -v
	@# Kill leftover host processes that dev/start.sh's trap may have
	@# missed (e.g., when the script was backgrounded and the parent
	@# shell exited). Without these, ports 5173/8080 stay occupied
	@# even though Docker is clean and the next 'make dev' fails its
	@# port pre-flight check.
	@pkill -f "build/air/mcp-data-platform" 2>/dev/null || true
	@pkill -f "air -c dev/.air.toml" 2>/dev/null || true
	@pkill -f "ui/node_modules/.bin/vite" 2>/dev/null || true
	@pkill -f "@esbuild/.*/bin/esbuild --service" 2>/dev/null || true
	@pkill -f "go run ./cmd/dev-mcp-mock" 2>/dev/null || true
	@pkill -f "/dev-mcp-mock$$" 2>/dev/null || true
	@echo "ACME dev environment stopped."

## dev: Start full dev environment with hot-reload (Docker + Go + Vite)
## Runs pre-flight checks (Docker, air, ports), starts services sequentially,
## waits for health, seeds data on first run, and reports clear status.
dev:
	@bash dev/start.sh

## dev-info: Print the dev login (Portal URL, API key, sign-in users)
## Handy when `make dev`'s startup banner has scrolled out of view.
dev-info:
	@bash dev/info.sh

## mock-check: Verify MSW mocks conform to Swagger spec types
mock-check: swagger
	@echo "Generating TypeScript types from Swagger spec..."
	cd $(UI_DIR) && npm run generate-api-types
	@echo "Type-checking mocks against generated types..."
	cd $(UI_DIR) && npx tsc --noEmit
	@echo "Running mock conformance tests..."
	cd $(UI_DIR) && npx vitest run src/mocks/conformance.test.ts
	@echo "Mock conformance check passed."

## preview-apps: Serve MCP apps locally at http://localhost:8000/test-harness.html (no server needed)
preview-apps:
	@echo "→ Open http://localhost:8000/test-harness.html"
	@cd apps && python3 -m http.server 8000 --bind 127.0.0.1

## preview-platform-info: Preview platform_info app with data from a real config file.
## Accepts a Kubernetes ConfigMap YAML or direct platform YAML.
## Usage: make preview-platform-info CONFIG=/path/to/config.yaml
## Requires Python 3 + PyYAML: pip3 install pyyaml
preview-platform-info:
	@if [ -z "$(CONFIG)" ]; then \
		echo "Usage: make preview-platform-info CONFIG=/path/to/config.yaml"; \
		exit 1; \
	fi
	@echo "→ Extracting preview data from $(CONFIG)"
	@python3 scripts/extract-preview-data.py "$(CONFIG)" apps/preview-data.json
	@$(MAKE) preview-apps
