# Meridian - Production-Grade Open Banking Ledger
# Makefile for build, test, and deployment automation

# Variables
BINARY_NAME=meridian
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

# Directories
CMD_DIR=./cmd
INTERNAL_DIR=./internal
PKG_DIR=./pkg
API_DIR=./api
DIST_DIR=./dist
COVERAGE_DIR=./coverage

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

.PHONY: all help build test lint clean proto proto-v1 proto-v2 proto-lint proto-breaking docker deploy-local fmt tidy deps coverage install proto-validate

# Default target
all: help

## help: Display this help message
help:
	@echo "Meridian - Production-Grade Open Banking Ledger"
	@echo ""
	@echo "Available targets:"
	@echo "  make build          - Compile all Go services"
	@echo "  make test           - Run tests with coverage"
	@echo "  make lint           - Run golangci-lint"
	@echo "  make fmt            - Format Go code"
	@echo "  make clean          - Remove build artifacts"
	@echo "  make proto          - Generate code from all protobuf versions"
	@echo "  make proto-v1       - Generate code from v1 protobuf definitions"
	@echo "  make proto-v2       - Generate code from v2 protobuf definitions (future)"
	@echo "  make proto-validate - Validate protobuf directory structure"
	@echo "  make proto-lint     - Lint protobuf files with buf"
	@echo "  make proto-breaking - Check for breaking proto changes"
	@echo "  make docker         - Build Docker images"
	@echo "  make deploy-local   - Deploy to local Kubernetes using Tilt"
	@echo "  make coverage       - Generate and open HTML coverage report"
	@echo "  make tidy           - Tidy and verify Go modules"
	@echo "  make deps           - Download dependencies"
	@echo "  make install        - Install development tools"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
	@echo "  COMMIT=$(COMMIT)"
	@echo "  BUILD_DATE=$(BUILD_DATE)"

## build: Compile all Go services
build: tidy
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(DIST_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME) $(CMD_DIR)/...
	@echo "Build complete: $(DIST_DIR)/$(BINARY_NAME)"

## test: Run tests with coverage
test:
	@echo "Running tests with coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
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

## lint: Run golangci-lint
lint:
	@echo "Running golangci-lint..."
	@which $(GOLANGCI_LINT) > /dev/null || (echo "golangci-lint not installed. Run 'make install'"; exit 1)
	$(GOLANGCI_LINT) run --timeout 5m ./...

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
proto: proto-v1
	@echo "All protobuf versions generated successfully"

## proto-v1: Generate code from v1 protobuf definitions
proto-v1: proto-validate
	@echo "Generating code from v1 protobuf definitions..."
	@which $(BUF) > /dev/null || (echo "buf not installed. Run 'go install github.com/bufbuild/buf/cmd/buf@latest'"; exit 1)
	@$(BUF) generate
	@echo "v1 protobuf generation complete"
	@echo "Generated files in: api/proto/meridian/*/v1/"

## proto-v2: Generate code from v2 protobuf definitions (placeholder for future use)
proto-v2:
	@echo "v2 protobuf definitions not yet available"
	@echo "When adding v2 support:"
	@echo "  1. Create api/proto/meridian/*/v2/ directories"
	@echo "  2. Update buf.gen.yaml for v2 generation paths"
	@echo "  3. Implement v2-specific generation logic here"

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
