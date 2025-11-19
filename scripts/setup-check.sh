#!/usr/bin/env bash

# Setup verification script for Meridian development environment
# Checks all required tools and provides installation guidance

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Track overall status
ALL_CHECKS_PASSED=true

echo "╔══════════════════════════════════════════════════════════╗"
echo "║                                                          ║"
echo "║  Meridian Development Environment Setup Verification     ║"
echo "║                                                          ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Helper functions
check_command() {
    local cmd=$1
    local required_version=$2
    local install_hint=$3

    if command -v "$cmd" &> /dev/null; then
        local version
        case "$cmd" in
            go)
                version=$(go version | awk '{print $3}' | sed 's/go//')
                ;;
            docker)
                version=$(docker --version | awk '{print $3}' | sed 's/,//')
                ;;
            kubectl)
                version=$(kubectl version --client --output=yaml 2>/dev/null | grep gitVersion | awk '{print $2}' | head -1)
                ;;
            helm)
                version=$(helm version --short | awk '{print $1}')
                ;;
            tilt)
                version=$(tilt version | head -1 | awk '{print $2}' | sed 's/,//')
                ;;
            kind)
                version=$(kind version 2>/dev/null | awk '{print $2}' | head -1)
                ;;
            ctlptl)
                version=$(ctlptl version 2>/dev/null | awk '{print $1}' | sed 's/,//')
                ;;
            golangci-lint)
                version=$(golangci-lint version --short 2>/dev/null | head -1)
                ;;
            buf)
                version=$(buf --version 2>/dev/null)
                ;;
            protoc)
                version=$(protoc --version | awk '{print $2}')
                ;;
            make)
                version=$(make --version | head -1 | awk '{print $3}')
                ;;
            git)
                version=$(git --version | awk '{print $3}')
                ;;
            *)
                version="unknown"
                ;;
        esac

        echo -e "${GREEN}✓${NC} $cmd"
        echo -e "  Version: $version"

        if [ -n "$required_version" ]; then
            echo -e "  Required: $required_version"
        fi
    else
        echo -e "${RED}✗${NC} $cmd ${RED}(not found)${NC}"
        echo -e "  ${YELLOW}Install:${NC} $install_hint"
        ALL_CHECKS_PASSED=false
    fi
    echo ""
}

list_local_contexts() {
    # Helper function to list available local Kubernetes contexts and provide guidance
    # Shows kind-meridian-local first if available, then lists other local contexts
    # Falls back to cluster creation instructions if no local contexts found

    local local_contexts
    local_contexts=$(kubectl config get-contexts -o name 2>/dev/null | grep -E "kind-|docker-desktop|rancher-desktop|minikube|colima")

    if [ -n "$local_contexts" ]; then
        echo -e "  Switch to a local cluster that works offline:"
        echo -e ""
        # Show the most likely context first (kind-meridian-local)
        if echo "$local_contexts" | grep -q "kind-meridian-local"; then
            echo -e "  ${BLUE}→ kubectl config use-context kind-meridian-local${NC}"
            echo -e ""
        fi
        echo -e "  ${GREEN}All available local contexts:${NC}"
        while IFS= read -r ctx; do
            if [ "$ctx" != "kind-meridian-local" ]; then
                echo -e "    kubectl config use-context $ctx"
            fi
        done <<< "$local_contexts"
    else
        echo -e "  1. Ensure Docker Desktop is running"
        echo -e "  2. Create a local Kind cluster:"
        echo -e ""
        echo -e "     ${BLUE}ctlptl create cluster kind --name=kind-meridian-local${NC}"
        echo -e ""
        echo -e "  3. The cluster will be automatically selected as your context"
    fi
}

check_k8s_cluster() {
    echo "Checking Kubernetes cluster connectivity..."

    # Get current context
    local current_context
    current_context=$(kubectl config current-context 2>/dev/null || echo "none")

    # Check if we're pointing to a remote cluster before attempting connection
    local is_remote_cluster=false
    if echo "$current_context" | grep -q "arn:aws:eks\|\.eks\.\|gke_\|aks-"; then
        is_remote_cluster=true
    fi

    # Quick network connectivity check (only for remote clusters)
    local network_available=true
    if [ "$is_remote_cluster" = true ]; then
        # Quick check - try to resolve a common domain with 2 second timeout
        if ! timeout 2 nc -z 8.8.8.8 53 2>/dev/null && ! timeout 2 host google.com >/dev/null 2>&1; then
            network_available=false
        fi
    fi

    # Try to connect to cluster with shorter timeout for offline scenarios
    local cluster_error
    local exit_code=0
    local timeout_duration=5

    # Use shorter timeout if we know we're offline
    if [ "$network_available" = false ]; then
        timeout_duration=2
    fi

    cluster_error=$(timeout "$timeout_duration" kubectl cluster-info 2>&1) || exit_code=$?

    if [ $exit_code -eq 0 ]; then
        echo -e "${GREEN}✓${NC} Kubernetes cluster accessible"
        echo -e "  Context: $current_context"

        # Check nodes
        local nodes
        nodes=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
        echo -e "  Nodes: $nodes"
    else
        echo -e "${RED}✗${NC} Cannot connect to Kubernetes cluster"
        echo -e "  Current context: $current_context"
        echo -e ""

        # Provide specific diagnosis based on context and network status
        # Check for offline scenario first
        if [ "$network_available" = false ]; then
            echo -e "  ${YELLOW}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${YELLOW}║  OFFLINE MODE DETECTED                                 ║${NC}"
            echo -e "  ${YELLOW}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""
            echo -e "  No network connectivity detected. This is normal for:"
            echo -e "    • Working offline (airplane mode, no WiFi)"
            echo -e "    • VPN disconnected"
            echo -e "    • Network issues"
            echo -e ""

            if [ "$is_remote_cluster" = true ]; then
                echo -e "  ${YELLOW}Additional issue:${NC} kubectl is configured for remote cluster"
                echo -e "  Context: ${BLUE}$current_context${NC}"
                echo -e ""
            fi

            echo -e "  ${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${GREEN}║  RECOMMENDED FIX:                                      ║${NC}"
            echo -e "  ${GREEN}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""

            list_local_contexts
        # Check for AWS/EKS context with network available
        elif echo "$current_context" | grep -q "arn:aws:eks\|\.eks\."; then
            echo -e "  ${YELLOW}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${YELLOW}║  REMOTE CLUSTER DETECTED                               ║${NC}"
            echo -e "  ${YELLOW}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""
            echo -e "  kubectl is configured for AWS EKS production cluster"
            echo -e "  Context: ${BLUE}$current_context${NC}"
            echo -e ""
            echo -e "  ${YELLOW}This requires:${NC}"
            echo -e "    • AWS SSO login"
            echo -e "    • Production access permissions"
            echo -e "    • Active AWS session"
            echo -e ""
            echo -e "  ${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${GREEN}║  RECOMMENDED FIX:                                      ║${NC}"
            echo -e "  ${GREEN}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""

            list_local_contexts
        elif echo "$cluster_error" | grep -q "connection refused\|no such host"; then
            echo -e "  ${YELLOW}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${YELLOW}║  CLUSTER NOT RUNNING                                   ║${NC}"
            echo -e "  ${YELLOW}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""
            echo -e "  The configured cluster is not running or unreachable"
            echo -e "  Context: ${BLUE}$current_context${NC}"
            echo -e ""

            # Offer to create cluster automatically if ctlptl and kind are available
            if command -v ctlptl &> /dev/null && command -v kind &> /dev/null && docker info &> /dev/null; then
                echo -e "  ${GREEN}I can create a Kind cluster for you!${NC}"
                echo -e ""
                read -p "  Create Kind cluster 'kind-meridian-local' now? (y/n): " -n 1 -r
                echo ""
                if [[ $REPLY =~ ^[Yy]$ ]]; then
                    echo -e ""
                    echo -e "  ${BLUE}Creating Kind cluster 'kind-meridian-local'...${NC}"
                    if ctlptl create cluster kind --name=kind-meridian-local; then
                        echo -e ""
                        echo -e "  ${GREEN}✓${NC} Kind cluster created successfully!"
                        echo -e "  Run this script again to verify, or start developing with: ${BLUE}tilt up${NC}"
                        echo ""
                        exit 0
                    else
                        echo -e ""
                        echo -e "  ${RED}✗${NC} Failed to create cluster"
                    fi
                fi
                echo -e ""
            fi

            echo -e "  ${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
            echo -e "  ${GREEN}║  RECOMMENDED FIX:                                      ║${NC}"
            echo -e "  ${GREEN}╚════════════════════════════════════════════════════════╝${NC}"
            echo -e ""
            echo -e "  Create a local Kubernetes cluster:"
            echo -e ""
            echo -e "  1. Ensure Docker Desktop is running"
            echo -e "  2. Create cluster:"
            echo -e ""
            echo -e "     ${BLUE}ctlptl create cluster kind --name=kind-meridian-local${NC}"
            echo -e ""
            echo -e "  ${YELLOW}Alternative options:${NC}"
            echo -e "    • Docker Desktop: Enable Kubernetes (Preferences → Kubernetes → Enable)"
            echo -e "    • Rancher Desktop: Enable Kubernetes in preferences"
            echo -e "    • minikube: minikube start"
        else
            echo -e ""
            echo -e "  ${YELLOW}Error details:${NC}"
            echo "$cluster_error" | head -3 | sed 's/^/    /'
            echo -e ""
            echo -e "  ${YELLOW}Troubleshooting:${NC}"
            echo -e "    1. Check available contexts: kubectl config get-contexts"
            echo -e "    2. Switch context: kubectl config use-context <context-name>"
            echo -e "    3. Start a local cluster (see options above)"
        fi

        ALL_CHECKS_PASSED=false
    fi
    echo ""
}

# Core Development Tools
echo "═══════════════════════════════════════"
echo " Core Development Tools"
echo "═══════════════════════════════════════"
echo ""

check_command "go" "1.23+" "brew install go"

# Validate Go environment
if command -v go &> /dev/null; then
    echo "Validating Go environment..."

    # Check GOROOT
    GOROOT_OUTPUT=$(go env GOROOT 2>&1)
    GOROOT_EXIT=$?

    if [ $GOROOT_EXIT -ne 0 ]; then
        echo -e "${RED}✗${NC} Go GOROOT configuration invalid"
        echo -e "  ${YELLOW}Error:${NC} $GOROOT_OUTPUT"

        # Check for specific toolchain errors
        if echo "$GOROOT_OUTPUT" | grep -q "cannot find GOROOT directory.*toolchain"; then
            echo -e ""
            echo -e "  ${YELLOW}Diagnosis:${NC} Go toolchain module corruption detected"
            echo -e "  This typically happens after Go version upgrades or incomplete installations"
            echo -e ""
            echo -e "  ${YELLOW}Recommended fix:${NC}"
            echo -e "    ${BLUE}go clean -modcache${NC}  # Clears corrupted toolchain modules"
            echo -e ""
            echo -e "  ${YELLOW}If that doesn't work, clean reinstall:${NC}"
            echo -e "    ${BLUE}brew uninstall go && brew install go${NC}"
            echo -e ""
        else
            echo -e "  ${YELLOW}Fix:${NC} Reinstall Go: ${BLUE}brew reinstall go${NC}"
        fi
        ALL_CHECKS_PASSED=false
    elif [ ! -d "$GOROOT_OUTPUT" ]; then
        echo -e "${RED}✗${NC} GOROOT directory does not exist"
        echo -e "  Expected: $GOROOT_OUTPUT"
        echo -e "  ${YELLOW}Fix:${NC} Reinstall Go: ${BLUE}brew reinstall go${NC}"
        ALL_CHECKS_PASSED=false
    else
        echo -e "${GREEN}✓${NC} GOROOT is valid"
        echo -e "  Location: $GOROOT_OUTPUT"

        # Test basic Go environment
        if go env GOPATH &> /dev/null; then
            echo -e "${GREEN}✓${NC} Go environment is properly configured"
        else
            echo -e "${RED}✗${NC} Go environment check failed"
            echo -e "  ${YELLOW}Fix:${NC} Check GOPATH configuration or reinstall Go"
            ALL_CHECKS_PASSED=false
        fi
    fi
    echo ""
fi

check_command "make" "" "pre-installed on macOS/Linux"
check_command "git" "2.x+" "brew install git"

# Docker & Kubernetes
echo "═══════════════════════════════════════"
echo " Container & Orchestration"
echo "═══════════════════════════════════════"
echo ""

check_command "docker" "20.x+" "brew install --cask docker"
check_command "kubectl" "1.28+" "brew install kubectl"
check_command "helm" "3.x+" "brew install helm"
check_command "kind" "" "brew install kind"
check_command "ctlptl" "" "brew install tilt-dev/tap/ctlptl"
check_command "tilt" "0.30+" "brew install tilt-dev/tap/tilt"

# Check Kubernetes cluster
check_k8s_cluster

# Protocol Buffers & API Tools
echo "═══════════════════════════════════════"
echo " API Development Tools"
echo "═══════════════════════════════════════"
echo ""

check_command "buf" "1.x+" "brew install bufbuild/buf/buf"
check_command "protoc" "3.x+" "brew install protobuf"

# Code Quality Tools
echo "═══════════════════════════════════════"
echo " Code Quality & Linting"
echo "═══════════════════════════════════════"
echo ""

check_command "golangci-lint" "2.x+" "brew install golangci-lint"

# Node.js & npm (for markdown linting)
echo "═══════════════════════════════════════"
echo " Node.js & npm"
echo "═══════════════════════════════════════"
echo ""

check_command "node" "18+" "brew install node"
check_command "npm" "9+" "brew install node"

# Check if markdownlint-cli2 is installed
if [ -f "package.json" ]; then
    if [ -d "node_modules" ] && npm list markdownlint-cli2 &> /dev/null; then
        echo -e "${GREEN}✓${NC} markdownlint-cli2 installed"
        echo -e "  Run: npm run lint:md"
    else
        echo -e "${YELLOW}⚠${NC}  markdownlint-cli2 not installed"
        echo -e "  This is required for markdown linting in pre-commit hooks"
        echo -e "  Install: npm install"
        ALL_CHECKS_PASSED=false
    fi
else
    echo -e "${YELLOW}⚠${NC}  package.json not found"
    echo -e "  Cannot verify markdownlint-cli2 installation"
fi

# Documentation Tools
echo "═══════════════════════════════════════"
echo " Documentation Tools"
echo "═══════════════════════════════════════"
echo ""

if command -v pkgsite &> /dev/null || [ -f "$(go env GOPATH)/bin/pkgsite" ]; then
    echo -e "${GREEN}✓${NC} pkgsite installed"
    echo -e "  Run: make docs (or pkgsite -http=:6060)"
    if ! command -v pkgsite &> /dev/null; then
        echo -e "  ${YELLOW}Note:${NC} Add \$(go env GOPATH)/bin to PATH"
    fi
else
    echo -e "${YELLOW}!${NC} pkgsite not installed (optional)"
    echo -e "  For local Go documentation web UI"
    echo -e "  Install: go install golang.org/x/pkgsite/cmd/pkgsite@latest"
    echo -e "  Usage: make docs"
fi

# Git Hooks
echo "═══════════════════════════════════════"
echo " Git Configuration"
echo "═══════════════════════════════════════"
echo ""

if [ -f ".githooks/pre-commit" ]; then
    if [ -x ".githooks/pre-commit" ]; then
        echo -e "${GREEN}✓${NC} Git hooks configured"
        echo -e "  Location: .githooks/pre-commit"
    else
        echo -e "${YELLOW}⚠${NC}  Git hooks found but not executable"
        echo -e "  ${YELLOW}Fix:${NC} chmod +x .githooks/pre-commit"
    fi
else
    echo -e "${YELLOW}⚠${NC}  Git hooks not installed"
    echo -e "  ${YELLOW}Install:${NC} Run .githooks/install.sh"
fi
echo ""

# Go Modules
echo "═══════════════════════════════════════"
echo " Project Dependencies"
echo "═══════════════════════════════════════"
echo ""

if [ -f "go.mod" ]; then
    echo -e "${GREEN}✓${NC} Go modules configured"
    echo -e "  ${YELLOW}Setup:${NC} Run 'go mod download' to install dependencies"
else
    echo -e "${RED}✗${NC} go.mod not found"
    echo -e "  ${YELLOW}Fix:${NC} Ensure you're in the project root directory"
    ALL_CHECKS_PASSED=false
fi
echo ""

# Summary
echo "═══════════════════════════════════════"
echo " Summary"
echo "═══════════════════════════════════════"
echo ""

if [ "$ALL_CHECKS_PASSED" = true ]; then
    echo -e "${GREEN}✓ All checks passed!${NC}"
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║                                                                ║${NC}"
    echo -e "${GREEN}║      You're all set! Ready to start developing.                ║${NC}"
    echo -e "${GREEN}║                                                                ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${BLUE}Quick Start:${NC}"
    echo ""
    echo "  1. Install dependencies:"
    echo -e "     ${BLUE}go mod download${NC}"
    echo ""
    echo "  2. Generate protobuf code:"
    echo -e "     ${BLUE}make proto${NC}"
    echo ""
    echo "  3. Start local development environment:"
    echo -e "     ${BLUE}tilt up${NC}"
    echo ""
    echo "  Once Tilt starts, access:"
    echo ""
    echo -e "  ${BLUE}Development & Monitoring:${NC}"
    echo "    • Tilt UI:        http://localhost:10350"
    echo ""
    echo -e "  ${BLUE}Application Endpoints:${NC}"
    echo "    • Meridian API:   http://localhost:8080"
    echo "    • Meridian gRPC:  localhost:9090"
    echo ""
    echo -e "  ${BLUE}Observability Stack:${NC}"
    echo "    • Grafana:        http://localhost:3000"
    echo "      (Anonymous access enabled - no login required)"
    echo "    • Prometheus:     http://localhost:9090"
    echo "      (Metrics collection and querying)"
    echo "    • Tempo:          http://localhost:3200"
    echo "      (Distributed tracing backend)"
    echo "    • Loki:           http://localhost:3100"
    echo "      (Log aggregation - query via Grafana)"
    echo ""
    echo -e "${YELLOW}Tip:${NC} Run ${BLUE}make test${NC} and ${BLUE}make build${NC} to verify everything works"
    echo ""
    exit 0
else
    echo -e "${RED}✗ Some checks failed${NC}"
    echo ""
    echo "Please install the missing tools using the hints above."
    echo "For automated installation on macOS, run:"
    echo "  ./scripts/install-tools.sh"
    echo ""
    echo "For detailed setup instructions, see:"
    echo "  README.md - Quick Start section"
    echo "  CONTRIBUTING.md - Development Environment Setup"
    echo ""
    exit 1
fi
