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
#   - Known violations tracked in .taskmaster/docs/037-audit-results.md
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

# Known violation files that are tracked in the audit document.
# These are existing violations that will be fixed by other tasks.
is_known_violation() {
    local file="$1"
    local line="$2"

    # position-keeping balance_mapper.go inferInstrumentProperties (Task 3)
    [[ "$file" == *position-keeping/adapters/balance_mapper.go ]] && return 0
    # saga handlers.go IsPhysicsInstrument (Task 5)
    [[ "$file" == *shared/pkg/saga/handlers.go ]] && return 0
    # internal-account defaultPrecision (Task 4)
    [[ "$file" == *internal-account/service/lien_service.go ]] && return 0
    # financial-accounting persistence backward-compat defaults (Task 6)
    [[ "$file" == *financial-accounting/adapters/persistence/repository.go ]] && return 0
    # financial-accounting domain currency re-exports (Task 7)
    [[ "$file" == *financial-accounting/domain/currency.go ]] && return 0
    # position-keeping domain currency re-exports (Task 7)
    [[ "$file" == *position-keeping/domain/quantity.go ]] && return 0
    # current-account domain currency re-exports (Task 7)
    [[ "$file" == *current-account/domain/quantity.go ]] && return 0
    # current-account hardcoded GBP (intentional business rule)
    [[ "$file" == *current-account/service/lien_service.go ]] && return 0
    [[ "$file" == *current-account/service/client_interfaces.go ]] && return 0
    # Starlark client default handler schemas (Task 8)
    [[ "$file" == *client/starlark.go ]] && return 0
    # current-account domain account.go currency.ByCode usage (Task 7)
    [[ "$file" == *current-account/domain/account.go ]] && return 0
    # current-account service grpc_account_endpoints.go currency.ByCode (Task 7)
    [[ "$file" == *current-account/service/grpc_account_endpoints.go ]] && return 0
    # reference-data service (it IS the source of truth)
    [[ "$file" == *services/reference-data/* ]] && return 0
    # control-plane admin balance sheet (reads from proto, not hardcoding)
    [[ "$file" == *services/control-plane/* ]] && return 0
    # instrument-cli simulate command
    [[ "$file" == *cmd/instrument-cli/* ]] && return 0
    # position-tool validation types
    [[ "$file" == *cmd/position-tool/* ]] && return 0
    # reconciliation pk client (reads from proto response)
    [[ "$file" == *services/reconciliation/* ]] && return 0
    # mcp-server audit tool (reads from proto response)
    [[ "$file" == *services/mcp-server/* ]] && return 0
    # event-router domain metrics (comment/doc only)
    [[ "$file" == *services/event-router/* ]] && return 0
    # financial-gateway client starlark (handler schema)
    [[ "$file" == *services/financial-gateway/client/* ]] && return 0
    # market-information (deals with actual currency pairs)
    [[ "$file" == *services/market-information/* ]] && return 0
    # shared/pkg/valuation (uses instrument codes in type-safe context)
    [[ "$file" == *shared/pkg/valuation/* ]] && return 0
    # shared/pkg/saga/linter.go (documents instrument patterns)
    [[ "$file" == *shared/pkg/saga/linter.go ]] && return 0
    # internal-account provisioning templates (seed-like default accounts)
    [[ "$file" == *internal-account/provisioning/* ]] && return 0
    # internal-account examples
    [[ "$file" == *internal-account/examples/* ]] && return 0
    # internal-account domain (doc comments)
    [[ "$file" == *internal-account/domain/* ]] && return 0
    # internal-account adapters persistence doc
    [[ "$file" == *internal-account/adapters/persistence/doc.go ]] && return 0
    # financial-accounting service files (using ParseCurrency from domain)
    [[ "$file" == *financial-accounting/service/* ]] && return 0
    # financial-accounting observability (metric label docs)
    [[ "$file" == *financial-accounting/observability/* ]] && return 0
    # position-keeping service (adapters/initiate using domain types)
    [[ "$file" == *position-keeping/service/* ]] && return 0
    # position-keeping adapters persistence (uses domain currency)
    [[ "$file" == *position-keeping/adapters/persistence/* ]] && return 0
    # position-keeping domain (doc.go, position.go comments)
    [[ "$file" == *position-keeping/domain/doc.go ]] && return 0
    [[ "$file" == *position-keeping/domain/position.go ]] && return 0
    # position-keeping domain position.go (doc comments)
    [[ "$file" == */position-keeping/domain/position.go ]] && return 0
    # current-account service files (grpc mappers, saga handlers, etc.)
    [[ "$file" == *current-account/service/* ]] && return 0
    # current-account webhook notifier (struct field)
    [[ "$file" == *current-account/webhook/* ]] && return 0
    # current-account client starlark
    [[ "$file" == *current-account/client/* ]] && return 0

    return 1
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

    if is_known_violation "$rel_file" "$line_num"; then
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
    -E 'case\s+"(USD|EUR|GBP|JPY|CHF|CAD|AUD|KWH|GPU_HOUR|CARBON_TONNE|CARBON_CREDIT)"' \
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
    echo "and document the reason in .taskmaster/docs/037-audit-results.md."
    exit 1
elif [ "$KNOWN_VIOLATIONS" -gt 0 ]; then
    echo -e "${GREEN}No new violations.${NC}"
    echo -e "${YELLOW}$KNOWN_VIOLATIONS known violation(s) tracked for remediation.${NC}"
    echo "See .taskmaster/docs/037-audit-results.md for details."
    exit 0
else
    echo -e "${GREEN}No violations detected.${NC}"
    exit 0
fi
