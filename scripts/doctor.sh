#!/usr/bin/env bash

# Meridian Development Environment Doctor
# Validates and optionally fixes the development environment setup
# Inspired by 'brew doctor' and 'claude doctor'

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Track overall status
ALL_CHECKS_PASSED=true
FIX_MODE=false
VERBOSE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --fix)
            FIX_MODE=true
            shift
            ;;
        --check)
            FIX_MODE=false
            shift
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            echo "Meridian Development Environment Doctor"
            echo ""
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --check        Check only, don't fix (default)"
            echo "  --fix          Automatically fix issues"
            echo "  -v, --verbose  Show more details"
            echo "  -h, --help     Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0              # Check environment and report issues"
            echo "  $0 --fix        # Check and automatically fix all issues"
            echo ""
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Run '$0 --help' for usage information"
            exit 1
            ;;
    esac
done

echo "╔══════════════════════════════════════════════════════════╗"
echo "║                                                          ║"
echo "║  Meridian Development Environment Doctor                 ║"
echo "║                                                          ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

if [ "$FIX_MODE" = true ]; then
    echo -e "${BLUE}Mode:${NC} Fix (will automatically install/fix issues)"
else
    echo -e "${BLUE}Mode:${NC} Check (will report issues only)"
fi
echo ""

# Detect OS
OS="unknown"
if [[ "$OSTYPE" == "darwin"* ]]; then
    OS="macos"
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    OS="linux"
fi

# Determine package manager (validated to prevent command injection)
if [[ "$OS" == "macos" ]]; then
    PKG_MANAGER="brew"
elif [[ "$OS" == "linux" ]]; then
    if command -v apt-get &> /dev/null; then
        PKG_MANAGER="apt-get"
    elif command -v yum &> /dev/null; then
        PKG_MANAGER="yum"
    else
        PKG_MANAGER="unknown"
    fi
fi

# Validate PKG_MANAGER to prevent command injection
case "$PKG_MANAGER" in
    brew|apt-get|yum|unknown) ;;  # Valid values
    *)
        echo -e "${RED}Error: Invalid package manager detected${NC}"
        exit 1
        ;;
esac

# Helper function to get tool version
get_version() {
    local cmd=$1
    local version
    case "$cmd" in
        go)
            version=$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')
            ;;
        docker)
            version=$(docker --version 2>/dev/null | awk '{print $3}' | sed 's/,//')
            ;;
        kubectl)
            version=$(kubectl version --client --output=yaml 2>/dev/null | grep gitVersion | awk '{print $2}' | head -1)
            ;;
        helm)
            version=$(helm version --short 2>/dev/null | awk '{print $1}')
            ;;
        tilt)
            version=$(tilt version 2>/dev/null | head -1 | awk '{print $2}' | sed 's/,//')
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
            version=$(protoc --version 2>/dev/null | awk '{print $2}')
            ;;
        make)
            version=$(make --version 2>/dev/null | head -1 | awk '{print $3}')
            ;;
        git)
            version=$(git --version 2>/dev/null | awk '{print $3}')
            ;;
        node)
            version=$(node --version 2>/dev/null | sed 's/v//')
            ;;
        npm)
            version=$(npm --version 2>/dev/null)
            ;;
        *)
            version="unknown"
            ;;
    esac
    echo "$version"
}

# Helper function to get install command based on platform
get_install_cmd() {
    local tool=$1

    case "$OS-$tool" in
        macos-go) echo "brew install go" ;;
        macos-git) echo "brew install git" ;;
        macos-docker) echo "brew install --cask docker" ;;
        macos-kubectl) echo "brew install kubectl" ;;
        macos-helm) echo "brew install helm" ;;
        macos-kind) echo "brew install kind" ;;
        macos-ctlptl) echo "brew install tilt-dev/tap/ctlptl" ;;
        macos-tilt) echo "brew install tilt-dev/tap/tilt" ;;
        macos-buf) echo "brew install bufbuild/buf/buf" ;;
        macos-protoc) echo "brew install protobuf" ;;
        macos-golangci-lint) echo "brew install golangci-lint" ;;
        macos-node) echo "brew install node" ;;

        linux-go) echo "sudo ${PKG_MANAGER} install -y golang-go" ;;
        linux-git) echo "sudo ${PKG_MANAGER} install -y git" ;;
        linux-docker) echo "curl -fsSL https://get.docker.com | sh" ;;
        linux-kubectl) echo "sudo snap install kubectl --classic" ;;
        linux-helm) echo "curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash" ;;
        linux-kind) echo "go install sigs.k8s.io/kind@latest" ;;
        linux-ctlptl) echo "go install github.com/tilt-dev/ctlptl/cmd/ctlptl@latest" ;;
        linux-tilt) echo "curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash" ;;
        linux-buf) echo "go install github.com/bufbuild/buf/cmd/buf@latest" ;;
        linux-protoc) echo "sudo ${PKG_MANAGER} install -y protobuf-compiler" ;;
        linux-golangci-lint) echo "curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b \$(go env GOPATH)/bin" ;;
        linux-node) echo "sudo ${PKG_MANAGER} install -y nodejs npm" ;;

        *) echo "" ;;  # Unknown combination
    esac
}

# Helper function to install tool
install_tool() {
    local tool=$1

    if [ "$FIX_MODE" = false ]; then
        return 1
    fi

    local install_cmd
    install_cmd=$(get_install_cmd "$tool")

    if [ -z "$install_cmd" ]; then
        echo -e "${RED}✗${NC} Cannot install $tool - unsupported platform"
        return 1
    fi

    echo -e "${YELLOW}Installing $tool...${NC}"
    # Security model: sh -c is safe here because:
    # 1. install_cmd comes exclusively from get_install_cmd() with hardcoded commands
    # 2. PKG_MANAGER is validated against whitelist (brew|apt-get|yum|unknown) at startup
    # 3. Tool name ($tool) comes from controlled check_tool() calls, not user input
    # 4. No user-supplied data flows into install_cmd construction
    if sh -c "$install_cmd" &> /dev/null; then
        echo -e "${GREEN}✓${NC} $tool installed successfully"
        return 0
    else
        echo -e "${RED}✗${NC} Failed to install $tool"
        return 1
    fi
}

# Check command and optionally fix
check_tool() {
    local cmd=$1
    local required_version=$2

    if command -v "$cmd" &> /dev/null; then
        local version
        version=$(get_version "$cmd")
        echo -e "${GREEN}✓${NC} $cmd"
        if [ "$VERBOSE" = true ]; then
            echo -e "  Version: $version"
            if [ -n "$required_version" ]; then
                echo -e "  Required: $required_version"
            fi
        fi
        return 0
    else
        echo -e "${RED}✗${NC} $cmd ${RED}(not found)${NC}"

        if [ "$FIX_MODE" = true ]; then
            install_tool "$cmd"
            return $?
        else
            # Show platform-appropriate fix suggestion
            local install_cmd
            install_cmd=$(get_install_cmd "$cmd")
            if [ -n "$install_cmd" ]; then
                echo -e "  ${YELLOW}Fix:${NC} $install_cmd"
            else
                echo -e "  ${YELLOW}Fix:${NC} Install $cmd for your platform"
            fi
            ALL_CHECKS_PASSED=false
            return 1
        fi
    fi
}

# Validate Go environment
check_go_environment() {
    if ! command -v go &> /dev/null; then
        return 1
    fi

    echo "Validating Go environment..."

    GOROOT_OUTPUT=$(go env GOROOT 2>&1)
    GOROOT_EXIT=$?

    if [ $GOROOT_EXIT -ne 0 ]; then
        echo -e "${RED}✗${NC} Go GOROOT configuration invalid"
        echo -e "  ${YELLOW}Error:${NC} $GOROOT_OUTPUT"

        if echo "$GOROOT_OUTPUT" | grep -q "cannot find GOROOT directory.*toolchain"; then
            echo -e "  ${YELLOW}Diagnosis:${NC} Go toolchain module corruption detected"
            if [ "$FIX_MODE" = true ]; then
                echo -e "${YELLOW}Cleaning Go module cache...${NC}"
                go clean -modcache
                echo -e "${GREEN}✓${NC} Go module cache cleaned"
            else
                echo -e "  ${YELLOW}Fix:${NC} go clean -modcache"
            fi
        else
            local reinstall_cmd
            reinstall_cmd=$(get_install_cmd "go")
            if [ -n "$reinstall_cmd" ]; then
                echo -e "  ${YELLOW}Fix:${NC} $reinstall_cmd"
            else
                echo -e "  ${YELLOW}Fix:${NC} Reinstall Go for your platform"
            fi
        fi
        ALL_CHECKS_PASSED=false
        return 1
    elif [ ! -d "$GOROOT_OUTPUT" ]; then
        echo -e "${RED}✗${NC} GOROOT directory does not exist"
        echo -e "  Expected: $GOROOT_OUTPUT"
        local reinstall_cmd
        reinstall_cmd=$(get_install_cmd "go")
        if [ -n "$reinstall_cmd" ]; then
            echo -e "  ${YELLOW}Fix:${NC} $reinstall_cmd"
        else
            echo -e "  ${YELLOW}Fix:${NC} Reinstall Go for your platform"
        fi
        ALL_CHECKS_PASSED=false
        return 1
    else
        echo -e "${GREEN}✓${NC} Go environment properly configured"
        if [ "$VERBOSE" = true ]; then
            echo -e "  GOROOT: $GOROOT_OUTPUT"
            # Check GOPATH with error handling
            if GOPATH_OUTPUT=$(go env GOPATH 2>&1); then
                echo -e "  GOPATH: $GOPATH_OUTPUT"
            fi
        fi
        return 0
    fi
}

# Check Docker daemon
check_docker_daemon() {
    if ! command -v docker &> /dev/null; then
        return 1  # Docker not installed, will be caught by tool check
    fi

    echo "Checking Docker daemon..."

    if docker info &> /dev/null; then
        echo -e "${GREEN}✓${NC} Docker daemon running"
        return 0
    else
        echo -e "${RED}✗${NC} Docker daemon not running"
        echo -e "  ${YELLOW}Fix:${NC} Start Docker Desktop"
        ALL_CHECKS_PASSED=false
        return 1
    fi
}

# Check Kubernetes cluster
check_k8s_cluster() {
    echo "Checking Kubernetes cluster..."

    local current_context
    current_context=$(kubectl config current-context 2>/dev/null || echo "none")

    # Check if pointing to remote cluster by inspecting cluster server URL
    local is_remote_cluster=false
    local cluster_server
    cluster_server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || echo "")

    # Remote if server is external (not localhost/127.0.0.1) or context name suggests remote
    if echo "$cluster_server" | grep -qv "localhost\|127.0.0.1\|0.0.0.0" || \
       echo "$current_context" | grep -q "arn:aws:eks\|\.eks\.\|gke_\|aks-"; then
        is_remote_cluster=true
    fi

    # Quick network check for remote clusters
    local network_available=true
    if [ "$is_remote_cluster" = true ]; then
        # Check if timeout command is available (not default on macOS)
        local has_timeout=false
        if command -v timeout &> /dev/null; then
            has_timeout=true
        elif command -v gtimeout &> /dev/null; then
            # GNU timeout on macOS (from coreutils)
            alias timeout=gtimeout
            has_timeout=true
        fi

        # Use curl as primary check (more reliable cross-platform)
        if command -v curl &> /dev/null; then
            if [ "$has_timeout" = true ]; then
                if ! timeout 2 curl -sI --fail https://google.com &>/dev/null; then
                    network_available=false
                fi
            else
                if ! curl -sI --fail --max-time 2 https://google.com &>/dev/null; then
                    network_available=false
                fi
            fi
        # Fallback to nc and host if curl not available
        elif command -v nc &> /dev/null && command -v host &> /dev/null; then
            if [ "$has_timeout" = true ]; then
                if ! timeout 2 nc -z 8.8.8.8 53 2>/dev/null || ! timeout 2 host google.com >/dev/null 2>&1; then
                    network_available=false
                fi
            else
                # Without timeout, just try the checks (may hang briefly)
                if ! nc -z -w 2 8.8.8.8 53 2>/dev/null || ! host -W 2 google.com >/dev/null 2>&1; then
                    network_available=false
                fi
            fi
        else
            # Cannot verify network, assume available to avoid false negatives
            network_available=true
        fi
    fi

    # Try to connect to cluster
    local cluster_timeout=5
    if [ "$network_available" = false ]; then
        cluster_timeout=2
    fi

    # Check timeout availability for kubectl check
    local kubectl_check_cmd
    if command -v timeout &> /dev/null; then
        kubectl_check_cmd="timeout $cluster_timeout kubectl cluster-info"
    elif command -v gtimeout &> /dev/null; then
        kubectl_check_cmd="gtimeout $cluster_timeout kubectl cluster-info"
    else
        # No timeout available, just run kubectl directly
        kubectl_check_cmd="kubectl cluster-info"
    fi

    if $kubectl_check_cmd &>/dev/null; then
        echo -e "${GREEN}✓${NC} Kubernetes cluster accessible"
        if [ "$VERBOSE" = true ]; then
            local nodes
            nodes=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
            echo -e "  Context: $current_context"
            echo -e "  Nodes: $nodes"
        fi
        return 0
    else
        echo -e "${RED}✗${NC} Cannot connect to Kubernetes cluster"
        echo -e "  Current context: $current_context"

        if [ "$FIX_MODE" = true ] && [ "$is_remote_cluster" = false ]; then
            # Try to create local cluster with registry
            if command -v ctlptl &> /dev/null && command -v kind &> /dev/null && docker info &> /dev/null 2>&1; then
                if ! kubectl config get-contexts kind-meridian-local &> /dev/null; then
                    echo -e "${BLUE}Creating Kind cluster 'kind-meridian-local' with local registry...${NC}"
                    if ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local; then
                        echo -e "${GREEN}✓${NC} Kind cluster created successfully!"
                        return 0
                    else
                        echo -e "${RED}✗${NC} Failed to create Kind cluster"
                    fi
                fi
            fi
        else
            echo -e "  ${YELLOW}Fix:${NC} ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local"
        fi

        ALL_CHECKS_PASSED=false
        return 1
    fi
}

# Check npm dependencies
check_npm_dependencies() {
    if [ ! -f "package.json" ]; then
        return 0  # Skip if no package.json
    fi

    if ! command -v npm &> /dev/null; then
        return 1  # npm not installed, will be caught by tool check
    fi

    # Check if all dependencies are installed (not just one specific package)
    if [ -d "node_modules" ] && npm list --depth=0 &> /dev/null; then
        echo -e "${GREEN}✓${NC} Node.js dependencies installed"
        if [ "$VERBOSE" = true ]; then
            local dep_count
            dep_count=$(npm list --depth=0 --json 2>/dev/null | grep -c "\"version\":" || echo "unknown")
            echo -e "  Packages: $dep_count"
        fi
        return 0
    else
        echo -e "${YELLOW}⚠${NC}  Node.js dependencies not installed or incomplete"

        if [ "$FIX_MODE" = true ]; then
            echo -e "${YELLOW}Installing Node.js dependencies...${NC}"
            if npm install; then
                echo -e "${GREEN}✓${NC} Node.js dependencies installed successfully"
                return 0
            else
                echo -e "${RED}✗${NC} Failed to install Node.js dependencies"
                ALL_CHECKS_PASSED=false
                return 1
            fi
        else
            echo -e "  ${YELLOW}Fix:${NC} npm install"
            ALL_CHECKS_PASSED=false
            return 1
        fi
    fi
}

# Check git hooks
check_git_hooks() {
    echo "Checking git hooks..."

    local source_hook=".githooks/pre-commit"
    local git_dir
    local installed_hook

    # Handle git worktrees: .git is a file with 'gitdir:' pointing to actual git dir
    if [ -f ".git" ]; then
        # Extract the gitdir path from the .git file
        git_dir=$(sed -n 's/^gitdir: //p' .git)
        if [ -n "$git_dir" ]; then
            # For worktrees, hooks are shared from the main repo's .git/hooks
            # Worktree gitdir format: /path/to/main/.git/worktrees/<name>
            # We need: /path/to/main/.git/hooks/pre-commit
            local main_git_dir
            main_git_dir=$(dirname "$(dirname "$git_dir")")
            installed_hook="${main_git_dir}/hooks/pre-commit"
        else
            installed_hook=".git/hooks/pre-commit"
        fi
    else
        installed_hook=".git/hooks/pre-commit"
    fi

    if [ ! -f "$source_hook" ]; then
        echo -e "${RED}✗${NC} Source pre-commit hook not found"
        echo -e "  Expected: $source_hook"
        ALL_CHECKS_PASSED=false
        return 1
    fi

    if [ ! -f "$installed_hook" ]; then
        echo -e "${RED}✗${NC} Pre-commit hook not installed"

        if [ "$FIX_MODE" = true ]; then
            echo -e "${YELLOW}Installing git hooks...${NC}"
            mkdir -p ".git/hooks"
            cp "$source_hook" "$installed_hook"
            chmod +x "$installed_hook"
            echo -e "${GREEN}✓${NC} Git hooks installed successfully"
            return 0
        else
            # Suggest install script if it exists, otherwise give direct command
            if [ -f ".githooks/install.sh" ]; then
                echo -e "  ${YELLOW}Fix:${NC} .githooks/install.sh"
            else
                echo -e "  ${YELLOW}Fix:${NC} cp $source_hook $installed_hook && chmod +x $installed_hook"
            fi
            ALL_CHECKS_PASSED=false
            return 1
        fi
    fi

    # Check if hook is executable
    if [ ! -x "$installed_hook" ]; then
        echo -e "${YELLOW}⚠${NC}  Pre-commit hook not executable"

        if [ "$FIX_MODE" = true ]; then
            chmod +x "$installed_hook"
            echo -e "${GREEN}✓${NC} Made hook executable"
        else
            echo -e "  ${YELLOW}Fix:${NC} chmod +x $installed_hook"
            ALL_CHECKS_PASSED=false
            return 1
        fi
    fi

    # Check if hook is in sync with source
    if ! cmp -s "$source_hook" "$installed_hook"; then
        echo -e "${YELLOW}⚠${NC}  Pre-commit hook out of sync with project"

        if [ "$FIX_MODE" = true ]; then
            echo -e "${YELLOW}Updating git hook...${NC}"
            cp "$source_hook" "$installed_hook"
            chmod +x "$installed_hook"
            echo -e "${GREEN}✓${NC} Git hook updated"
            return 0
        else
            # Suggest install script if it exists, otherwise give direct command
            if [ -f ".githooks/install.sh" ]; then
                echo -e "  ${YELLOW}Fix:${NC} .githooks/install.sh"
            else
                echo -e "  ${YELLOW}Fix:${NC} cp $source_hook $installed_hook && chmod +x $installed_hook"
            fi
            ALL_CHECKS_PASSED=false
            return 1
        fi
    fi

    echo -e "${GREEN}✓${NC} Git hooks properly configured"
    if [ "$VERBOSE" = true ]; then
        echo -e "  Location: $installed_hook"
    fi
    return 0
}

# Core Development Tools
echo "═══════════════════════════════════════"
echo " Core Development Tools"
echo "═══════════════════════════════════════"
echo ""

check_tool "go" "1.23+"
check_go_environment
echo ""

check_tool "make" ""
echo ""

check_tool "git" "2.x+"
echo ""

# Container & Orchestration
echo "═══════════════════════════════════════"
echo " Container & Orchestration"
echo "═══════════════════════════════════════"
echo ""

check_tool "docker" "20.x+"
echo ""

check_docker_daemon
echo ""

check_tool "kubectl" "1.28+"
echo ""

check_tool "helm" "3.x+"
echo ""

check_tool "kind" ""
echo ""

check_tool "ctlptl" ""
echo ""

check_tool "tilt" "0.30+"
echo ""

check_k8s_cluster
echo ""

# API Development Tools
echo "═══════════════════════════════════════"
echo " API Development Tools"
echo "═══════════════════════════════════════"
echo ""

check_tool "buf" "1.x+"
echo ""

check_tool "protoc" "3.x+"
echo ""

# Code Quality
echo "═══════════════════════════════════════"
echo " Code Quality & Linting"
echo "═══════════════════════════════════════"
echo ""

check_tool "golangci-lint" "2.x+"
echo ""

# Node.js
echo "═══════════════════════════════════════"
echo " Node.js & npm"
echo "═══════════════════════════════════════"
echo ""

check_tool "node" "20+"
echo ""

check_tool "npm" "10+"
echo ""

check_npm_dependencies
echo ""

# Documentation Tools
echo "═══════════════════════════════════════"
echo " Documentation Tools"
echo "═══════════════════════════════════════"
echo ""

if command -v pkgsite &> /dev/null || [ -f "$(go env GOPATH)/bin/pkgsite" ]; then
    echo -e "${GREEN}✓${NC} pkgsite installed"
    if [ "$VERBOSE" = true ]; then
        echo -e "  Run: make docs"
    fi
else
    echo -e "${YELLOW}!${NC} pkgsite not installed (optional)"
    if [ "$FIX_MODE" = true ]; then
        echo -e "${YELLOW}Installing pkgsite...${NC}"
        if go install golang.org/x/pkgsite/cmd/pkgsite@latest; then
            echo -e "${GREEN}✓${NC} pkgsite installed"
        else
            echo -e "${RED}✗${NC} Failed to install pkgsite"
        fi
    else
        echo -e "  ${YELLOW}Install:${NC} go install golang.org/x/pkgsite/cmd/pkgsite@latest"
    fi
fi
echo ""

# Git Configuration
echo "═══════════════════════════════════════"
echo " Git Configuration"
echo "═══════════════════════════════════════"
echo ""

check_git_hooks
echo ""

# Project Dependencies
echo "═══════════════════════════════════════"
echo " Project Dependencies"
echo "═══════════════════════════════════════"
echo ""

if [ -f "go.mod" ]; then
    echo -e "${GREEN}✓${NC} Go modules configured"
    if [ "$VERBOSE" = true ]; then
        echo -e "  Run: go mod download"
    fi
else
    echo -e "${RED}✗${NC} go.mod not found"
    echo -e "  ${YELLOW}Note:${NC} Ensure you're in the project root directory"
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
    echo -e "${GREEN}║      Your environment is healthy. Ready to develop!            ║${NC}"
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
    exit 0
else
    echo -e "${RED}✗ Some checks failed${NC}"
    echo ""

    if [ "$FIX_MODE" = false ]; then
        echo "To automatically fix issues, run:"
        echo -e "  ${BLUE}$0 --fix${NC}"
        echo ""
    else
        echo "Some issues could not be fixed automatically."
        echo "Please address the remaining issues manually."
        echo ""
    fi

    exit 1
fi
