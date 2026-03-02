# Meridian - Production-Grade Open Banking Ledger
# Makefile for build, test, and deployment automation

# Variables
BINARY_NAME=meridian
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

# Directories
SERVICES_DIR=./services
SHARED_DIR=./shared
UTILITIES_DIR=./utilities
DIST_DIR=./dist
COVERAGE_DIR=./coverage

# Dev environment
DEV_COMPOSE=deploy/dev/docker-compose.yml

# Tools
GOLANGCI_LINT=golangci-lint
BUF=$(shell go env GOPATH)/bin/buf
TILT=tilt

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod
GOGET=$(GOCMD) get
GOFMT=$(GOCMD) fmt

.PHONY: all help build seed-dev seed-dev-build test lint clean proto proto-v1 proto-v2 proto-openapi proto-lint proto-breaking proto-descriptors docker deploy-local fmt tidy deps coverage install proto-validate proto-deps-update proto-deps-graph proto-plugins-info validate-tilt validate-semconv validate-sagas proto-jsonschema validate-manifest-jsonschema validate-manifests control-plane-ci test-control-plane migrate-diff-all migrate-diff-current migrate-diff-position migrate-apply-all migrate-status-all migrate-lint-all migrate-hash-all migrate-apply-orgs migrate-status-orgs docs generate-saga-docs swagger-split swagger-ui dev-up dev-down dev-clean asyncapi

# Default target
all: help

## help: Display this help message
help:
	@echo "Meridian - Production-Grade Open Banking Ledger"
	@echo ""
	@echo "Available targets:"
	@echo "  make build             - Compile all Go services"
	@echo "  make seed-dev-build    - Build the seed-dev binary"
	@echo "  make seed-dev          - Build and run seed-dev against local dev environment"
	@echo "  make test              - Run tests with coverage"
	@echo "  make lint              - Run golangci-lint, validate Tiltfile, and validate semconv versions"
	@echo "  make validate-tilt     - Validate Tiltfile configuration"
	@echo "  make validate-sagas    - Validate all saga scripts in the codebase"
	@echo "  make fmt               - Format Go code"
	@echo "  make clean             - Remove build artifacts"
	@echo "  make proto             - Generate code from all protobuf versions (includes OpenAPI)"
	@echo "  make proto-v1          - Generate code from v1 protobuf definitions"
	@echo "  make proto-v2          - Generate code from v2 protobuf definitions (future)"
	@echo "  make proto-openapi     - Show location of generated OpenAPI spec"
	@echo "  make proto-descriptors - Build compiled FileDescriptorSet for Vanguard transcoder"
	@echo "  make proto-validate    - Validate protobuf directory structure"
	@echo "  make proto-lint        - Lint protobuf files with buf"
	@echo "  make proto-breaking    - Check for breaking proto changes"
	@echo "  make proto-deps-update - Update buf.lock with latest dependencies"
	@echo "  make proto-deps-graph  - Display dependency graph"
	@echo "  make proto-plugins-info - Display current protoc plugin versions"
	@echo "  make proto-jsonschema  - Generate JSON Schema from manifest proto"
	@echo "  make validate-manifest-jsonschema - Validate JSON Schema is in sync with proto"
	@echo "  make validate-manifests - Validate example manifests against schema"
	@echo "  make control-plane-ci  - Run full control plane CI pipeline"
	@echo "  make test-control-plane - Run control plane unit tests"
	@echo "  make docker            - Build Docker images"
	@echo "  make deploy-local      - Deploy to local Kubernetes using Tilt"
	@echo "  make coverage          - Generate and open HTML coverage report"
	@echo "  make tidy              - Tidy and verify Go modules"
	@echo "  make deps              - Download dependencies"
	@echo "  make install           - Install development tools"
	@echo ""
	@echo "Database Migration targets:"
	@echo "  make migrate-diff-all          - Generate migrations for all schemas"
	@echo "  make migrate-diff-current      - Generate migration for current_account schema"
	@echo "  make migrate-diff-position     - Generate migration for position_keeping schema"
	@echo "  make migrate-apply-all         - Apply all pending migrations"
	@echo "  make migrate-status-all        - Show migration status for all schemas"
	@echo "  make migrate-lint-all          - Lint all migrations"
	@echo "  make migrate-hash-all          - Verify all migration checksums"
	@echo "  make migrate-apply-orgs        - Apply migrations to all organization schemas"
	@echo "  make migrate-status-orgs       - Show migration status for all organization schemas"
	@echo ""
	@echo "Documentation targets:"
	@echo "  make generate-saga-docs        - Generate Markdown and JSON Schema docs for saga handlers"
	@echo ""
	@echo "API Explorer targets:"
	@echo "  make swagger-split             - Split monolithic swagger into per-service files"
	@echo "  make swagger-ui                - Start local Swagger UI server (requires Tilt running)"
	@echo ""
	@echo "Dev environment targets:"
	@echo "  make dev-up                    - Start entire Meridian platform in dev mode"
	@echo "  make dev-down                  - Stop dev environment containers"
	@echo "  make dev-clean                 - Stop dev environment and delete all data volumes"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
	@echo "  COMMIT=$(COMMIT)"
	@echo "  BUILD_DATE=$(BUILD_DATE)"

## build: Compile all Go services
build: tidy
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(DIST_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME) $(UTILITIES_DIR)/meridian
	$(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/atlas-loader $(UTILITIES_DIR)/atlas-loader
	@echo "Build complete:"
	@echo "  - $(DIST_DIR)/$(BINARY_NAME)"
	@echo "  - $(DIST_DIR)/atlas-loader"

## seed-dev-build: Build the seed-dev binary
seed-dev-build:
	@echo "Building seed-dev binary..."
	@mkdir -p bin
	$(GOBUILD) -o bin/seed-dev ./cmd/seed-dev
	@echo "Binary written to bin/seed-dev"

## seed-dev: Seed the local dev tenant with manifest configuration
seed-dev: seed-dev-build
	@./scripts/seed-dev-tenant.sh

## test: Run tests with coverage
test:
	@echo "Running tests with coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v -short -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	@echo "Filtering generated proto files from coverage..."
	@grep -v -E '\.pb\.go|\.pb\.validate\.go|_grpc\.pb\.go' $(COVERAGE_DIR)/coverage.out > $(COVERAGE_DIR)/coverage-filtered.out || true
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage-filtered.out | tail -n 1
	@echo "Coverage report (excluding generated files): $(COVERAGE_DIR)/coverage-filtered.out"
	@echo "Full coverage report: $(COVERAGE_DIR)/coverage.out"

## coverage: Generate and open HTML coverage report
coverage: test
	@echo "Generating HTML coverage report..."
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "Opening coverage report..."
	@open $(COVERAGE_DIR)/coverage.html 2>/dev/null || xdg-open $(COVERAGE_DIR)/coverage.html 2>/dev/null || echo "Please open $(COVERAGE_DIR)/coverage.html manually"

## validate-sagas: Validate all saga scripts in the codebase
validate-sagas:
	@echo "Discovering and validating all saga scripts..."
	@SCRIPT_COUNT=0; \
	FAILED_COUNT=0; \
	PROJECT_ROOT=$$(pwd); \
	for SCRIPT in $$(find services -type f -name "*.star" \( -path "*/sagas/*" -o -path "*/saga/defaults/*" \) 2>/dev/null | sort); do \
		SCRIPT_COUNT=$$((SCRIPT_COUNT + 1)); \
		echo "Validating: $$SCRIPT"; \
		SCRIPT_ABS="$$PROJECT_ROOT/$$SCRIPT"; \
		if ! $(GOTEST) -run TestValidateSagaScript_ProductionScript ./shared/pkg/saga -args -script="$$SCRIPT_ABS"; then \
			FAILED_COUNT=$$((FAILED_COUNT + 1)); \
		fi; \
	done; \
	echo ""; \
	if [ $$SCRIPT_COUNT -eq 0 ]; then \
		echo "ℹ️  No saga scripts found in services/ directories"; \
		echo "This is expected if no .star files exist yet in sagas/ or saga/defaults/ paths"; \
	else \
		echo "Validated $$SCRIPT_COUNT script(s)"; \
		if [ $$FAILED_COUNT -gt 0 ]; then \
			echo "❌ $$FAILED_COUNT script(s) failed validation"; \
			exit 1; \
		else \
			echo "✅ All scripts passed validation"; \
		fi; \
	fi

## lint: Run golangci-lint and validate Tiltfile
lint: validate-tilt validate-semconv
	@echo "Running golangci-lint..."
	@which $(GOLANGCI_LINT) > /dev/null || (echo "golangci-lint not installed. Run 'make install'"; exit 1)
	$(GOLANGCI_LINT) run --timeout 5m ./...

## validate-tilt: Validate Tiltfile configuration
validate-tilt:
	@./scripts/validate-tiltfile.sh Tiltfile

## validate-semconv: Validate OpenTelemetry semantic convention versions are consistent
validate-semconv:
	@./scripts/validate-semconv-versions.sh

## fmt: Format Go code
fmt:
	@echo "Formatting Go code..."
	$(GOFMT) ./...
	@echo "Code formatted successfully"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	@rm -rf $(DIST_DIR)
	@rm -rf $(COVERAGE_DIR)
	@rm -f *.test
	@rm -f coverage.out
	@echo "Clean complete"

## proto: Generate code from all protobuf versions
proto: proto-v1 proto-descriptors
	@echo "All protobuf versions generated successfully"

## proto-v1: Generate code from v1 protobuf definitions
proto-v1: proto-validate
	@echo "Generating code from v1 protobuf definitions..."
	@which $(BUF) > /dev/null || (echo "buf not installed. Run 'go install github.com/bufbuild/buf/cmd/buf@latest'"; exit 1)
	@$(BUF) generate
	@echo "v1 protobuf generation complete"
	@echo "Generated files in: api/proto/meridian/*/v1/"

## proto-descriptors: Build compiled FileDescriptorSet for the Vanguard HTTP/JSON transcoder
proto-descriptors: proto-validate
	@echo "Building proto FileDescriptorSet..."
	@which $(BUF) > /dev/null || (echo "buf not installed. Run 'go install github.com/bufbuild/buf/cmd/buf@latest'"; exit 1)
	@$(BUF) build api/proto -o cmd/meridian/descriptor.binpb
	@echo "Proto descriptor written to cmd/meridian/descriptor.binpb"

## proto-v2: Generate code from v2 protobuf definitions (placeholder for future use)
proto-v2:
	@echo "v2 protobuf definitions not yet available"
	@echo "When adding v2 support:"
	@echo "  1. Create api/proto/meridian/*/v2/ directories"
	@echo "  2. Update buf.gen.yaml for v2 generation paths"
	@echo "  3. Implement v2-specific generation logic here"

## proto-openapi: Show location of generated OpenAPI spec
proto-openapi: proto
	@echo ""
	@echo "OpenAPI specification generated:"
	@echo "  Location: api/openapi/meridian.swagger.json"
	@echo ""
	@echo "Usage:"
	@echo "  - Import into Postman/Insomnia for API testing"
	@echo "  - Generate client SDKs with OpenAPI Generator"
	@echo "  - Host with Swagger UI for interactive docs"
	@echo ""
	@if [ -f api/openapi/meridian.swagger.json ]; then \
		echo "File size: $$(wc -c < api/openapi/meridian.swagger.json | tr -d ' ') bytes"; \
		echo "Endpoints: $$(grep -c '"operationId"' api/openapi/meridian.swagger.json) operations"; \
	else \
		echo "Note: Run 'make proto' first to generate the spec"; \
	fi

## swagger-split: Split monolithic swagger into per-service files
swagger-split:
	@./scripts/split-swagger.sh

## swagger-ui: Serve Swagger UI for interactive API exploration
swagger-ui: swagger-split
	@command -v python3 >/dev/null || { echo "Error: python3 is required for the HTTP server"; exit 1; }
	@echo ""
	@echo "Meridian API Explorer"
	@echo "  Open: http://localhost:8091/swagger-ui.html"
	@echo "  Gateway: http://localhost:8090"
	@echo ""
	@echo "  Use the dropdown to switch between services."
	@echo "  Press Ctrl+C to stop."
	@echo ""
	@cd api/openapi && python3 -m http.server 8091

## proto-validate: Validate protobuf directory structure and versions
proto-validate:
	@echo "Validating protobuf directory structure..."
	@test -d api/proto/meridian || (echo "Error: api/proto/meridian directory not found"; exit 1)
	@echo "✓ Proto directory structure valid"
	@echo "Checking for v1 proto files..."
	@find api/proto/meridian -name "*.proto" -path "*/v1/*" | head -n 3 | while read f; do echo "  ✓ Found: $$f"; done
	@test -n "$$(find api/proto/meridian -name '*.proto' -path '*/v1/*')" || (echo "Error: No v1 proto files found"; exit 1)
	@echo "✓ v1 proto files validated"

## proto-lint: Lint protobuf files with buf
proto-lint:
	@echo "Linting protobuf files..."
	@which $(BUF) > /dev/null || (echo "buf not installed. Run 'go install github.com/bufbuild/buf/cmd/buf@latest'"; exit 1)
	@$(BUF) lint
	@echo "Protobuf lint complete"

## proto-breaking: Check for breaking proto changes
proto-breaking:
	@echo "Checking for breaking protobuf changes..."
	@which $(BUF) > /dev/null || (echo "buf not installed. Run 'go install github.com/bufbuild/buf/cmd/buf@latest'"; exit 1)
	@$(BUF) breaking --against '.git#branch=develop'
	@echo "No breaking changes detected"

## docker: Build Docker images
docker:
	@echo "Building Docker image..."
	@docker build -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .
	@echo "Docker image built: $(BINARY_NAME):$(VERSION)"

## deploy-local: Deploy to local Kubernetes using Tilt
deploy-local:
	@echo "Deploying to local Kubernetes with Tilt..."
	@which $(TILT) > /dev/null || (echo "Tilt not installed. Please install from https://tilt.dev"; exit 1)
	$(TILT) up

## tidy: Tidy and verify Go modules
tidy:
	@echo "Tidying Go modules..."
	$(GOMOD) tidy
	$(GOMOD) verify

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

## install: Install development tools
install:
	@echo "Installing development tools..."
	@echo "Installing golangci-lint..."
	@which $(GOLANGCI_LINT) > /dev/null || curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
	@echo "Installing buf and protobuf tools..."
	@go install github.com/bufbuild/buf/cmd/buf@latest
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "All tools installed successfully"
	@echo ""
	@echo "Optional tools to install manually:"
	@echo "  - Tilt: brew install tilt-dev/tap/tilt"

## proto-deps-update: Update buf.lock with latest dependencies
proto-deps-update:
	@echo "Updating buf dependencies..."
	@$(BUF) dep update
	@echo "Dependencies updated. Commit buf.lock to version control."

## proto-deps-graph: Display dependency graph
proto-deps-graph:
	@echo "Dependency graph:"
	@$(BUF) dep graph

## proto-plugins-info: Display current protoc plugin versions
proto-plugins-info:
	@echo "Current protoc plugin versions (from buf.gen.yaml):"
	@grep -E '^\s+- remote:' buf.gen.yaml | sed 's/^[[:space:]]*- remote: /  /'
	@echo ""
	@echo "To update plugin versions:"
	@echo "  1. Check latest versions at https://buf.build"
	@echo "  2. Update versions in buf.gen.yaml"
	@echo "  3. Run 'make proto' to test generation"
	@echo "  4. Commit buf.gen.yaml with updated versions"

## proto-jsonschema: Generate JSON Schema from manifest protobuf definition
proto-jsonschema:
	@echo "Generating JSON Schema from manifest proto..."
	@which protoc-gen-jsonschema > /dev/null 2>&1 || \
		([ -f "$$(go env GOPATH)/bin/protoc-gen-jsonschema" ] && export PATH="$$PATH:$$(go env GOPATH)/bin") || \
		(echo "protoc-gen-jsonschema not installed. Run: go install github.com/chrusty/protoc-gen-jsonschema/cmd/protoc-gen-jsonschema@latest"; exit 1)
	@export PATH="$$PATH:$$(go env GOPATH)/bin" && \
		$(BUF) generate --template buf.gen.jsonschema.yaml --path api/proto/meridian/control_plane/v1/manifest.proto
	@cp api/jsonschema/meridian.control_plane.v1/Manifest.json api/jsonschema/manifest.v1.schema.json
	@rm -rf api/jsonschema/meridian.control_plane.v1
	@echo "JSON Schema generated: api/jsonschema/manifest.v1.schema.json"

## validate-manifest-jsonschema: Validate JSON Schema is in sync with manifest proto
validate-manifest-jsonschema:
	@./scripts/validate-manifest-jsonschema.sh

## migrate-diff-all: Generate migrations for all schemas
migrate-diff-all: migrate-diff-current migrate-diff-position
	@echo "All schema migrations generated."

## migrate-diff-current: Generate migration for current_account schema
migrate-diff-current:
	@echo "Generating migration for current_account schema..."
	@if [ -z "$$MIGRATION_NAME" ]; then \
		if [ -t 0 ]; then \
			read -p "Enter migration name for current_account: " MIGRATION_NAME; \
		else \
			echo "Error: MIGRATION_NAME environment variable not set (required in non-interactive mode)"; \
			exit 1; \
		fi; \
	fi; \
	atlas migrate diff $$MIGRATION_NAME --env local --config services/current-account/atlas/atlas.hcl
	@echo "current_account migration generated. Review services/current-account/migrations/ directory."

## migrate-diff-position: Generate migration for position_keeping schema
migrate-diff-position:
	@echo "Generating migration for position_keeping schema..."
	@if [ -z "$$MIGRATION_NAME" ]; then \
		if [ -t 0 ]; then \
			read -p "Enter migration name for position_keeping: " MIGRATION_NAME; \
		else \
			echo "Error: MIGRATION_NAME environment variable not set (required in non-interactive mode)"; \
			exit 1; \
		fi; \
	fi; \
	atlas migrate diff $$MIGRATION_NAME --env local --config services/position-keeping/atlas/atlas.hcl
	@echo "position_keeping migration generated. Review services/position-keeping/migrations/ directory."

## migrate-apply-all: Apply all pending migrations
migrate-apply-all:
	@echo "Applying migrations for all schemas..."
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "Error: DATABASE_URL environment variable not set"; \
		exit 1; \
	fi
	@echo "Applying shared migrations (audit factory)..."
	@atlas migrate apply --env local --config shared/atlas/atlas.hcl --url "$$DATABASE_URL"
	@echo "Applying current_account migrations (includes current_account_audit)..."
	@atlas migrate apply --env local --config services/current-account/atlas/atlas.hcl --url "$$DATABASE_URL"
	@echo "Applying position_keeping migrations (includes position_keeping_audit)..."
	@atlas migrate apply --env local --config services/position-keeping/atlas/atlas.hcl --url "$$DATABASE_URL"
	@echo "All schema migrations applied (shared + each service with its own audit schema)."

## migrate-status-all: Show migration status for all schemas
migrate-status-all:
	@echo "Migration status for all schemas:"
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "Error: DATABASE_URL environment variable not set"; \
		exit 1; \
	fi
	@printf "\n=== shared (audit factory) ===\n"
	@atlas migrate status --env local --config shared/atlas/atlas.hcl --url "$$DATABASE_URL"
	@printf "\n=== current_account schema ===\n"
	@atlas migrate status --env local --config services/current-account/atlas/atlas.hcl --url "$$DATABASE_URL"
	@printf "\n=== position_keeping schema ===\n"
	@atlas migrate status --env local --config services/position-keeping/atlas/atlas.hcl --url "$$DATABASE_URL"

## migrate-lint-all: Lint all migrations for potential issues
migrate-lint-all:
	@echo "Linting migrations for all schemas..."
	@echo "Linting shared migrations..."
	@atlas migrate lint --env local --config shared/atlas/atlas.hcl --latest 1
	@echo "Linting current_account migrations..."
	@atlas migrate lint --env local --config services/current-account/atlas/atlas.hcl --latest 1
	@echo "Linting position_keeping migrations..."
	@atlas migrate lint --env local --config services/position-keeping/atlas/atlas.hcl --latest 1
	@echo "All migrations linted successfully."

## migrate-hash-all: Verify migration integrity for all schemas
migrate-hash-all:
	@echo "Verifying migration checksums for all schemas..."
	@atlas migrate hash --env local --config shared/atlas/atlas.hcl
	@atlas migrate hash --env local --config services/current-account/atlas/atlas.hcl
	@atlas migrate hash --env local --config services/position-keeping/atlas/atlas.hcl
	@echo "All migration checksums verified."

## migrate-apply-orgs: Apply migrations to all organization schemas
migrate-apply-orgs:
	@echo "Applying migrations to all active organizations..."
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "Error: DATABASE_URL environment variable not set"; \
		exit 1; \
	fi
	@if [ -z "$$ORGANIZATION_SERVICE_URL" ]; then \
		echo "Error: ORGANIZATION_SERVICE_URL environment variable not set"; \
		exit 1; \
	fi
	@chmod +x scripts/migrate-all-orgs.sh
	@./scripts/migrate-all-orgs.sh

## migrate-status-orgs: Show migration status for all organization schemas
migrate-status-orgs:
	@echo "Checking migration status for all organizations..."
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "Error: DATABASE_URL environment variable not set"; \
		exit 1; \
	fi
	@if [ -z "$$ORGANIZATION_SERVICE_URL" ]; then \
		echo "Error: ORGANIZATION_SERVICE_URL environment variable not set"; \
		exit 1; \
	fi
	@ORGS=$$(orgctl list --status=active 2>&1 | tail -n +3 | awk '{print $$1}' | grep -v '^$$'); \
	for ORG in $$ORGS; do \
		echo ""; \
		echo "=== Organization: $$ORG ==="; \
		for CONFIG in services/*/atlas/atlas.hcl; do \
			SERVICE=$$(basename $$(dirname $$(dirname $$CONFIG))); \
			echo "Service: $$SERVICE"; \
			atlas migrate status --env local --config $$CONFIG --url "$$DATABASE_URL&search_path=org_$$ORG" || true; \
		done; \
	done

## docs: Start local documentation server (pkgsite)
docs:
	@echo "Starting pkgsite documentation server..."
	@echo "Access documentation at: http://localhost:6060/github.com/meridianhub/meridian"
	@echo "Press Ctrl+C to stop"
	@command -v pkgsite >/dev/null 2>&1 || { echo "Installing pkgsite..."; go install golang.org/x/pkgsite/cmd/pkgsite@latest; }
	@pkgsite -open=false -http=:6060

## generate-saga-docs: Generate Markdown and JSON Schema documentation for saga handlers
generate-saga-docs:
	@echo "Generating saga handler documentation..."
	@go run tools/saga-doc-gen/main.go tools/saga-doc-gen/generator.go -schema-dir=shared/pkg/saga/schema -output-dir=docs
	@echo "Documentation generated successfully"

## asyncapi: Generate AsyncAPI 3.0.0 specs from topic registry and proto definitions
asyncapi:
	@echo "Generating AsyncAPI specs..."
	@./scripts/gen-asyncapi.sh
	@echo "AsyncAPI specs generated in api/asyncapi/"

## validate-manifests: Validate example manifests against protobuf schema, CEL, and Starlark
validate-manifests:
	@echo "Validating example manifests..."
	@$(GOCMD) run ./services/control-plane/cmd/validate/ -manifest='examples/manifests/*.json'
	@echo "All example manifests validated successfully"

## test-control-plane: Run control plane service unit tests
test-control-plane:
	@echo "Running control plane tests..."
	@$(GOTEST) -v -short -race ./services/control-plane/...
	@echo "Control plane tests passed"

## control-plane-ci: Run full control plane CI pipeline (schema sync + validation + tests)
control-plane-ci: validate-manifest-jsonschema validate-manifests test-control-plane
	@echo "Control plane CI pipeline passed"

## dev-up: Start entire Meridian platform in dev mode
DEV_PORTS=26257 8080 50051 8090
dev-up: proto
	@command -v docker >/dev/null || { echo "Error: docker is required"; exit 1; }
	@failed=0; for port in $(DEV_PORTS); do \
		pid=$$(lsof -ti :$$port 2>/dev/null | head -1); \
		if [ -n "$$pid" ]; then \
			proc=$$(ps -p $$pid -o comm= 2>/dev/null || echo "unknown"); \
			echo "Error: port $$port already in use by $$proc (pid $$pid)"; \
			failed=1; \
		fi; \
	done; \
	if [ $$failed -eq 1 ]; then \
		echo ""; \
		echo "Hint: if Tilt is running, stop it first:  tilt down"; \
		echo "      or stop leftover containers:        make dev-down"; \
		exit 1; \
	fi
	@echo ""
	@echo "Starting Meridian dev environment..."
	@echo ""
	@echo "  Once ready, services will be available at:"
	@echo ""
	@echo "    Meridian Console         http://localhost:5173"
	@echo "    Gateway (REST/Connect)   localhost:8090 (API only, no web UI)"
	@echo "    gRPC                     localhost:50051"
	@echo "    CockroachDB UI           http://localhost:8080"
	@echo "    CockroachDB SQL          postgresql://root@localhost:26257/defaultdb?sslmode=disable"
	@echo ""
	@echo "  Swagger UI (requires separate terminal):"
	@echo "    make swagger-ui          http://localhost:8091/swagger-ui.html"
	@echo ""
	@echo "  Press Ctrl+C to stop."
	@echo ""
	docker compose -f $(DEV_COMPOSE) up --build

## dev-down: Stop dev environment containers
dev-down:
	@echo "Stopping dev environment..."
	docker compose -f $(DEV_COMPOSE) down

## dev-clean: Stop dev environment and delete all data volumes
dev-clean:
	@echo "Stopping dev environment and removing volumes..."
	docker compose -f $(DEV_COMPOSE) down -v
