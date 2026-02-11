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

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := gofmt
GOLINT := golangci-lint

.PHONY: all build test lint fmt clean install help docs-serve docs-build verify \
	dead-code mutate patch-coverage doc-check swagger swagger-check \
	semgrep codeql sast \
	e2e-up e2e-down e2e-seed e2e-test e2e e2e-logs e2e-clean

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

## security: Run security checks (gosec blocks, govulncheck is informational)
security:
	@echo "Running gosec..."
	@which gosec > /dev/null || (echo "Installing gosec..." && go install github.com/securego/gosec/v2/cmd/gosec@latest)
	$(shell go env GOPATH)/bin/gosec -quiet ./...
	@echo "Running govulncheck (informational)..."
	@which govulncheck > /dev/null || (echo "Installing govulncheck..." && go install golang.org/x/vuln/cmd/govulncheck@latest)
	@$(shell go env GOPATH)/bin/govulncheck ./... || echo "NOTE: govulncheck found issues — review above (stdlib vulns require Go upgrade)"

## semgrep: Run Semgrep SAST with standard and custom rules
semgrep:
	@echo "Running Semgrep..."
	@which semgrep > /dev/null || (echo "Installing semgrep..." && pip3 install semgrep --quiet)
	semgrep scan --config p/golang --config .semgrep/ --error --quiet .

## codeql: Run CodeQL analysis (requires codeql CLI)
codeql:
	@echo "Running CodeQL analysis..."
	@which codeql > /dev/null || (echo "ERROR: codeql CLI not found. Install: brew install codeql"; exit 1)
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
	@which deadcode > /dev/null || (echo "Installing deadcode..." && go install golang.org/x/tools/cmd/deadcode@latest)
	@OUTPUT=$$($(shell go env GOPATH)/bin/deadcode ./... 2>&1 | grep -v "^$$") || true; \
	if [ -n "$$OUTPUT" ]; then \
		echo "Dead code detected (review for false positives):"; \
		echo "$$OUTPUT"; \
	else \
		echo "No dead code found."; \
	fi

## mutate: Run mutation testing with 60% efficacy threshold
mutate:
	@echo "Running mutation testing..."
	@which gremlins > /dev/null || (echo "Installing gremlins..." && go install github.com/go-gremlins/gremlins/cmd/gremlins@latest)
	$(shell go env GOPATH)/bin/gremlins unleash --workers 1 --timeout-coefficient 3 --threshold-efficacy 60 ./pkg/...

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
	@which swag > /dev/null || (echo "Installing swag..." && go install github.com/swaggo/swag/cmd/swag@latest)
	$(shell go env GOPATH)/bin/swag init --generalInfo pkg/admin/handler.go --dir . --output internal/apidocs --parseDependency
	@echo "Swagger docs generated in internal/apidocs/"

## swagger-check: Verify Swagger docs are up to date
swagger-check: swagger
	@if git diff --quiet internal/apidocs/; then \
		echo "Swagger docs are up to date"; \
	else \
		echo "ERROR: Swagger docs are out of date. Run 'make swagger' and commit."; \
		exit 1; \
	fi

## verify: Run the full CI-equivalent check suite (test, lint, security, SAST, coverage, mutation, release)
verify: fmt swagger-check test lint security semgrep codeql coverage-report patch-coverage doc-check dead-code mutate release-check
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
