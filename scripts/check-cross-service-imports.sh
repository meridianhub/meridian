#!/usr/bin/env bash
# check-cross-service-imports.sh
#
# CI check: Ensures no service's cmd/ directory imports another service's client package.
# Each BIAN service domain must be independently deployable with zero knowledge of other
# services. Cross-service coordination belongs in the saga orchestration layer.
#
# Usage: scripts/check-cross-service-imports.sh
# Exit code: 0 if clean, 1 if cross-service imports found.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SERVICES_DIR="${REPO_ROOT}/services"
VIOLATIONS=0

# Find all service directories
for service_dir in "${SERVICES_DIR}"/*/; do
    service_name="$(basename "$service_dir")"
    cmd_dir="${service_dir}cmd"

    # Skip services without a cmd/ directory
    if [ ! -d "$cmd_dir" ]; then
        continue
    fi

    # Scan Go files in cmd/ for imports of other service client packages
    for go_file in "${cmd_dir}"/*.go; do
        [ -f "$go_file" ] || continue

        # Extract import paths that match services/*/client
        while IFS= read -r import_line; do
            # Extract the service name from the import path
            # Pattern: "github.com/meridianhub/meridian/services/<other-service>/client"
            imported_service=$(echo "$import_line" | grep -oE 'services/[^/]+/client' | head -1 | cut -d'/' -f2)

            if [ -z "$imported_service" ]; then
                continue
            fi

            # Self-referential imports are allowed (service importing its own client)
            if [ "$imported_service" = "$service_name" ]; then
                continue
            fi

            echo "VIOLATION: services/${service_name}/cmd/$(basename "$go_file") imports services/${imported_service}/client"
            VIOLATIONS=$((VIOLATIONS + 1))
        done < <(grep -E '"github\.com/meridianhub/meridian/services/[^"]+/client"' "$go_file" 2>/dev/null || true)
    done
done

if [ "$VIOLATIONS" -gt 0 ]; then
    echo ""
    echo "Found ${VIOLATIONS} cross-service import violation(s)."
    echo "Each service must be independently deployable. Cross-service coordination"
    echo "belongs in the saga orchestration layer (shared/pkg/saga/)."
    exit 1
fi

echo "No cross-service import violations found."
exit 0
