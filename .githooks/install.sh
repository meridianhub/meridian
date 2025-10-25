#!/bin/bash
# Install Git hooks for Meridian development

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GIT_DIR="$(git rev-parse --git-dir)"

echo "📦 Installing Git hooks..."

# Configure Git to use .githooks directory
git config core.hooksPath .githooks

echo "✅ Git hooks installed successfully!"
echo ""
echo "The following hooks are now active:"
ls -1 "$SCRIPT_DIR" | grep -v install.sh | sed 's/^/  - /'
echo ""
echo "To disable hooks temporarily, use:"
echo "  git commit --no-verify"
