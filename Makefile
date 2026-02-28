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
ADMIN_UI_DIR := ./admin-ui
ADMIN_UI_EMBED_DIR := ./internal/adminui/dist

# Tool versions — keep in sync with .github/workflows/ci.yml
GOLANGCI_LINT_VERSION := v2.8.0
GOSEC_VERSION := v2.22.0

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := gofmt
GOLINT := golangci-lint

.PHONY: all build test lint fmt clean install help docs-serve docs-build verify \
	tools-check dead-code mutate patch-coverage doc-check swagger swagger-check \
	semgrep codeql sast embed-clean \
	frontend-install frontend-build frontend-dev frontend-test frontend-storybook \
	e2e-up e2e-down e2e-seed e2e-test e2e e2e-logs e2e-clean \
	dev-up dev-down preview-apps preview-platform-info

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

## coverage: Generate coverage report
coverage: test
	@echo "Generating coverage report..."
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run linter (full + patch-scoped to match CI)
lint:
	@echo "Running linter..."
	$(GOLINT) run ./...
	@echo "Running patch-scoped lint (matches CI only-new-issues)..."
	@MERGE_BASE=$$(git merge-base main HEAD 2>/dev/null) || true; \
	if [ -n "$$MERGE_BASE" ] && [ "$$MERGE_BASE" != "$$(git rev-parse HEAD)" ]; then \
		$(GOLINT) run --new-from-rev=$$MERGE_BASE ./...; \
	fi

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
	@rm -rf $(ADMIN_UI_DIR)/dist $(ADMIN_UI_DIR)/node_modules
	@# Reset embed dir but keep .gitkeep
	@find $(ADMIN_UI_EMBED_DIR) -not -name '.gitkeep' -not -path $(ADMIN_UI_EMBED_DIR) -delete 2>/dev/null || true
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
	@ISSUES=$$(python3 -c "import json,sys; d=json.load(open('codeql-results.sarif')); \
		print(sum(1 for run in d.get('runs',[]) for r in run.get('results',[]) \
		if r.get('level','note')=='error'))" 2>/dev/null || echo 0); \
	if [ "$$ISSUES" -gt 0 ]; then \
		echo "FAIL: CodeQL found $$ISSUES error-level issues. See codeql-results.sarif for details."; \
		exit 1; \
	else \
		echo "CodeQL: no error-level issues found."; \
	fi

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
	swag init --generalInfo pkg/admin/handler.go --dir . --output internal/apidocs --parseDependency
	@echo "Swagger docs generated in internal/apidocs/"

## swagger-check: Verify Swagger docs are up to date
swagger-check: swagger
	@if git diff --quiet internal/apidocs/; then \
		echo "Swagger docs are up to date"; \
	else \
		echo "ERROR: Swagger docs are out of date. Run 'make swagger' and commit."; \
		exit 1; \
	fi

## tools-check: Verify all required tools are installed before running verify
tools-check:
	@echo "Checking required tools..."
	@missing=""; \
	which golangci-lint > /dev/null 2>&1 || missing="$$missing  golangci-lint: go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)\n"; \
	which gosec > /dev/null 2>&1         || missing="$$missing  gosec: go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)\n"; \
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
		printf "$$missing"; \
		echo ""; \
		echo "Install all missing tools before running make verify."; \
		exit 1; \
	fi
	@echo "All required tools found."

## embed-clean: Reset admin UI embed dir to .gitkeep only (matches CI clean checkout)
embed-clean:
	@echo "Cleaning admin UI embed directory..."
	@find $(ADMIN_UI_EMBED_DIR) -not -name '.gitkeep' -not -path $(ADMIN_UI_EMBED_DIR) -delete 2>/dev/null || true

## verify: Run the full CI-equivalent check suite (test, lint, security, SAST, coverage, mutation, release)
verify: tools-check fmt swagger-check embed-clean test lint security semgrep codeql coverage-report patch-coverage doc-check dead-code mutate release-check
	@echo ""
	@echo "=== All checks passed ==="

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
# Admin UI Frontend Targets
# =============================================================================

## frontend-install: Install admin UI dependencies
frontend-install:
	@echo "Installing admin UI dependencies..."
	cd $(ADMIN_UI_DIR) && npm ci
	@echo "Admin UI dependencies installed."

## frontend-build: Build admin UI and copy to embed directory
frontend-build: frontend-install
	@echo "Building admin UI..."
	cd $(ADMIN_UI_DIR) && npm run build
	@echo "Copying dist to embed directory..."
	@rm -rf $(ADMIN_UI_EMBED_DIR)/*
	@cp -r $(ADMIN_UI_DIR)/dist/* $(ADMIN_UI_EMBED_DIR)/
	@rm -f $(ADMIN_UI_EMBED_DIR)/mockServiceWorker.js
	@echo "Admin UI built and embedded."

## frontend-dev: Run admin UI dev server (hot reload)
frontend-dev:
	cd $(ADMIN_UI_DIR) && npm run dev

## frontend-test: Run admin UI tests
frontend-test:
	cd $(ADMIN_UI_DIR) && npm run test

## frontend-storybook: Run Storybook for component development
frontend-storybook:
	cd $(ADMIN_UI_DIR) && npm run storybook

## build-with-ui: Build Go binary with embedded admin UI
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
	@echo "Start the admin UI:"
	@echo "  cd admin-ui && npm run dev"
	@echo ""
	@echo "Or use MSW mode (no backend needed):"
	@echo "  cd admin-ui && VITE_MSW=true npm run dev"
	@echo ""
	@echo "API Key: acme-dev-key-2024"
	@echo ""

## dev-down: Stop ACME dev environment and remove volumes
dev-down:
	@echo "Stopping ACME dev environment..."
	$(DEV_COMPOSE) down -v
	@echo "ACME dev environment stopped."

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
