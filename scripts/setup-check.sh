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
                version=$(ctlptl version 2>/dev/null | grep 'Version:' | awk '{print $2}')
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

    # Get current context
    local current_context
    current_context=$(kubectl config current-context 2>/dev/null || echo "none")

    # Try to connect to cluster with timeout (single invocation)
    local cluster_error
    local exit_code=0
    cluster_error=$(timeout 5 kubectl cluster-info 2>&1) || exit_code=$?

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

        # Provide specific diagnosis based on error
        # Check for AWS/EKS context or AWS authentication errors
        if echo "$current_context" | grep -q "arn:aws:eks\|\.eks\."; then
            echo -e ""
            echo -e "  ${YELLOW}Diagnosis:${NC} kubectl is configured for AWS EKS cluster requiring SSO login"
            echo -e ""
            echo -e "  ${YELLOW}ACTION REQUIRED:${NC} Switch to a local cluster context:"
            echo -e ""

            # Check for available local contexts
            local local_contexts
            local_contexts=$(kubectl config get-contexts -o name 2>/dev/null | grep -E "kind-|docker-desktop|rancher-desktop|minikube|colima")

            if [ -n "$local_contexts" ]; then
                echo -e "  ${GREEN}Available local contexts:${NC}"
                while IFS= read -r ctx; do
                    echo -e "    kubectl config use-context $ctx"
                done <<< "$local_contexts"
                echo -e ""
                echo -e "  ${YELLOW}Run one of the above commands to switch to local development.${NC}"
            else
                echo -e "  ${YELLOW}No local cluster contexts found. Create a local cluster:${NC}"
                echo -e ""
                echo -e "  ${GREEN}Recommended (Kind + ctlptl):${NC}"
                echo -e "    1. Ensure Docker Desktop is running"
                echo -e "    2. Create cluster: ${BLUE}ctlptl create cluster kind --name=meridian-local${NC}"
                echo -e "    3. Verify: kubectl config use-context kind-meridian-local"
                echo -e ""
                echo -e "  ${YELLOW}Alternative options:${NC}"
                echo -e "    - Docker Desktop: Enable Kubernetes in settings (Preferences → Kubernetes)"
                echo -e "    - Rancher Desktop: Enable Kubernetes in preferences"
                echo -e "    - minikube: minikube start"
            fi
        elif echo "$cluster_error" | grep -q "connection refused\|no such host"; then
            echo -e ""
            echo -e "  ${YELLOW}Diagnosis:${NC} Cluster not running or unreachable"
            echo -e ""

            # Offer to create cluster automatically if ctlptl and kind are available
            if command -v ctlptl &> /dev/null && command -v kind &> /dev/null && docker info &> /dev/null; then
                echo -e "  ${GREEN}I can create a Kind cluster for you!${NC}"
                echo -e ""
                read -p "  Create Kind cluster 'meridian-local' now? (y/n): " -n 1 -r
                echo ""
                if [[ $REPLY =~ ^[Yy]$ ]]; then
                    echo -e ""
                    echo -e "  ${BLUE}Creating Kind cluster 'meridian-local'...${NC}"
                    if ctlptl create cluster kind --name=meridian-local; then
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

            echo -e "  ${YELLOW}ACTION REQUIRED:${NC} Start a local Kubernetes cluster"
            echo -e ""
            echo -e "  ${GREEN}Recommended (Kind + ctlptl):${NC}"
            echo -e "    1. Ensure Docker Desktop is running"
            echo -e "    2. Create cluster: ${BLUE}ctlptl create cluster kind --name=meridian-local${NC}"
            echo -e "    3. Verify: kubectl get nodes"
            echo -e ""
            echo -e "  ${YELLOW}Alternative options:${NC}"
            echo -e "    - Docker Desktop: Enable Kubernetes (Preferences → Kubernetes → Enable)"
            echo -e "    - Rancher Desktop: Enable Kubernetes in preferences"
            echo -e "    - minikube: minikube start"
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
    echo -e "${GREEN}║  🚀 You're all set! Ready to start developing.                ║${NC}"
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
    echo "    • Tilt UI:        http://localhost:10350"
    echo "    • Meridian API:   http://localhost:8080"
    echo "    • Meridian gRPC:  localhost:9090"
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
