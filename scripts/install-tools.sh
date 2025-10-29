#!/usr/bin/env bash

# Automated tool installation for Meridian development environment
# Supports macOS (Homebrew) and Linux

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo "╔══════════════════════════════════════════════════════════╗"
echo "║                                                          ║"
echo "║  Meridian Development Tools Installer                    ║"
echo "║                                                          ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Detect OS
OS="unknown"
if [[ "$OSTYPE" == "darwin"* ]]; then
    OS="macos"
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    OS="linux"
fi

echo "Detected OS: $OS"
echo ""

# Check for package manager
if [[ "$OS" == "macos" ]]; then
    if ! command -v brew &> /dev/null; then
        echo -e "${RED}✗ Homebrew not found${NC}"
        echo ""
        echo "Install Homebrew first:"
        echo "  /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
        echo ""
        exit 1
    fi
    PKG_MANAGER="brew"
elif [[ "$OS" == "linux" ]]; then
    if command -v apt-get &> /dev/null; then
        PKG_MANAGER="apt"
    elif command -v yum &> /dev/null; then
        PKG_MANAGER="yum"
    else
        echo -e "${RED}✗ No supported package manager found${NC}"
        exit 1
    fi
else
    echo -e "${RED}✗ Unsupported operating system${NC}"
    exit 1
fi

echo -e "Using package manager: ${GREEN}$PKG_MANAGER${NC}"
echo ""

# Installation functions
install_tool() {
    local tool=$1
    local install_cmd=$2

    if command -v "$tool" &> /dev/null; then
        echo -e "${GREEN}✓${NC} $tool already installed"
        return 0
    fi

    echo -e "${YELLOW}Installing $tool...${NC}"
    if eval "$install_cmd"; then
        echo -e "${GREEN}✓${NC} $tool installed successfully"
    else
        echo -e "${RED}✗${NC} Failed to install $tool"
        return 1
    fi
}

# Install tools based on OS
if [[ "$OS" == "macos" ]]; then
    echo "Installing development tools via Homebrew..."
    echo ""

    # Core tools
    install_tool "go" "brew install go"
    install_tool "git" "brew install git"

    # Docker & Kubernetes
    install_tool "docker" "brew install --cask docker"
    install_tool "kubectl" "brew install kubectl"
    install_tool "helm" "brew install helm"
    install_tool "kind" "brew install kind"
    install_tool "ctlptl" "brew install tilt-dev/tap/ctlptl"
    install_tool "tilt" "brew install tilt-dev/tap/tilt"

    # API tools
    install_tool "buf" "brew install bufbuild/buf/buf"
    install_tool "protoc" "brew install protobuf"

    # Code quality
    install_tool "golangci-lint" "brew install golangci-lint"

elif [[ "$OS" == "linux" ]]; then
    echo "Installing development tools for Linux..."
    echo ""

    if [[ "$PKG_MANAGER" == "apt" ]]; then
        # Update package list
        sudo apt-get update

        # Core tools
        install_tool "go" "sudo apt-get install -y golang-go"
        install_tool "git" "sudo apt-get install -y git"
        install_tool "make" "sudo apt-get install -y build-essential"

        # Docker
        if ! command -v docker &> /dev/null; then
            echo -e "${YELLOW}Installing Docker...${NC}"
            curl -fsSL https://get.docker.com -o get-docker.sh
            sudo sh get-docker.sh
            sudo usermod -aG docker "$USER"
            rm get-docker.sh
            echo -e "${GREEN}✓${NC} Docker installed (logout and login to use without sudo)"
        fi

        # kubectl
        install_tool "kubectl" "sudo snap install kubectl --classic"

        # Helm
        if ! command -v helm &> /dev/null; then
            echo -e "${YELLOW}Installing Helm...${NC}"
            curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
        fi

        # Tilt
        if ! command -v tilt &> /dev/null; then
            echo -e "${YELLOW}Installing Tilt...${NC}"
            curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
        fi

        # buf
        if ! command -v buf &> /dev/null; then
            echo -e "${YELLOW}Installing buf...${NC}"
            BIN="/usr/local/bin" && \
            VERSION="1.59.0" && \
            curl -sSL "https://github.com/bufbuild/buf/releases/download/v${VERSION}/buf-$(uname -s)-$(uname -m)" -o "${BIN}/buf" && \
            chmod +x "${BIN}/buf"
        fi

        # protoc
        if ! command -v protoc &> /dev/null; then
            echo -e "${YELLOW}Installing protoc...${NC}"
            PROTOC_VERSION="33.0"
            curl -LO "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip"
            sudo unzip -o protoc-${PROTOC_VERSION}-linux-x86_64.zip -d /usr/local bin/protoc
            sudo unzip -o protoc-${PROTOC_VERSION}-linux-x86_64.zip -d /usr/local 'include/*'
            rm protoc-${PROTOC_VERSION}-linux-x86_64.zip
        fi

        # golangci-lint
        if ! command -v golangci-lint &> /dev/null; then
            echo -e "${YELLOW}Installing golangci-lint...${NC}"
            curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
        fi
    fi
fi

echo ""
echo "═══════════════════════════════════════"
echo " Installation Complete"
echo "═══════════════════════════════════════"
echo ""

echo "Next steps:"
echo "  1. Verify installation: ./scripts/setup-check.sh"
echo "  2. Start Docker Desktop (required for Kind cluster)"
echo "  3. Create Kind cluster: ctlptl create cluster kind --name=meridian-local"
echo "  4. Clone the repository and run: go mod download"
echo "  5. Install git hooks: .githooks/install.sh"
echo "  6. Start developing: tilt up"
echo ""
echo -e "${BLUE}Tip:${NC} Kind + ctlptl provides a fast local Kubernetes cluster optimized for Tilt development"
echo ""

if [[ "$OS" == "linux" ]] && groups "$USER" | grep -q docker; then
    echo -e "${YELLOW}Note:${NC} You may need to logout and login for Docker group membership to take effect"
    echo ""
fi
