#!/usr/bin/env bash
# lint-multi-asset-purity.sh - Detect hardcoded asset references in production Go code
#
# Scans for patterns that violate multi-asset purity:
#   1. Hardcoded instrument codes in string comparisons
#   2. Deprecated currency registry imports (shared/domain/money)
#   3. Hardcoded precision constants (defaultPrecision = 2)
#   4. Switch statements on instrument codes
#
# Allowlisted exceptions:
#   - Test files (*_test.go)
#   - Seed commands (cmd/seed-*)
#   - Utilities (utilities/)
#   - External adapters (adapters/stripe/)
#   - payment-order service (intentionally currency-only by business rule)
#   - Known violations tracked in docs/audit/multi-asset-purity.md
#
# Usage:
#   ./scripts/lint-multi-asset-purity.sh [--strict]
#
# Exit codes:
#   0 - No new violations (or only known violations in non-strict mode)
#   1 - New violations detected

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STRICT="${1:-}"
VIOLATIONS=0
KNOWN_VIOLATIONS=0

# Colors for terminal output (disabled in CI)
if [ -t 1 ]; then
    RED='\033[0;31m'
    YELLOW='\033[0;33m'
    GREEN='\033[0;32m'
    NC='\033[0m'
else
    RED=''
    YELLOW=''
    GREEN=''
    NC=''
fi

# Common exclusion arguments for grep
# shellcheck disable=SC2034
GREP_EXCLUDES=(
    --include='*.go'
    --exclude='*_test.go'
    --exclude='*.pb.go'
    --exclude-dir='vendor'
    --exclude-dir='.git'
    --exclude-dir='node_modules'
)

# Paths to exclude from violations (allowlisted)
is_allowlisted() {
    local file="$1"
    # Test files
    [[ "$file" == *_test.go ]] && return 0
    # Seed commands
    [[ "$file" == *cmd/seed-* ]] && return 0
    # Utilities
    [[ "$file" == *utilities/* ]] && return 0
    # Stripe adapters (external API requirements)
    [[ "$file" == *adapters/stripe/* ]] && return 0
    # payment-order service (intentionally currency-only)
    [[ "$file" == *services/payment-order/* ]] && return 0
    # Generated protobuf files
    [[ "$file" == *.pb.go ]] && return 0
    # The bucketing library itself (it IS the registry)
    [[ "$file" == *shared/pkg/bucketing/* ]] && return 0
    # The currency library itself (canonical currency definitions)
    [[ "$file" == *shared/platform/quantity/currency/* ]] && return 0
    # Proto mappers (already deprecated, tracked for removal)
    [[ "$file" == *shared/pkg/proto/mappers/* ]] && return 0
    # Legacy money packages (tracked for migration)
    [[ "$file" == *shared/domain/money/* ]] && return 0
    [[ "$file" == *shared/pkg/money/* ]] && return 0
    # shared/pkg/amount (instrument handling library)
    [[ "$file" == *shared/pkg/amount/* ]] && return 0
    return 1
}

# Known violation files tracked in docs/audit/multi-asset-purity.md.
# Each entry is a specific file (not a directory) to avoid silently
# exempting new files added to the same directory.
is_known_violation() {
    local file="$1"

    case "$file" in
        # Critical: hardcoded switch on instrument codes (Task 3)
        *position-keeping/adapters/balance_mapper.go) return 0 ;;
        # Critical: deprecated currency re-exports (Task 7)
        *financial-accounting/domain/currency.go) return 0 ;;
        *position-keeping/domain/quantity.go) return 0 ;;
        # High: IsPhysicsInstrument hardcodes "KWH"/"GAS" (Task 5)
        *shared/pkg/saga/handlers.go) return 0 ;;
        # High: defaultPrecision = 2 fallback (Task 4)
        *internal-account/service/lien_service.go) return 0 ;;
        # High: backward-compat precision defaults (Task 6)
        *financial-accounting/adapters/persistence/repository.go) return 0 ;;
        # Medium: current-account GBP (intentional business rule)
        *current-account/domain/account.go) return 0 ;;
        *current-account/service/lien_service.go) return 0 ;;
        *current-account/service/client_interfaces.go) return 0 ;;
        *current-account/service/grpc_account_endpoints.go) return 0 ;;
        *current-account/service/grpc_control_endpoints.go) return 0 ;;
        *) return 1 ;;
    esac
}

report_violation() {
    local category="$1"
    local file="$2"
    local line_num="$3"
    local content="$4"

    # Strip repo root prefix for cleaner output
    local rel_file="${file#"$REPO_ROOT"/}"

    if is_allowlisted "$rel_file"; then
        return
    fi

    if is_known_violation "$rel_file"; then
        KNOWN_VIOLATIONS=$((KNOWN_VIOLATIONS + 1))
        return
    fi

    echo -e "${RED}VIOLATION${NC} [$category]: $rel_file:$line_num"
    echo "  $content"
    echo ""
    VIOLATIONS=$((VIOLATIONS + 1))
}

echo "Multi-Asset Purity Lint"
echo "======================="
echo ""

# --- Check 1: Hardcoded instrument codes in string comparisons ---
echo "Checking for hardcoded instrument code comparisons..."

while IFS=: read -r file line_num content; do
    [ -z "$file" ] && continue
    # Skip lines that are only comments
    trimmed="${content#"${content%%[! ]*}"}"
    [[ "$trimmed" == //* ]] && continue
    report_violation "HARDCODED_INSTRUMENT" "$file" "$line_num" "$content"
done < <(grep -rn \
    "${GREP_EXCLUDES[@]}" \
    -E '(==|!=)\s*"(GBP|USD|EUR|JPY|CHF|CAD|AUD|NZD|KWH|MWH|GPU_HOUR|CARBON_CREDIT|CARBON_TONNE|GAS|WATER)"' \
    "$REPO_ROOT/services" "$REPO_ROOT/shared" 2>/dev/null || true)

# --- Check 2: Switch statements on instrument codes ---
echo "Checking for switch statements on instrument codes..."

while IFS=: read -r file line_num content; do
    [ -z "$file" ] && continue
    trimmed="${content#"${content%%[! ]*}"}"
    [[ "$trimmed" == //* ]] && continue
    report_violation "INSTRUMENT_SWITCH" "$file" "$line_num" "$content"
done < <(grep -rn \
    "${GREP_EXCLUDES[@]}" \
    -E 'case\s+"(GBP|USD|EUR|JPY|CHF|CAD|AUD|NZD|KWH|MWH|GPU_HOUR|CARBON_CREDIT|CARBON_TONNE|GAS|WATER)"' \
    "$REPO_ROOT/services" "$REPO_ROOT/shared" 2>/dev/null || true)

# --- Check 3: Deprecated shared/domain/money imports ---
echo "Checking for deprecated shared/domain/money imports..."

while IFS=: read -r file line_num content; do
    [ -z "$file" ] && continue
    report_violation "DEPRECATED_IMPORT" "$file" "$line_num" "$content"
done < <(grep -rn \
    "${GREP_EXCLUDES[@]}" \
    -E '"github\.com/meridianhub/meridian/shared/domain/money"' \
    "$REPO_ROOT/services" "$REPO_ROOT/shared" 2>/dev/null || true)

# --- Check 4: Hardcoded precision defaults ---
echo "Checking for hardcoded precision fallbacks..."

while IFS=: read -r file line_num content; do
    [ -z "$file" ] && continue
    trimmed="${content#"${content%%[! ]*}"}"
    [[ "$trimmed" == //* ]] && continue
    report_violation "HARDCODED_PRECISION" "$file" "$line_num" "$content"
done < <(grep -rn \
    "${GREP_EXCLUDES[@]}" \
    -E '(defaultPrecision|precision)\s*(=|:=)\s*2\b' \
    "$REPO_ROOT/services" "$REPO_ROOT/shared" 2>/dev/null || true)

# --- Check 5: currency.ByCode usage (should migrate to Reference Data) ---
echo "Checking for currency.ByCode usage..."

while IFS=: read -r file line_num content; do
    [ -z "$file" ] && continue
    report_violation "CURRENCY_REGISTRY" "$file" "$line_num" "$content"
done < <(grep -rn \
    "${GREP_EXCLUDES[@]}" \
    -E 'currency\.ByCode\(' \
    "$REPO_ROOT/services" "$REPO_ROOT/shared" 2>/dev/null || true)

# --- Summary ---
echo ""
echo "======================="

if [ "$VIOLATIONS" -gt 0 ]; then
    echo -e "${RED}Found $VIOLATIONS NEW violation(s).${NC}"
    if [ "$KNOWN_VIOLATIONS" -gt 0 ]; then
        echo -e "${YELLOW}($KNOWN_VIOLATIONS known violations tracked in audit document)${NC}"
    fi
    echo ""
    echo "New violations must be resolved before merge."
    echo "If a violation is intentional, add it to the known violations list in this script"
    echo "and document the reason in docs/audit/multi-asset-purity.md."
    exit 1
elif [ "$KNOWN_VIOLATIONS" -gt 0 ]; then
    if [ "$STRICT" = "--strict" ]; then
        echo -e "${RED}Strict mode: $KNOWN_VIOLATIONS known violation(s) must also be resolved.${NC}"
        echo "See docs/audit/multi-asset-purity.md for details."
        exit 1
    fi
    echo -e "${GREEN}No new violations.${NC}"
    echo -e "${YELLOW}$KNOWN_VIOLATIONS known violation(s) tracked for remediation.${NC}"
    echo "See docs/audit/multi-asset-purity.md for details."
    exit 0
else
    echo -e "${GREEN}No violations detected.${NC}"
    exit 0
fi
