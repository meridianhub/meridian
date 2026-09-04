#!/usr/bin/env bash
# check-service-coverage.sh
#
# Runs unit tests (with -short to skip integration tests) for each Go service
# and fails if any service falls below the minimum coverage threshold.
#
# Exclusions are read from codecov.yml (single source of truth).
# This script converts codecov glob patterns to grep filters applied to
# Go coverprofiles, so both Codecov and this script enforce the same rules.
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

# Validate THRESHOLD is a non-negative integer in range 0-100
if ! [[ "${THRESHOLD}" =~ ^[0-9]+$ ]] || [ "${THRESHOLD}" -gt 100 ]; then
    echo "ERROR: COVERAGE_THRESHOLD must be an integer between 0 and 100 (got: '${THRESHOLD}')" >&2
    exit 1
fi

# Build exclude pattern from codecov.yml (single source of truth for exclusions).
# See scripts/codecov-exclude-pattern.sh for the glob-to-grep conversion logic.
EXCLUDE_PATTERN=""
exclude_script="${REPO_ROOT}/scripts/codecov-exclude-pattern.sh"
if [ -x "${exclude_script}" ]; then
    if exclude=$("${exclude_script}"); then
        EXCLUDE_PATTERN="${exclude}"
        echo "Exclude pattern (from codecov.yml): ${EXCLUDE_PATTERN}"
    else
        echo "WARNING: Failed to read exclusions from codecov.yml"
    fi
else
    echo "WARNING: ${exclude_script} not found, running without exclusions"
fi

# A package whose TestMain short-circuits under -short never runs any of its
# tests, so every statement in it counts as uncovered here while the integration
# job exercises it fully. Counting those measures this gate's own -short flag
# rather than the service's unit coverage, so such packages are excluded from
# this check only. They stay in codecov.yml, where the integration run covers
# them.
#
# The guard must be in TestMain: a per-test `t.Skip` leaves the rest of the
# package running, and excluding that package would hide genuinely unit-tested
# code from the gate.
INTEGRATION_ONLY_PATTERN=""
while IFS= read -r test_file; do
    [ -n "${test_file}" ] || continue
    grep -q 'func TestMain(' "${test_file}" || continue
    awk '/func TestMain\(/ { in_main = 1 }
         in_main && /testing\.Short\(\)/ { found = 1; exit }
         in_main && /^}/ { exit }
         END { exit !found }' "${test_file}" || continue

    rel="$(dirname "${test_file}")"
    rel="${rel#"${REPO_ROOT}/"}"
    case "|${INTEGRATION_ONLY_PATTERN}|" in
        *"|${rel}/|"*) continue ;;
    esac
    if [ -n "${INTEGRATION_ONLY_PATTERN}" ]; then
        INTEGRATION_ONLY_PATTERN="${INTEGRATION_ONLY_PATTERN}|${rel}/"
    else
        INTEGRATION_ONLY_PATTERN="${rel}/"
    fi
done < <(find "${REPO_ROOT}/services" "${REPO_ROOT}/shared" -name '*_test.go' -type f 2>/dev/null | sort)

if [ -n "${INTEGRATION_ONLY_PATTERN}" ]; then
    echo "Packages skipped wholesale by -short, excluded from the measurement: ${INTEGRATION_ONLY_PATTERN}"
    if [ -n "${EXCLUDE_PATTERN}" ]; then
        EXCLUDE_PATTERN="${EXCLUDE_PATTERN}|${INTEGRATION_ONLY_PATTERN}"
    else
        EXCLUDE_PATTERN="${INTEGRATION_ONLY_PATTERN}"
    fi
fi

FAILED=0
PASSED=0
SKIPPED=0

echo ""
echo "Per-service Go coverage check (threshold: ${THRESHOLD}%)"
echo "Using -short flag to skip integration tests"
echo ""

for service_dir in "${REPO_ROOT}"/services/*/; do
    service="$(basename "${service_dir}")"

    # Skip non-Go directories (e.g., README, embed.go top-level items)
    if [ ! -d "${service_dir}" ]; then
        continue
    fi

    # Skip if no Go source files.
    # find is not piped into grep here: grep -q exits at the first match, find
    # then dies on SIGPIPE, and under `set -o pipefail` that non-zero status made
    # the guard report "no Go source files" for a service that has them. The
    # result was a gate that silently skipped a different subset of services on
    # every run. -print -quit stops find at the first hit with no pipe involved.
    if [ -z "$(find "${service_dir}" -maxdepth 5 -name "*.go" -not -name "*_test.go" -print -quit)" ]; then
        echo "  SKIP ${service} (no Go source files)"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi

    # Skip if no test files
    if [ -z "$(find "${service_dir}" -maxdepth 5 -name "*_test.go" -print -quit)" ]; then
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

    # Filter coverprofile using exclusions derived from codecov.yml
    target_file="${coverage_file}"
    if [ -n "${EXCLUDE_PATTERN}" ]; then
        filtered_file="${TMPDIR}/meridian_coverage_${service}_filtered.out"
        head -1 "${coverage_file}" > "${filtered_file}"
        tail -n +2 "${coverage_file}" \
            | grep -v -E "${EXCLUDE_PATTERN}" \
            >> "${filtered_file}" || true
        rm -f "${coverage_file}"
        target_file="${filtered_file}"
    fi

    if ! coverage="$(go tool cover -func="${target_file}" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"; then
        echo "  SKIP ${service} (cover command failed)"
        SKIPPED=$((SKIPPED + 1))
        rm -f "${target_file}"
        continue
    fi
    rm -f "${target_file}"

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
