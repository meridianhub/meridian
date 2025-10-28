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
echo "║  Meridian Development Environment Setup Verification    ║"
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
            golangci-lint)
                version=$(golangci-lint version --format short 2>/dev/null | head -1)
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

check_k8s_cluster() {
    echo "Checking Kubernetes cluster connectivity..."

    if kubectl cluster-info &> /dev/null; then
        local context
        context=$(kubectl config current-context 2>/dev/null || echo "unknown")
        echo -e "${GREEN}✓${NC} Kubernetes cluster accessible"
        echo -e "  Context: $context"

        # Check nodes
        local nodes
        nodes=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
        echo -e "  Nodes: $nodes"
    else
        echo -e "${RED}✗${NC} Cannot connect to Kubernetes cluster"
        echo -e "  ${YELLOW}Setup:${NC} Start a local cluster with:"
        echo -e "    - Docker Desktop: Enable Kubernetes in settings"
        echo -e "    - kind: kind create cluster"
        echo -e "    - minikube: minikube start"
        echo -e "    - colima: colima start --kubernetes"
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
    echo "You're ready to start developing. Try:"
    echo "  1. go mod download          # Install Go dependencies"
    echo "  2. make proto               # Generate protobuf code"
    echo "  3. make build               # Build the application"
    echo "  4. make test                # Run tests"
    echo "  5. tilt up                  # Start local development environment"
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
