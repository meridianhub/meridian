#!/usr/bin/env bash
# check-service-coverage.sh
#
# Runs unit tests (with -short to skip integration tests) for each Go service
# and fails if any service falls below the minimum coverage threshold.
#
# Usage:
#   ./scripts/check-service-coverage.sh
#
# Environment variables:
#   COVERAGE_THRESHOLD  Minimum coverage % required per service (default: 80)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
THRESHOLD="${COVERAGE_THRESHOLD:-80}"
TMPDIR="${TMPDIR:-/tmp}"

# Validate THRESHOLD is a non-negative integer in range 0–100
if ! [[ "${THRESHOLD}" =~ ^[0-9]+$ ]] || [ "${THRESHOLD}" -gt 100 ]; then
    echo "ERROR: COVERAGE_THRESHOLD must be an integer between 0 and 100 (got: '${THRESHOLD}')" >&2
    exit 1
fi

FAILED=0
PASSED=0
SKIPPED=0

echo "Per-service Go coverage check (threshold: ${THRESHOLD}%)"
echo "Using -short flag to skip integration tests"
echo ""

for service_dir in "${REPO_ROOT}"/services/*/; do
    service="$(basename "${service_dir}")"

    # Skip non-Go directories (e.g., README, embed.go top-level items)
    if [ ! -d "${service_dir}" ]; then
        continue
    fi

    # Skip if no Go source files
    if ! find "${service_dir}" -name "*.go" -not -name "*_test.go" -maxdepth 5 | grep -q .; then
        echo "  SKIP ${service} (no Go source files)"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi

    # Skip if no test files
    if ! find "${service_dir}" -name "*_test.go" -maxdepth 5 | grep -q .; then
        echo "  SKIP ${service} (no test files)"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi

    coverage_file="${TMPDIR}/meridian_coverage_${service}.out"
    rm -f "${coverage_file}"

    # Run unit tests with coverage; continue even if tests fail so we report all services
    if ! go test -short -covermode=atomic -coverprofile="${coverage_file}" \
        "./services/${service}/..." 2>&1; then
        echo "  FAIL ${service} (test execution failed)"
        FAILED=$((FAILED + 1))
        continue
    fi

    if [ ! -s "${coverage_file}" ]; then
        echo "  SKIP ${service} (no coverage output produced)"
        SKIPPED=$((SKIPPED + 1))
        rm -f "${coverage_file}"
        continue
    fi

    if ! coverage="$(go tool cover -func="${coverage_file}" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"; then
        echo "  SKIP ${service} (cover command failed)"
        SKIPPED=$((SKIPPED + 1))
        rm -f "${coverage_file}"
        continue
    fi
    rm -f "${coverage_file}"

    if [ -z "${coverage}" ]; then
        echo "  SKIP ${service} (could not parse coverage)"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi

    if awk -v threshold="${THRESHOLD}" -v cov="${coverage}" 'BEGIN { exit !(cov + 0 < threshold + 0) }'; then
        echo "  FAIL ${service}: ${coverage}% < ${THRESHOLD}%"
        FAILED=$((FAILED + 1))
    else
        echo "  PASS ${service}: ${coverage}%"
        PASSED=$((PASSED + 1))
    fi
done

echo ""
echo "Results: ${PASSED} passed, ${FAILED} failed, ${SKIPPED} skipped"

if [ "${FAILED}" -gt 0 ]; then
    echo ""
    echo "ERROR: ${FAILED} service(s) are below the ${THRESHOLD}% coverage threshold."
    exit 1
fi

echo "All services meet the ${THRESHOLD}% coverage threshold."
