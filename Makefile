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
GOSEC_VERSION := v2.28.0
GREMLINS_VERSION := v0.6.0

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := gofmt
GOLINT := golangci-lint

.PHONY: all build test lint lint-full fmt clean install help docs-serve docs-build verify verify-release \
	tools-check dead-code mutate patch-coverage doc-check swagger swagger-check \
	semgrep codeql sast osv embed-clean migrate-check \
	frontend-install frontend-build frontend-build-content-viewer \
	frontend-dev frontend-mock frontend-test frontend-lint frontend-e2e \
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

# Real-DB round-trip gate for tool write paths. Runs every integration test
# named *RealDB* against a self-provisioned pgvector container (testcontainers),
# exercising the actual schema (NOT NULL constraints, defaults, column types)
# that sqlmock cannot. This is the gate that would have caught the prompt-create
# defect that shipped to production (pq.Array(nil) -> NULL into NOT NULL tags).
# Convention: name any tool write-path round-trip test *RealDB* so it runs here.
# Requires Docker. Part of `verify`, alongside migrate-check.
## test-realdb: Real-Postgres round-trip gate for tool write paths (Docker required)
test-realdb:
	@echo "Running real-DB round-trip gate..."
	$(GOTEST) -count=1 -tags=integration -run 'RealDB' ./...
	@echo "Real-DB gate passed."

# Live post-deploy smoke: connects to a RUNNING MCP server as a real MCP client
# (admin API key) and exercises every user-facing write tool end to end, then
# cleans up. This is the layer that catches deployment drift / config the
# in-process gates cannot see. Skips unless MCP_API_KEY is set.
## smoke: Live smoke against a running MCP (env: MCP_BASE_URL, MCP_API_KEY)
smoke:
	@echo "Running live smoke against $${MCP_BASE_URL:-http://localhost:8099}..."
	$(GOTEST) -count=1 -tags=integration ./test/smoke/ -run Smoke -v

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
		-p 127.0.0.1:$(MIGRATE_PG_PORT):5432 $(MIGRATE_PG_IMAGE) >/dev/null
	@trap 'docker rm -f $(MIGRATE_PG_CONTAINER) >/dev/null 2>&1 || true' EXIT; \
		echo "  waiting for Postgres on :$(MIGRATE_PG_PORT)..."; \
		for i in $$(seq 1 60); do \
			docker exec $(MIGRATE_PG_CONTAINER) pg_isready -h localhost -p 5432 -U migrate -d migrate_check >/dev/null 2>&1 && break; \
			if [ "$$i" = "60" ]; then echo "FAIL: Postgres did not become ready" >&2; exit 1; fi; \
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

## osv: Run osv-scanner (informational; mirrors OpenSSF Scorecard)
## Not part of `verify`: osv-scanner scans the whole go.sum graph regardless of
## reachability, so it flags test-only/indirect deps that never touch the
## binary (which govulncheck, already in `security`, correctly reports as 0).
## Suppressions with justification + expiry live in osv-scanner.toml.
osv:
	@echo "Running osv-scanner (informational)..."
	@osv-scanner scan source -r --config osv-scanner.toml . || true

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

## doc-check: Fail on orphaned docs or unregistered tool refs; warn on undocumented changes
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
	if ! which gremlins > /dev/null 2>&1; then \
		missing="$$missing  gremlins: go install github.com/go-gremlins/gremlins/cmd/gremlins@$(GREMLINS_VERSION)\n"; \
	else \
		v=$$(go version -m $$(which gremlins) 2>/dev/null | awk '$$1=="mod" && $$2 ~ /gremlins/ {print $$3}'); \
		if [ -z "$$v" ] || [ "$$v" = "(devel)" ]; then \
			v=$$(gremlins --version 2>&1 | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
			case "$$v" in v*) ;; *) v="v$$v";; esac; \
		fi; \
		if [ "$$v" != "$(GREMLINS_VERSION)" ]; then \
			mismatch="$$mismatch  gremlins: have $$v, want $(GREMLINS_VERSION) — go install github.com/go-gremlins/gremlins/cmd/gremlins@$(GREMLINS_VERSION)\n"; \
		fi; \
	fi; \
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
verify: tools-check fmt swagger-check embed-clean test migrate-check test-realdb frontend-test frontend-lint frontend-e2e lint bench-test bench-lint security semgrep codeql coverage-report patch-coverage doc-check dead-code release-check
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

## frontend-lint: Run the UI complexity/coupling lint gate (#816, mirrors CI's frontend job lint step)
frontend-lint:
	cd $(UI_DIR) && npm run lint

## frontend-e2e: Run the interactive Playwright suite against the MSW-mocked dev server (mirrors CI's frontend-e2e job)
frontend-e2e:
	cd $(UI_DIR) && npx playwright install chromium && npm run test:e2e

## build-with-ui: Build Go binary with embedded UI
build-with-ui: frontend-build build

# =============================================================================
# E2E Testing Targets
# =============================================================================

# Cleared DOCKER_DEFAULT_PLATFORM (see DEV_COMPOSE below) so the local E2E stack
# uses the host's native platform for multi-arch images like pgvector.
E2E_COMPOSE := DOCKER_DEFAULT_PLATFORM= docker compose -f docker-compose.e2e.yml

## e2e-up: Start E2E test environment (PostgreSQL, Trino, SeaweedFS)
e2e-up:
	@echo "Starting E2E test environment..."
	@echo "NOTE: For full E2E tests, also run 'datahub docker quickstart' separately"
	$(E2E_COMPOSE) up -d postgres trino seaweedfs
	@echo "Waiting for services to be healthy..."
	@./scripts/wait-for-services.sh
	@echo "Running setup containers..."
	$(E2E_COMPOSE) up seaweedfs-setup trino-setup
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
	@docker volume rm -f mcp-data-platform_postgres_data mcp-data-platform_seaweedfs_data 2>/dev/null || true
	@echo "E2E cleanup complete."

# =============================================================================
# Local Dev Environment (ACME Corporation)
# =============================================================================

# Clear DOCKER_DEFAULT_PLATFORM for the local dev stack: a developer who exports
# it as linux/amd64 (to build cluster images) would otherwise force the amd64
# variant of multi-arch images like pgvector/pgvector:pg16, which fails on an
# arm64 host whose cached image is arm64 ("platform does not match"). An empty
# value lets Docker pick the host's native platform, so this works on both
# arm64 and amd64 machines.
DEV_COMPOSE := DOCKER_DEFAULT_PLATFORM= docker compose -f dev/docker-compose.yml

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

# =============================================================================
# Load Testing (issue #921)
# =============================================================================
#
# The load harness lives in test/load (a separate Go module, kept out of the
# root coverage/test/lint denominator). It drives named workloads against a
# running platform over the MCP protocol and REST surfaces and publishes numbers
# in docs/reference/tuning-and-scaling.md.
#
# Like `mutate`, load testing is DELIBERATELY NOT part of `make verify`: it
# stands up Docker services and a real server binary and runs for tens of
# seconds to an hour per scenario — far too slow and heavy for a per-commit
# gate. Do NOT add load-* to the `verify` target. Run it on demand, locally or
# via the workflow_dispatch load.yml workflow.

# LOAD_KEY is the admin API key the harness authenticates with. Override for a
# non-default deployment. LOAD_CONFIG points at the platform config for the run.
LOAD_KEY ?= load-admin-key
LOAD_CONFIG ?= test/load/config/platform.load.yaml
LOAD_ADDR ?= :8099
LOAD_METRICS_ADDR ?= :9091
LOAD_PPROF_ADDR ?= :6060
LOAD_URL ?= http://localhost:8099
LOAD_PID := build/mcp-data-platform-load.pid
LOAD_LOG := build/mcp-data-platform-load.log

## load-up: Start the compose stack and a release-built platform for load tests
load-up: e2e-up
	@echo "Building release-style platform binary (no -race)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-load $(CMD_DIR)
	@echo "Building loadgen..."
	@cd test/load && $(GO) build -o ../../$(BUILD_DIR)/loadgen ./loadgen
	@echo "Starting platform on $(LOAD_ADDR) (metrics $(LOAD_METRICS_ADDR), pprof $(LOAD_PPROF_ADDR))..."
	@API_KEY_ADMIN=$(LOAD_KEY) LOG_LEVEL=info \
		OTEL_METRICS_ADDR=$(LOAD_METRICS_ADDR) PPROF_ADDR=$(LOAD_PPROF_ADDR) \
		OAUTH_RL_ENABLED=$(OAUTH_RL_ENABLED) AUDIT_DELIVERY=$(AUDIT_DELIVERY) \
		$(BUILD_DIR)/$(BINARY_NAME)-load --config $(LOAD_CONFIG) --transport http --address $(LOAD_ADDR) \
		> $(LOAD_LOG) 2>&1 & echo $$! > $(LOAD_PID)
	@echo "Waiting for readiness on $(LOAD_URL)/readyz ..."
	@for i in $$(seq 1 30); do \
		if curl -fsS $(LOAD_URL)/readyz >/dev/null 2>&1; then echo "Platform ready (pid $$(cat $(LOAD_PID)))."; exit 0; fi; \
		sleep 1; \
	done; \
	echo "ERROR: platform did not become ready; see $(LOAD_LOG)"; tail -20 $(LOAD_LOG); exit 1

## load-run: Run one load scenario (SCENARIO=<name>; see loadgen -list)
## Optional: CONCURRENCY=, DURATION=, WARMUP=, RATE=, OUT= override defaults.
load-run:
	@if [ -z "$(SCENARIO)" ]; then echo "Usage: make load-run SCENARIO=<name> (see: $(BUILD_DIR)/loadgen -list)"; exit 1; fi
	@mkdir -p build/load-reports
	$(BUILD_DIR)/loadgen \
		-scenario $(SCENARIO) \
		-url $(LOAD_URL) \
		-metrics-url http://localhost$(LOAD_METRICS_ADDR)/metrics \
		-pprof-url http://localhost$(LOAD_PPROF_ADDR) \
		-credential $(LOAD_KEY) \
		-release-build \
		-profile-dir build/load-reports/profiles \
		-out build/load-reports/report-$(SCENARIO).json \
		$(if $(CONCURRENCY),-concurrency $(CONCURRENCY),) \
		$(if $(DURATION),-duration $(DURATION),) \
		$(if $(WARMUP),-warmup $(WARMUP),) \
		$(if $(RATE),-rate $(RATE),)

## load-down: Stop the load platform and the compose stack
load-down:
	@if [ -f $(LOAD_PID) ]; then \
		echo "Stopping platform (pid $$(cat $(LOAD_PID)))..."; \
		kill $$(cat $(LOAD_PID)) 2>/dev/null || true; \
		rm -f $(LOAD_PID); \
	fi
	@$(MAKE) e2e-down

## load-test: Build, vet, lint, and unit-test the harness module itself
load-test:
	@echo "Testing the load harness module..."
	@cd test/load && $(GO) build ./... && $(GO) vet ./... && $(GO) test ./...

# =============================================================================
# Agent-Effectiveness Benchmark (issue #930, phase 1: #942)
# =============================================================================
#
# The benchmark harness lives in bench/ (a separate Go module, kept out of the
# root coverage/test/lint denominator, like test/load). It ablates the PLATFORM
# (arms a0/a2 as config profiles) while holding the model, prompt scaffold,
# seed data, and task set constant, and reads efficiency metrics back from the
# platform's own audit API.
#
# Like `mutate` and `load-*`, benchmarking is DELIBERATELY NOT part of
# `make verify`: it stands up Docker services, a real server binary, and (for
# real runs) a model API. Do NOT add bench-* to the `verify` target.
#
# Arms (config profiles selected by BENCH_ARM):
#   a0  baseline — raw tools, no enrichment, no search
#   a1  enrichment — a0 plus semantic cross-enrichment (needs DataHub)
#   a2  knowledge — a1 plus search, the search-first gate, knowledge pages (needs DataHub)
#   a3  lifecycle — a2 plus memory/insight + apply_knowledge (needs DataHub)
# The DataHub arms (a1/a2/a3) require a DataHub quickstart seeded via
# `make bench-seed-datahub`.

# BENCH_ARM selects the platform config profile; BENCH_KEY is the admin API key.
BENCH_ARM ?= a0
BENCH_KEY ?= bench-admin-key
BENCH_CONFIG ?= bench/config/platform.bench.$(BENCH_ARM).yaml
BENCH_ADDR ?= :8098
BENCH_METRICS_ADDR ?= :9092
BENCH_URL ?= http://localhost:8098
BENCH_PID := build/mcp-data-platform-bench.pid
BENCH_LOG := build/mcp-data-platform-bench.log
BENCH_COMPOSE := DOCKER_DEFAULT_PLATFORM= docker compose -f docker-compose.e2e.yml

## bench-gen: Regenerate seed artifacts, the task set, and the S5 protocols from the fixed seed
bench-gen:
	@cd bench && $(GO) run ./seedgen -seed-dir seed -tasks-dir tasks -protocols-dir protocols -curriculum-dir curriculum

# --- API-connection architecture study (issue #1027) ---------------------
# Arms are config profiles (BENCH_ARM=b0|b1-lex|b1-hyb); the catalog tier
# is a run parameter (BENCH_TIER=t0|t1|t2). bench-api-up starts Postgres,
# the fixture service (apisvc), the per-endpoint MCP server (epmcp, b0
# only), and the platform, then registers the fixture through the admin
# REST API (apisetup). b1-hyb additionally waits for the embedding index
# to cover every operation (requires ollama serve + nomic-embed-text).
BENCH_TIER ?= t0
BENCH_APISVC_ADDR ?= :8110
BENCH_APISVC_URL ?= http://127.0.0.1:8110
BENCH_EPMCP_ADDR ?= :8111
BENCH_EPMCP_URL ?= http://127.0.0.1:8111
BENCH_APISVC_KEY ?= bench-fixture-key
BENCH_APISVC_PID := build/bench-apisvc.pid
BENCH_APISVC_LOG := build/bench-apisvc.log
BENCH_EPMCP_PID := build/bench-epmcp.pid
BENCH_EPMCP_LOG := build/bench-epmcp.log
BENCH_EMBED_WAIT ?= 60m
# The b* arms use their own database (mcp_bench_api) so bench state and
# migration version never collide with a dev/e2e mcp_platform database
# migrated by another branch. Created idempotently at bench-api-up.
BENCH_PG_CONTAINER ?= e2e-postgres

## bench-api-gen: Regenerate the API study's specs and task set from the fixed seeds
bench-api-gen:
	@cd bench && $(GO) run ./apigen -specs-dir specs -tasks-dir tasks-api

## bench-api-up: Start Postgres + fixture service (+epmcp for b0) + platform, then register fixtures (BENCH_ARM=b0|b1-lex|b1-hyb|b2, BENCH_TIER=t0|t1|t2). b2 (code mode) starts only the fixture service: no platform, no MCP.
bench-api-up:
	@case "$(BENCH_ARM)" in b0|b1-lex|b1-hyb|b2) ;; \
		*) echo "ERROR: bench-api-up needs BENCH_ARM=b0|b1-lex|b1-hyb|b2 (got '$(BENCH_ARM)')"; exit 1 ;; esac
	@if [ "$(BENCH_PG)" = "skip" ]; then \
		echo "Using external Postgres on 5432 (BENCH_PG=skip)..."; \
	else \
		echo "Starting Postgres..."; \
		$(BENCH_COMPOSE) up -d postgres; \
		for i in $$(seq 1 30); do \
			if docker exec e2e-postgres pg_isready -U platform -d mcp_platform -q 2>/dev/null; then break; fi; \
			sleep 1; \
		done; \
		docker exec e2e-postgres pg_isready -U platform -d mcp_platform -q || { echo "ERROR: Postgres not ready"; exit 1; }; \
	fi
	@docker exec $(BENCH_PG_CONTAINER) psql -U platform -d postgres -tc \
		"SELECT 1 FROM pg_database WHERE datname='mcp_bench_api'" 2>/dev/null | grep -q 1 \
		|| docker exec $(BENCH_PG_CONTAINER) psql -U platform -d postgres -c "CREATE DATABASE mcp_bench_api OWNER platform"
	@echo "Building binaries..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-bench $(CMD_DIR)
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/bench-apisvc ./apisvc \
		&& $(GO) build -o ../$(BUILD_DIR)/bench-epmcp ./epmcp \
		&& $(GO) build -o ../$(BUILD_DIR)/bench-apisetup ./apisetup \
		&& $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@for pid in $(BENCH_APISVC_PID) $(BENCH_EPMCP_PID) $(BENCH_PID); do \
		if [ -f $$pid ]; then \
			kill $$(cat $$pid) 2>/dev/null || true; \
			while kill -0 $$(cat $$pid) 2>/dev/null; do sleep 1; done; \
			rm -f $$pid; \
		fi; \
	done
	@echo "Starting fixture service on $(BENCH_APISVC_ADDR)..."
	@$(BUILD_DIR)/bench-apisvc -addr $(BENCH_APISVC_ADDR) -api-key $(BENCH_APISVC_KEY) \
		> $(BENCH_APISVC_LOG) 2>&1 & echo $$! > $(BENCH_APISVC_PID)
	@for i in $$(seq 1 15); do \
		if curl -fsS -H "X-API-Key: $(BENCH_APISVC_KEY)" $(BENCH_APISVC_URL)/_bench/requests >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done; \
	curl -fsS -H "X-API-Key: $(BENCH_APISVC_KEY)" $(BENCH_APISVC_URL)/_bench/requests >/dev/null 2>&1 \
		|| { echo "ERROR: fixture service not ready; see $(BENCH_APISVC_LOG)"; tail -5 $(BENCH_APISVC_LOG); exit 1; }
	@if [ "$(BENCH_ARM)" = "b0" ]; then \
		echo "Starting per-endpoint MCP server ($(BENCH_TIER)) on $(BENCH_EPMCP_ADDR)..."; \
		$(BUILD_DIR)/bench-epmcp -addr $(BENCH_EPMCP_ADDR) -spec bench/specs/$(BENCH_TIER).json \
			-target $(BENCH_APISVC_URL) -api-key $(BENCH_APISVC_KEY) \
			> $(BENCH_EPMCP_LOG) 2>&1 & echo $$! > $(BENCH_EPMCP_PID); \
		for i in $$(seq 1 15); do \
			code=$$(curl -s -o /dev/null -w "%{http_code}" $(BENCH_EPMCP_URL) 2>/dev/null); \
			if [ "$$code" != "000" ]; then break; fi; \
			sleep 1; \
		done; \
		kill -0 $$(cat $(BENCH_EPMCP_PID)) 2>/dev/null \
			|| { echo "ERROR: epmcp exited; see $(BENCH_EPMCP_LOG)"; tail -5 $(BENCH_EPMCP_LOG); exit 1; }; \
	fi
	@if [ "$(BENCH_ARM)" = "b2" ]; then \
		echo "Code mode: no platform, no MCP — fixture service only."; \
	else \
		if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then \
			echo "ERROR: something else is already serving $(BENCH_URL); run 'make bench-api-down' first"; exit 1; fi; \
		echo "Starting platform ($(BENCH_CONFIG)) on $(BENCH_ADDR)..."; \
		API_KEY_ADMIN=$(BENCH_KEY) LOG_LEVEL=info OTEL_METRICS_ADDR=$(BENCH_METRICS_ADDR) \
			$(BUILD_DIR)/$(BINARY_NAME)-bench --config $(BENCH_CONFIG) --transport http --address $(BENCH_ADDR) \
			> $(BENCH_LOG) 2>&1 & echo $$! > $(BENCH_PID); \
		for i in $$(seq 1 30); do \
			if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then break; fi; \
			sleep 1; \
		done; \
		curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1 \
			|| { echo "ERROR: platform did not become ready; see $(BENCH_LOG)"; tail -20 $(BENCH_LOG); exit 1; }; \
		kill -0 $$(cat $(BENCH_PID)) 2>/dev/null \
			|| { echo "ERROR: bench platform exited after start; see $(BENCH_LOG)"; tail -20 $(BENCH_LOG); exit 1; }; \
		echo "Registering fixtures (arm $(BENCH_ARM), tier $(BENCH_TIER))..."; \
		case "$(BENCH_ARM)" in \
			b0) $(BUILD_DIR)/bench-apisetup -mode b0 -url $(BENCH_URL) -credential $(BENCH_KEY) \
				-epmcp $(BENCH_EPMCP_URL) ;; \
			b1-lex) $(BUILD_DIR)/bench-apisetup -mode b1 -url $(BENCH_URL) -credential $(BENCH_KEY) \
				-spec bench/specs/$(BENCH_TIER).json -fixture $(BENCH_APISVC_URL) -fixture-key $(BENCH_APISVC_KEY) ;; \
			b1-hyb) $(BUILD_DIR)/bench-apisetup -mode b1 -url $(BENCH_URL) -credential $(BENCH_KEY) \
				-spec bench/specs/$(BENCH_TIER).json -fixture $(BENCH_APISVC_URL) -fixture-key $(BENCH_APISVC_KEY) \
				-wait-embed $(BENCH_EMBED_WAIT) ;; \
		esac; \
	fi
	@echo "API study stack ready (arm $(BENCH_ARM), tier $(BENCH_TIER))."

## bench-api-run: Run the API-connection study benchmark against the running b* stack (BENCH_ARM, BENCH_TIER; LLM=, SCRIPT=, K=, MODEL=, SUITE=)
bench-api-run:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@dir="build/bench-results/api-$(BENCH_ARM)-$(BENCH_TIER)-$$(date +%Y%m%d-%H%M%S)"; \
	mkdir -p $$dir; \
	echo "Results dir: $$dir"; \
	$(BUILD_DIR)/benchrun \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-arm $(BENCH_ARM) \
		-tier $(BENCH_TIER) \
		-tasks bench/tasks-api \
		-fixture-url $(BENCH_APISVC_URL) \
		-fixture-key $(BENCH_APISVC_KEY) \
		-identity-keys 150 \
		-git-commit $$(git rev-parse HEAD) \
		-out $$dir/results.json \
		$(if $(filter b2,$(BENCH_ARM)),-code-spec bench/specs/$(BENCH_TIER).json,) \
		$(if $(LLM),-llm $(LLM),) \
		$(if $(SCRIPT),-script $(SCRIPT),) \
		$(if $(SUITE),-suite $(SUITE),) \
		$(if $(K),-k $(K),) \
		$(if $(MODEL),-model $(MODEL),)

## bench-api-smoke: Scripted (no-API-key) end-to-end smoke against the running b1 stack
bench-api-smoke:
	@$(MAKE) bench-api-run LLM=scripted SCRIPT=bench/tasks-api/scripted-smoke.json K=1

# --- Perishable-knowledge study (issue #1054) ----------------------------
# One arm (bench/config/platform.bench.pk.yaml), one spec (bench/specs/pk.json),
# and a fixture service serving the perishable surface. The world a cell starts
# in is BENCH_PK_WORLD; the harness changes it between sessions through the
# fixture's own control plane, not through a restart. Its own database
# (mcp_bench_pk) keeps study state off the #1027 and dev databases.
BENCH_PK_APISVC_ADDR ?= :8112
BENCH_PK_APISVC_URL ?= http://127.0.0.1:8112
BENCH_PK_APISVC_KEY ?= bench-pk-fixture-key
BENCH_PK_WORLD ?= monitors-0
BENCH_PK_CONFIG := bench/config/platform.bench.pk.yaml
BENCH_PK_APISVC_PID := build/bench-pk-apisvc.pid
BENCH_PK_APISVC_LOG := build/bench-pk-apisvc.log

## bench-pk-up: Start Postgres + the perishable fixture + platform, then register the fixture (#1054; BENCH_PK_WORLD=)
bench-pk-up:
	@if [ "$(BENCH_PG)" = "skip" ]; then \
		echo "Using external Postgres on 5432 (BENCH_PG=skip)..."; \
	else \
		echo "Starting Postgres..."; \
		$(BENCH_COMPOSE) up -d postgres; \
		for i in $$(seq 1 30); do \
			if docker exec e2e-postgres pg_isready -U platform -d mcp_platform -q 2>/dev/null; then break; fi; \
			sleep 1; \
		done; \
		docker exec e2e-postgres pg_isready -U platform -d mcp_platform -q || { echo "ERROR: Postgres not ready"; exit 1; }; \
	fi
	@docker exec $(BENCH_PG_CONTAINER) psql -U platform -d postgres -tc \
		"SELECT 1 FROM pg_database WHERE datname='mcp_bench_pk'" 2>/dev/null | grep -q 1 \
		|| docker exec $(BENCH_PG_CONTAINER) psql -U platform -d postgres -c "CREATE DATABASE mcp_bench_pk OWNER platform"
	@echo "Building binaries..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-bench $(CMD_DIR)
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/bench-apisvc ./apisvc \
		&& $(GO) build -o ../$(BUILD_DIR)/bench-apisetup ./apisetup \
		&& $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@for pid in $(BENCH_PK_APISVC_PID) $(BENCH_PID); do \
		if [ -f $$pid ]; then \
			kill $$(cat $$pid) 2>/dev/null || true; \
			while kill -0 $$(cat $$pid) 2>/dev/null; do sleep 1; done; \
			rm -f $$pid; \
		fi; \
	done
	@echo "Starting perishable fixture service on $(BENCH_PK_APISVC_ADDR) (world $(BENCH_PK_WORLD))..."
	@$(BUILD_DIR)/bench-apisvc -addr $(BENCH_PK_APISVC_ADDR) -api-key $(BENCH_PK_APISVC_KEY) \
		-surface perishable -world $(BENCH_PK_WORLD) \
		> $(BENCH_PK_APISVC_LOG) 2>&1 & echo $$! > $(BENCH_PK_APISVC_PID)
	@for i in $$(seq 1 15); do \
		if curl -fsS -H "X-API-Key: $(BENCH_PK_APISVC_KEY)" $(BENCH_PK_APISVC_URL)/_bench/world >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done; \
	curl -fsS -H "X-API-Key: $(BENCH_PK_APISVC_KEY)" $(BENCH_PK_APISVC_URL)/_bench/world >/dev/null 2>&1 \
		|| { echo "ERROR: fixture service not ready; see $(BENCH_PK_APISVC_LOG)"; tail -5 $(BENCH_PK_APISVC_LOG); exit 1; }
	@if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then \
		echo "ERROR: something else is already serving $(BENCH_URL); run 'make bench-pk-down' first"; exit 1; fi
	@echo "Starting platform ($(BENCH_PK_CONFIG)) on $(BENCH_ADDR)..."
	@API_KEY_ADMIN=$(BENCH_KEY) LOG_LEVEL=info OTEL_METRICS_ADDR=$(BENCH_METRICS_ADDR) \
		$(BUILD_DIR)/$(BINARY_NAME)-bench --config $(BENCH_PK_CONFIG) --transport http --address $(BENCH_ADDR) \
		> $(BENCH_LOG) 2>&1 & echo $$! > $(BENCH_PID)
	@for i in $$(seq 1 30); do \
		if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done; \
	curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1 \
		|| { echo "ERROR: platform did not become ready; see $(BENCH_LOG)"; tail -20 $(BENCH_LOG); exit 1; }
	@echo "Registering the perishable fixture..."
	@$(BUILD_DIR)/bench-apisetup -mode b1 -url $(BENCH_URL) -credential $(BENCH_KEY) \
		-spec bench/specs/pk.json -fixture $(BENCH_PK_APISVC_URL) -fixture-key $(BENCH_PK_APISVC_KEY)
	@echo "Perishable-knowledge stack ready (world $(BENCH_PK_WORLD))."

## bench-pk-corpus: Run the capture-corpus episodes against the running pk stack (#1054 stage 1; REPLICATES=, MODEL=)
bench-pk-corpus:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/bench-pkcorpus ./pkcorpus
	@dir="build/bench-results/pk-corpus-$$(date +%Y%m%d-%H%M%S)"; \
	mkdir -p $$dir; \
	echo "Corpus dir: $$dir"; \
	$(BUILD_DIR)/bench-pkcorpus \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-fixture-url $(BENCH_PK_APISVC_URL) \
		-fixture-key $(BENCH_PK_APISVC_KEY) \
		-identity-keys 150 \
		-git-commit $$(git rev-parse HEAD) \
		-out $$dir \
		$(if $(REPLICATES),-replicates $(REPLICATES),) \
		$(if $(MODEL),-model $(MODEL),)

## bench-pk-down: Stop the perishable-knowledge stack (platform, fixture service, compose)
bench-pk-down:
	@for pid in $(BENCH_PID) $(BENCH_PK_APISVC_PID); do \
		if [ -f $$pid ]; then \
			kill $$(cat $$pid) 2>/dev/null || true; \
			rm -f $$pid; \
		fi; \
	done
	@$(MAKE) e2e-down

## bench-api-down: Stop the API study stack (platform, epmcp, fixture service, compose)
bench-api-down:
	@for pid in $(BENCH_PID) $(BENCH_EPMCP_PID) $(BENCH_APISVC_PID); do \
		if [ -f $$pid ]; then \
			kill $$(cat $$pid) 2>/dev/null || true; \
			rm -f $$pid; \
		fi; \
	done
	@$(MAKE) e2e-down

## bench-up: Start the compose stack, seed the bench warehouse, and run the platform (BENCH_ARM=a0|a1|a2|a3)
bench-up: e2e-up
	@echo "Seeding bench warehouse in Trino..."
	@$(BENCH_COMPOSE) cp bench/seed/trino/setup.sql trino:/tmp/bench-setup.sql
	@$(BENCH_COMPOSE) exec -T trino trino --file /tmp/bench-setup.sql
	@echo "Building release-style platform binary (no -race)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-bench $(CMD_DIR)
	@echo "Building benchrun..."
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@if [ -f $(BENCH_PID) ]; then \
		echo "Stopping previous bench platform (pid $$(cat $(BENCH_PID)))..."; \
		kill $$(cat $(BENCH_PID)) 2>/dev/null || true; \
		while kill -0 $$(cat $(BENCH_PID)) 2>/dev/null; do sleep 1; done; \
		rm -f $(BENCH_PID); \
	fi
	@if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then \
		echo "ERROR: something else is already serving $(BENCH_URL); run 'make bench-down' first"; exit 1; fi
	@echo "Starting platform ($(BENCH_CONFIG)) on $(BENCH_ADDR)..."
	@API_KEY_ADMIN=$(BENCH_KEY) LOG_LEVEL=info OTEL_METRICS_ADDR=$(BENCH_METRICS_ADDR) \
		$(BUILD_DIR)/$(BINARY_NAME)-bench --config $(BENCH_CONFIG) --transport http --address $(BENCH_ADDR) \
		> $(BENCH_LOG) 2>&1 & echo $$! > $(BENCH_PID)
	@echo "Waiting for readiness on $(BENCH_URL)/readyz ..."
	@for i in $$(seq 1 30); do \
		if curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done; \
	if ! curl -fsS $(BENCH_URL)/readyz >/dev/null 2>&1; then \
		echo "ERROR: platform did not become ready; see $(BENCH_LOG)"; tail -20 $(BENCH_LOG); exit 1; fi; \
	if ! kill -0 $$(cat $(BENCH_PID)) 2>/dev/null; then \
		echo "ERROR: bench platform exited after start (another server answered readiness?); see $(BENCH_LOG)"; \
		tail -20 $(BENCH_LOG); exit 1; fi
	@if [ "$(BENCH_SEED_PAGES)" = "0" ]; then \
		echo "Skipping knowledge-page seeding (BENCH_SEED_PAGES=0, cold-start empty baseline)."; \
	else \
		echo "Seeding knowledge pages (requires platform migrations, just applied on boot)..."; \
		$(BENCH_COMPOSE) exec -T postgres psql -q -U platform -d mcp_platform -v ON_ERROR_STOP=1 \
			< bench/seed/postgres/knowledge_pages.sql; \
	fi
	@echo "Platform ready (pid $$(cat $(BENCH_PID)), arm $(BENCH_ARM))."

## bench-seed-datahub: Push bench metadata into a running DataHub quickstart (a2 arm)
BENCH_DATAHUB_GMS ?= http://localhost:8080
bench-seed-datahub:
	@command -v datahub >/dev/null 2>&1 || { echo "ERROR: datahub CLI not found (pip install acryl-datahub)"; exit 1; }
	@mkdir -p $(BUILD_DIR)
	@printf 'source:\n  type: file\n  config:\n    path: %s/bench/seed/datahub/bench_mces.json\nsink:\n  type: datahub-rest\n  config:\n    server: %s\n' "$$(pwd)" "$(BENCH_DATAHUB_GMS)" > $(BUILD_DIR)/bench-datahub-recipe.yml
	datahub ingest -c $(BUILD_DIR)/bench-datahub-recipe.yml

## bench-seed-datahub-empty: Push the cold-start empty baseline into DataHub (entities present, undocumented; issue #963)
bench-seed-datahub-empty:
	@command -v datahub >/dev/null 2>&1 || { echo "ERROR: datahub CLI not found (pip install acryl-datahub)"; exit 1; }
	@mkdir -p $(BUILD_DIR)
	@printf 'source:\n  type: file\n  config:\n    path: %s/bench/seed/datahub/bench_mces_empty.json\nsink:\n  type: datahub-rest\n  config:\n    server: %s\n' "$$(pwd)" "$(BENCH_DATAHUB_GMS)" > $(BUILD_DIR)/bench-datahub-empty-recipe.yml
	datahub ingest -c $(BUILD_DIR)/bench-datahub-empty-recipe.yml

## bench-run: Run the benchmark (ARM must match bench-up; LLM=anthropic|scripted|claude-cli, SUITE=, K=, MODEL=)
bench-run:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@echo "Resetting search-first gate state (discovery scopes persist in Postgres across runs)..."
	@$(BENCH_COMPOSE) exec -T postgres psql -q -U platform -d mcp_platform -v ON_ERROR_STOP=1 \
		-c "TRUNCATE search_gate_discovery"
	$(BUILD_DIR)/benchrun \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-arm $(BENCH_ARM) \
		-tasks bench/tasks \
		-git-commit $$(git rev-parse HEAD) \
		-out build/bench-results/results-$(BENCH_ARM).json \
		$(if $(LLM),-llm $(LLM),) \
		$(if $(SCRIPT),-script $(SCRIPT),) \
		$(if $(SUITE),-suite $(SUITE),) \
		$(if $(K),-k $(K),) \
		$(if $(MODEL),-model $(MODEL),) \
		$(if $(BASELINE),-baseline $(BASELINE),)

## bench-smoke: Run the scripted (no-API-key) smoke against the running platform
bench-smoke:
	@$(MAKE) bench-run LLM=scripted SCRIPT=bench/tasks/scripted-smoke.json K=1

## bench-lifecycle: Run the S5 memory-insight-knowledge lifecycle protocols into a fresh per-run dir under build/bench-results/ (needs bench-up BENCH_ARM=a3; LLM=anthropic|scripted|claude-cli, K=, MODEL=, BASELINE=)
bench-lifecycle:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@echo "Resetting search-first gate state (discovery scopes persist in Postgres across runs)..."
	@$(BENCH_COMPOSE) exec -T postgres psql -q -U platform -d mcp_platform -v ON_ERROR_STOP=1 \
		-c "TRUNCATE search_gate_discovery"
	@out_dir="build/bench-results/lifecycle-a3-$$(date +%Y%m%d-%H%M%S)-$$$$"; \
	mkdir "$$out_dir"; \
	echo "Lifecycle results dir: $$out_dir (each run gets its own dir; nothing is ever overwritten)"; \
	$(BUILD_DIR)/benchrun \
		-lifecycle \
		-arm a3 \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-protocols bench/protocols \
		-git-commit $$(git rev-parse HEAD) \
		-out "$$out_dir/lifecycle-a3.json" \
		$(if $(LLM),-llm $(LLM),) \
		$(if $(SCRIPT),-script $(SCRIPT),) \
		$(if $(K),-k $(K),) \
		$(if $(MODEL),-model $(MODEL),) \
		$(if $(BASELINE),-baseline $(BASELINE),)

## bench-lifecycle-smoke: Run the scripted (no-API-key) lifecycle smoke against the running a3 platform
bench-lifecycle-smoke:
	@$(MAKE) bench-lifecycle LLM=scripted SCRIPT=bench/protocols/scripted-lifecycle-smoke.json K=1

## bench-lifecycle-report: Print the human summary of a lifecycle run (RESULTS=<run dir>/lifecycle-a3.json)
bench-lifecycle-report:
	@if [ -z "$(RESULTS)" ]; then \
		echo "ERROR: set RESULTS=<path to a lifecycle results JSON>. Available run dirs:"; \
		ls -d build/bench-results/lifecycle-a3-*/ 2>/dev/null || echo "  (none under build/bench-results/)"; \
		exit 1; fi
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	$(BUILD_DIR)/benchrun -lifecycle -summarize $(RESULTS)

## bench-supersede: Run the isolated supersede sub-benchmark into a fresh per-run dir under build/bench-results/ (issue #964; needs bench-up BENCH_ARM=a3; LLM=anthropic|scripted|claude-cli, K=, MODEL=, TEACH_BUDGET=)
bench-supersede:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@echo "Resetting search-first gate state (discovery scopes persist in Postgres across runs)..."
	@$(BENCH_COMPOSE) exec -T postgres psql -q -U platform -d mcp_platform -v ON_ERROR_STOP=1 \
		-c "TRUNCATE search_gate_discovery"
	@out_dir="build/bench-results/supersede-a3-$$(date +%Y%m%d-%H%M%S)-$$$$"; \
	mkdir "$$out_dir"; \
	echo "Supersede results dir: $$out_dir (each run gets its own dir; nothing is ever overwritten)"; \
	$(BUILD_DIR)/benchrun \
		-supersede \
		-arm a3 \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-protocols bench/protocols \
		-git-commit $$(git rev-parse HEAD) \
		-out "$$out_dir/supersede-a3.json" \
		$(if $(LLM),-llm $(LLM),) \
		$(if $(SCRIPT),-script $(SCRIPT),) \
		$(if $(K),-k $(K),) \
		$(if $(MODEL),-model $(MODEL),) \
		$(if $(TEACH_BUDGET),-teach-budget $(TEACH_BUDGET),)

## bench-supersede-smoke: Run the scripted (no-API-key) supersede sub-benchmark against the running a3 platform
bench-supersede-smoke:
	@$(MAKE) bench-supersede LLM=scripted SCRIPT=bench/protocols/scripted-lifecycle-smoke.json K=1

## bench-supersede-report: Print the human summary of a supersede run (RESULTS=<run dir>/supersede-a3.json)
bench-supersede-report:
	@if [ -z "$(RESULTS)" ]; then \
		echo "ERROR: set RESULTS=<path to a supersede results JSON>. Available run dirs:"; \
		ls -d build/bench-results/supersede-a3-*/ 2>/dev/null || echo "  (none under build/bench-results/)"; \
		exit 1; fi
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	$(BUILD_DIR)/benchrun -supersede -summarize $(RESULTS)

## bench-cold-start: Run the cold-start knowledge-growth curriculum into a fresh per-run dir under build/bench-results/ (issue #963; needs an empty-seeded a3: bench-up BENCH_ARM=a3 BENCH_SEED_PAGES=0 + bench-seed-datahub-empty on a FRESH DataHub quickstart; LLM=anthropic|scripted|claude-cli, K=, MODEL=, SETTLE=)
bench-cold-start:
	@mkdir -p build/bench-results
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@echo "Resetting cold-start state so the baseline is truly empty (search gate, prior insights/changesets, and any promoted knowledge pages persist in Postgres across runs)..."
	@echo "  (CASCADE also clears portal_threads, which FK-references knowledge pages; the bench stack is disposable scratch state.)"
	@$(BENCH_COMPOSE) exec -T postgres psql -q -U platform -d mcp_platform -v ON_ERROR_STOP=1 \
		-c "TRUNCATE search_gate_discovery, memory_records, knowledge_changesets, portal_knowledge_pages CASCADE"
	@out_dir="build/bench-results/cold-start-a3-$$(date +%Y%m%d-%H%M%S)-$$$$"; \
	mkdir "$$out_dir"; \
	echo "Cold-start results dir: $$out_dir (each run gets its own dir; nothing is ever overwritten)"; \
	$(BUILD_DIR)/benchrun \
		-cold-start \
		-arm a3 \
		-url $(BENCH_URL) \
		-credential $(BENCH_KEY) \
		-curriculum bench/curriculum \
		-tasks bench/tasks \
		-git-commit $$(git rev-parse HEAD) \
		-out "$$out_dir/results.json" \
		$(if $(LLM),-llm $(LLM),) \
		$(if $(SCRIPT),-script $(SCRIPT),) \
		$(if $(K),-k $(K),) \
		$(if $(MODEL),-model $(MODEL),) \
		$(if $(SETTLE),-settle $(SETTLE),)

## bench-cold-start-smoke: Run the scripted (no-API-key) cold-start smoke against the running a3 platform (no cache-settle pause)
bench-cold-start-smoke:
	@$(MAKE) bench-cold-start LLM=scripted SCRIPT=bench/curriculum/scripted-cold-start-smoke.json K=1 SETTLE=0s

## bench-cold-start-report: Print the human summary (learning curve) of a cold-start run (RESULTS=<run dir>/results.json)
bench-cold-start-report:
	@if [ -z "$(RESULTS)" ]; then \
		echo "ERROR: set RESULTS=<path to a cold-start results.json>. Available run dirs:"; \
		ls -d build/bench-results/cold-start-a3-*/ 2>/dev/null || echo "  (none under build/bench-results/)"; \
		exit 1; fi
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	$(BUILD_DIR)/benchrun -cold-start -summarize $(RESULTS)

## bench-report: Print the human summary of the last run for BENCH_ARM
bench-report:
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	$(BUILD_DIR)/benchrun -summarize build/bench-results/results-$(BENCH_ARM).json

## bench-compare: Render the cross-arm comparison (arm-by-suite tables, bootstrap CIs) from all per-arm results
BENCH_COMPARE_OUT ?= build/bench-results/comparison.md
bench-compare:
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	@files=$$(ls build/bench-results/results-*.json 2>/dev/null | paste -sd, -); \
	if [ -z "$$files" ]; then echo "ERROR: no build/bench-results/results-*.json to compare"; exit 1; fi; \
	$(BUILD_DIR)/benchrun -compare "$$files" -compare-out $(BENCH_COMPARE_OUT)

## bench-report-pdf: Render the benchmark report to PDF + HTML in build/report/ (needs pandoc + tectonic; not part of verify)
bench-report-pdf:
	@bash bench/report/render-report.sh

## bench-calibrate: Run the judge calibration and print its human-agreement rate (needs ANTHROPIC_API_KEY; uses the rubric's pinned model)
bench-calibrate:
	@cd bench && $(GO) build -o ../$(BUILD_DIR)/benchrun ./benchrun
	$(BUILD_DIR)/benchrun -calibrate -rubric bench/judge/rubric.yaml -calibration bench/judge/calibration.yaml

## bench-down: Stop the bench platform and the compose stack
bench-down:
	@if [ -f $(BENCH_PID) ]; then \
		echo "Stopping platform (pid $$(cat $(BENCH_PID)))..."; \
		kill $$(cat $(BENCH_PID)) 2>/dev/null || true; \
		rm -f $(BENCH_PID); \
	fi
	@$(MAKE) e2e-down

## bench-test: Build, vet, and unit-test the benchmark module itself
bench-test:
	@echo "Testing the benchmark module..."
	@cd bench && $(GO) build ./... && $(GO) vet ./... && $(GO) test ./...

## bench-lint: Full-module lint of the bench/ harness
##
## Mirrors CI's "Harness module checks" job, which runs golangci-lint over the
## whole bench module with NO only-new-issues scoping — unlike the main-module
## `lint` target, whose --new-from-patch scoping cannot see a finding that
## anchors on an unchanged line (e.g. a gocognit report on a func declaration
## whose body grew). bench/ is a separate Go module, so the root `lint` never
## reaches it at all; without this target a bench lint finding surfaces only
## in CI (PR #978's gocognit failure was exactly this gap).
bench-lint:
	@echo "Linting the benchmark module (full module, matching CI's harness job)..."
	@cd bench && $(GOLINT) run ./...
