#!/bin/bash
# Setup git hooks for Meridian project
# This script installs pre-commit hooks that validate Go code and protobuf schemas

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOKS_DIR=".githooks"
GIT_HOOKS_DIR=".git/hooks"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "======================================"
echo "  Meridian Git Hooks Setup"
echo "======================================"
echo ""

# Verify we're in a git repository
if [ ! -d ".git" ]; then
    echo "Error: Not in a git repository root"
    echo "Please run this script from the repository root directory"
    exit 1
fi

# Create .git/hooks directory if it doesn't exist
mkdir -p "$GIT_HOOKS_DIR"

# Install pre-commit hook
if [ -f "$HOOKS_DIR/pre-commit" ]; then
    cp "$HOOKS_DIR/pre-commit" "$GIT_HOOKS_DIR/pre-commit"
    chmod +x "$GIT_HOOKS_DIR/pre-commit"
    echo -e "${GREEN}✓${NC} Installed pre-commit hook"
else
    echo -e "${YELLOW}⚠${NC}  pre-commit hook not found in $HOOKS_DIR"
    exit 1
fi

echo ""
echo "======================================"
echo "  Setup Complete!"
echo "======================================"
echo ""
echo "Git hooks installed successfully."
echo ""
echo "The pre-commit hook will automatically:"
echo "  • Run buf lint on proto files"
echo "  • Check for breaking proto changes"
echo "  • Format Go files with gofumpt"
echo "  • Lint Go files with golangci-lint"
echo ""
echo "To verify installation:"
echo "  ls -la .git/hooks/pre-commit"
echo ""
echo "To skip hooks temporarily (not recommended):"
echo "  git commit --no-verify"
echo ""
echo "For more information:"
echo "  • See .githooks/README.md for hook details"
echo "  • See .claude/skills/schema-evolution/SKILL.md for proto guidelines"
echo ""
