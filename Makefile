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
PROTOC=protoc
TILT=tilt

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod
GOGET=$(GOCMD) get
GOFMT=$(GOCMD) fmt

.PHONY: all help build test lint clean proto docker deploy-local fmt tidy deps coverage install

# Default target
all: help

## help: Display this help message
help:
	@echo "Meridian - Production-Grade Open Banking Ledger"
	@echo ""
	@echo "Available targets:"
	@echo "  make build         - Compile all Go services"
	@echo "  make test          - Run tests with coverage"
	@echo "  make lint          - Run golangci-lint"
	@echo "  make fmt           - Format Go code"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make proto         - Generate code from protobuf definitions"
	@echo "  make docker        - Build Docker images"
	@echo "  make deploy-local  - Deploy to local Kubernetes using Tilt"
	@echo "  make coverage      - Generate and open HTML coverage report"
	@echo "  make tidy          - Tidy and verify Go modules"
	@echo "  make deps          - Download dependencies"
	@echo "  make install       - Install development tools"
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
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | tail -n 1
	@echo "Coverage report: $(COVERAGE_DIR)/coverage.out"

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

## proto: Generate code from protobuf definitions
proto:
	@echo "Generating code from protobuf definitions..."
	@which $(PROTOC) > /dev/null || (echo "protoc not installed. Please install protobuf compiler"; exit 1)
	@find $(API_DIR) -name "*.proto" -exec $(PROTOC) --go_out=. --go-grpc_out=. {} + 2>/dev/null || echo "No .proto files found (this is OK for now)"
	@echo "Protobuf generation complete"

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
	@echo "All tools installed successfully"
	@echo ""
	@echo "Optional tools to install manually:"
	@echo "  - Tilt: brew install tilt-dev/tap/tilt"
	@echo "  - Protobuf compiler: brew install protobuf"
