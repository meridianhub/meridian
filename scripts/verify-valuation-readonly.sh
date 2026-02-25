#!/usr/bin/env bash
# verify-valuation-readonly.sh
#
# CI verification script to ensure the valuation engine builtins package
# maintains read-only constraints and does not import write-capable clients.
#
# CRITICAL: The valuation engine must be provably read-only to prevent
# valuation scripts from performing mutations (writes, deletes, etc.).
#
# This script enforces architectural constraints at CI time.

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILTINS_DIR="$REPO_ROOT/shared/pkg/valuation/internal/builtins"

echo "🔍 Verifying valuation engine read-only constraints..."
echo "   Builtins directory: $BUILTINS_DIR"
echo ""

# Check if builtins directory exists
if [ ! -d "$BUILTINS_DIR" ]; then
    echo -e "${RED}✗ ERROR: Builtins directory not found: $BUILTINS_DIR${NC}"
    exit 1
fi

# FORBIDDEN IMPORTS
# These packages/patterns indicate write-capable operations and MUST NOT be imported
FORBIDDEN_PATTERNS=(
    # gRPC clients with mutation operations
    "meridianhub/meridian/services/position-keeping/client"
    "meridianhub/meridian/services/financial-accounting/client"
    "meridianhub/meridian/services/current-account/client"
    "meridianhub/meridian/services/internal-account/client"

    # Database write operations
    "gorm.io/gorm.*Create"
    "gorm.io/gorm.*Update"
    "gorm.io/gorm.*Delete"
)

# ALLOWED IMPORTS
# These are explicitly allowed for read-only operations
ALLOWED_PATTERNS=(
    # Standard library
    "fmt"
    "errors"
    "time"
    "context"

    # Read-only types
    "github.com/shopspring/decimal"
    "github.com/google/uuid"

    # Starlark runtime (read-only execution)
    "go.starlark.net/starlark"
)

VIOLATIONS_FOUND=0

# Function to check for forbidden imports
check_forbidden_imports() {
    local file=$1

    for pattern in "${FORBIDDEN_PATTERNS[@]}"; do
        if grep -q "$pattern" "$file"; then
            echo -e "${RED}✗ FORBIDDEN IMPORT DETECTED${NC}"
            echo "   File: $file"
            echo "   Pattern: $pattern"
            echo "   Line:"
            grep -n "$pattern" "$file" | head -1
            echo ""
            VIOLATIONS_FOUND=$((VIOLATIONS_FOUND + 1))
        fi
    done
}

# Check all Go files in builtins directory
echo "📋 Checking Go source files..."
while IFS= read -r -d '' file; do
    check_forbidden_imports "$file"
done < <(find "$BUILTINS_DIR" -name "*.go" -not -name "*_test.go" -print0)

# Additional check: scan for dangerous function calls
echo "📋 Checking for dangerous function calls..."
DANGEROUS_FUNCTIONS=(
    "\\.Create\\("
    "\\.Update\\("
    "\\.Delete\\("
    "\\.Save\\("
    "\\.Exec\\("
    "http\\.Post\\("
    "http\\.Put\\("
    "http\\.Delete\\("
)

for file in "$BUILTINS_DIR"/*.go; do
    if [ -f "$file" ] && [[ ! "$file" =~ _test\.go$ ]]; then
        for func in "${DANGEROUS_FUNCTIONS[@]}"; do
            if grep -E "$func" "$file" | grep -v "^//" | grep -v "^\s*//" > /dev/null 2>&1; then
                echo -e "${YELLOW}⚠ WARNING: Potentially dangerous function call${NC}"
                echo "   File: $file"
                echo "   Pattern: $func"
                echo "   Line:"
                grep -n -E "$func" "$file" | grep -v "^//" | head -1
                echo ""
                VIOLATIONS_FOUND=$((VIOLATIONS_FOUND + 1))
            fi
        done
    fi
done

# Final verdict
echo "═══════════════════════════════════════════════════════════"
if [ $VIOLATIONS_FOUND -eq 0 ]; then
    echo -e "${GREEN}✓ PASSED: No read-only violations found${NC}"
    echo ""
    echo "The valuation engine builtins package maintains read-only constraints."
    echo "No write-capable imports or dangerous function calls detected."
    exit 0
else
    echo -e "${RED}✗ FAILED: $VIOLATIONS_FOUND violation(s) found${NC}"
    echo ""
    echo "The valuation engine MUST be read-only. Fix the violations above."
    echo ""
    echo "Allowed operations:"
    echo "  ✓ Reading from databases (SELECT queries)"
    echo "  ✓ Calling read-only gRPC methods (Get*, List*, Query*)"
    echo "  ✓ Pure computation (CEL evaluation, Starlark execution)"
    echo ""
    echo "Forbidden operations:"
    echo "  ✗ Writing to databases (CREATE, UPDATE, DELETE)"
    echo "  ✗ Calling mutation gRPC methods (Create*, Update*, Delete*, Execute*)"
    echo "  ✗ Making HTTP mutation requests (POST, PUT, DELETE)"
    echo ""
    exit 1
fi
