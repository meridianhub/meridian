#!/usr/bin/env bash
# codecov-exclude-pattern.sh
#
# Reads codecov.yml ignore globs and outputs a grep -E pattern for filtering
# Go coverprofile lines. This is the bridge that keeps the per-service coverage
# script and CI workflow filters aligned with codecov.yml (single source of truth).
#
# Usage:
#   pattern=$(./scripts/codecov-exclude-pattern.sh)
#   grep -v -E "$pattern" coverage.out > coverage-filtered.out
#
# Handles three glob categories:
#   **/*.ext      -> file extension match (\.ext:)
#   **/dir/**     -> directory match (/dir/)
#   path/to/file  -> exact path match (path/to/file:)
#
# Non-Go patterns (frontend/*, utilities/*) are skipped.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CODECOV_YML="${1:-${REPO_ROOT}/codecov.yml}"

if [ ! -f "${CODECOV_YML}" ]; then
    echo "ERROR: codecov.yml not found at ${CODECOV_YML}" >&2
    exit 1
fi

if ! command -v yq &>/dev/null; then
    echo "ERROR: yq is required but not found" >&2
    exit 1
fi

patterns=()

while IFS= read -r glob; do
    case "${glob}" in
        # File extension: **/*.pb.go -> \.pb\.go:
        \*\*/\**)
            ext="${glob##*\*}"
            patterns+=("$(echo "${ext}" | sed 's/\./\\./g'):")
            ;;
        # Directory: **/cmd/** -> /cmd/
        \*\*/*\*\*)
            dir="${glob#\*\*/}"
            dir="${dir%/\*\*}"
            patterns+=("/${dir}/")
            ;;
        # Skip non-Go paths
        frontend/*|utilities/*) continue ;;
        # Specific file: path/to/file.go -> path/to/file\.go:
        *)
            patterns+=("$(echo "${glob}" | sed 's/\./\\./g'):")
            ;;
    esac
done < <(yq -r '.ignore[]' "${CODECOV_YML}" 2>/dev/null)

if [ ${#patterns[@]} -eq 0 ]; then
    echo "WARNING: No exclude patterns found in ${CODECOV_YML}" >&2
    exit 1
fi

IFS='|'
echo "${patterns[*]}"
