#!/usr/bin/env bash
# migrate-all-orgs.sh
# Apply Atlas migrations to all active organization schemas
#
# This script queries active organizations via the Organization Service's gRPC API
# and applies identical schema migrations to each org's isolated PostgreSQL schema.
#
# Usage:
#   export DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=disable"
#   export ORGANIZATION_SERVICE_URL="localhost:9090"  # Optional, defaults to localhost:9090
#   ./scripts/migrate-all-orgs.sh [--service SERVICE] [--dry-run]
#
# Options:
#   --service SERVICE  Apply migrations only for specified service (e.g., current-account)
#   --dry-run          Show what would be done without executing

set -euo pipefail

# Configuration
DB_URL="${DATABASE_URL:?DATABASE_URL environment variable required}"
ORG_SERVICE_URL="${ORGANIZATION_SERVICE_URL:-localhost:9090}"

# Script directory for relative paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Parse arguments
SERVICE_FILTER=""
DRY_RUN=false

show_help() {
    cat <<EOF
Usage: $0 [OPTIONS]

Apply Atlas migrations to all active organization schemas.

Options:
  --service SERVICE  Apply migrations only for specified service (e.g., current-account)
  --dry-run          Show what would be done without executing
  -h, --help         Show this help message

Environment Variables:
  DATABASE_URL             Required. PostgreSQL connection string
  ORGANIZATION_SERVICE_URL Optional. gRPC address (default: localhost:9090)

Examples:
  $0                           # Migrate all services for all active orgs
  $0 --service current-account # Migrate only current-account service
  $0 --dry-run                 # Preview what would be migrated
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --service)
            SERVICE_FILTER="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            show_help
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [--service SERVICE] [--dry-run] [-h|--help]"
            exit 1
            ;;
    esac
done

# Function to mask credentials in database URL for logging
mask_db_url() {
    # Remove user:password@ portion from URL for safe logging
    echo "$1" | sed -E 's|(postgres(ql)?://)([^:]+:[^@]+@)|\1***:***@|'
}

# Function to build URL with search_path parameter
build_url_with_schema() {
    local base_url="$1"
    local schema="$2"
    if [[ "$base_url" == *"?"* ]]; then
        echo "${base_url}&search_path=${schema}"
    else
        echo "${base_url}?search_path=${schema}"
    fi
}

echo "============================================"
echo "Multi-Organization Atlas Migration"
echo "============================================"
echo "Database URL: $(mask_db_url "$DB_URL")"
echo "Organization Service: $ORG_SERVICE_URL"
echo "Dry run: $DRY_RUN"
echo ""

# Check if orgctl is available
if ! command -v orgctl &> /dev/null; then
    # Try building it if in the project
    if [[ -f "$PROJECT_ROOT/cmd/orgctl/main.go" ]]; then
        echo "Building orgctl..."
        go build -o "$PROJECT_ROOT/dist/orgctl" "$PROJECT_ROOT/cmd/orgctl"
        ORGCTL="$PROJECT_ROOT/dist/orgctl"
    else
        echo "Error: orgctl not found. Please build it with: go build -o dist/orgctl ./cmd/orgctl"
        exit 1
    fi
else
    ORGCTL="orgctl"
fi

# Query active organizations via Organization Service
echo "Querying active organizations from Organization Service..."
ORGS_OUTPUT=$($ORGCTL list --status=active 2>&1) || {
    echo "Error: Failed to query organizations from Organization Service"
    echo "$ORGS_OUTPUT"
    echo ""
    echo "Ensure Organization Service is running at $ORG_SERVICE_URL"
    exit 1
}

# Parse organization IDs from tabular output (skip header lines)
ORGS=$(echo "$ORGS_OUTPUT" | tail -n +3 | awk '{print $1}' | grep -v '^$' || true)

if [[ -z "$ORGS" ]]; then
    echo "No active organizations found"
    exit 0
fi

echo ""
echo "Found active organizations:"
echo "$ORGS" | sed 's/^/  - /'
echo ""

# Track failures for reporting
declare -a FAILED_ORGS=()
declare -a SUCCESS_ORGS=()

# Find Atlas configurations to process
if [[ -n "$SERVICE_FILTER" ]]; then
    CONFIGS=("$PROJECT_ROOT/services/$SERVICE_FILTER/atlas/atlas.hcl")
    if [[ ! -f "${CONFIGS[0]}" ]]; then
        echo "Error: Atlas config not found for service: $SERVICE_FILTER"
        exit 1
    fi
else
    # Find all service atlas configs (exclude shared)
    mapfile -t CONFIGS < <(find "$PROJECT_ROOT/services" -name "atlas.hcl" -type f | sort)
fi

if [[ ${#CONFIGS[@]} -eq 0 ]]; then
    echo "No Atlas configurations found"
    exit 1
fi

echo "Services to migrate:"
for CONFIG in "${CONFIGS[@]}"; do
    SERVICE=$(basename "$(dirname "$(dirname "$CONFIG")")")
    echo "  - $SERVICE"
done
echo ""

# Apply migrations to each organization schema
for CONFIG in "${CONFIGS[@]}"; do
    SERVICE=$(basename "$(dirname "$(dirname "$CONFIG")")")

    echo "============================================"
    echo "Service: $SERVICE"
    echo "============================================"

    while IFS= read -r ORG_ID; do
        # Trim whitespace
        ORG_ID=$(echo "$ORG_ID" | xargs)
        if [[ -z "$ORG_ID" ]]; then
            continue
        fi

        ORG_SCHEMA="org_${ORG_ID}"
        echo "  Migrating schema: $ORG_SCHEMA"

        if [[ "$DRY_RUN" == "true" ]]; then
            echo "    [DRY RUN] Would apply migrations to $ORG_SCHEMA"
            SUCCESS_ORGS+=("$ORG_ID:$SERVICE")
            continue
        fi

        # Apply migrations using search_path to target specific organization schema
        # The search_path URL parameter redirects Atlas to the org-specific schema
        if atlas migrate apply \
            --env local \
            --config "$CONFIG" \
            --url "$(build_url_with_schema "$DB_URL" "$ORG_SCHEMA")" \
            --tx-mode none 2>&1; then
            echo "    ✓ $ORG_SCHEMA complete"
            SUCCESS_ORGS+=("$ORG_ID:$SERVICE")
        else
            echo "    ✗ $ORG_SCHEMA failed"
            FAILED_ORGS+=("$ORG_ID:$SERVICE")
        fi
    done <<< "$ORGS"

    echo ""
done

# Summary
echo "============================================"
echo "Migration Summary"
echo "============================================"
echo "Successful: ${#SUCCESS_ORGS[@]}"
echo "Failed: ${#FAILED_ORGS[@]}"

if [[ ${#FAILED_ORGS[@]} -gt 0 ]]; then
    echo ""
    echo "Failed migrations:"
    for FAILURE in "${FAILED_ORGS[@]}"; do
        echo "  ✗ $FAILURE"
    done
    exit 1
fi

echo ""
echo "✓ All organization migrations applied successfully"
