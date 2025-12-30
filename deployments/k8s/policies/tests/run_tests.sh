#!/usr/bin/env bash
# Integration tests for the BlockDevModeInProduction OPA Gatekeeper policy
#
# Prerequisites:
#   - opa: brew install opa (or download from https://www.openpolicyagent.org/docs/latest/#running-opa)
#
# Usage:
#   ./run_tests.sh           # Run all tests
#   ./run_tests.sh -v        # Verbose output

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERBOSE=${1:-""}

echo "=== OPA Policy Tests: BlockDevModeInProduction ==="
echo ""

# Check for opa command
if ! command -v opa &> /dev/null; then
    echo "ERROR: 'opa' command not found."
    echo "Install with: brew install opa"
    echo "Or download from: https://www.openpolicyagent.org/docs/latest/#running-opa"
    exit 1
fi

echo "Running OPA unit tests..."
echo ""

# Note: --v0-compatible is required because Gatekeeper ConstraintTemplates
# use the older Rego v0 syntax. OPA 1.x defaults to Rego v1.
if [ "$VERBOSE" = "-v" ]; then
    opa test "$SCRIPT_DIR" --v0-compatible -v
else
    opa test "$SCRIPT_DIR" --v0-compatible
fi

echo ""
echo "=== All tests passed ==="
