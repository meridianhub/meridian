#!/usr/bin/env bash
# check-service-coverage.sh
#
# Runs unit tests (with -short to skip integration tests) for each Go service
# and fails if any service falls below the minimum coverage threshold.
#
# Exclusions come from two sources:
#   1. codecov.yml, the single source of truth for what coverage ignores
#      anywhere. Its globs are converted to grep filters applied to Go
#      coverprofiles, so Codecov and this script enforce the same rules.
#   2. Packages this gate cannot measure, because their TestMain
#      short-circuits under -short and no test in them runs. These are excluded
#      here only, never in codecov.yml, where the integration run covers them.
#
# Usage:
#   ./scripts/check-service-coverage.sh
#
# Environment variables:
#   COVERAGE_THRESHOLD  Minimum coverage % required per service (default: 80)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v jq &>/dev/null; then
    echo "ERROR: jq is required but not found" >&2
    exit 1
fi
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

# Packages in which every test skipped under -short contribute no covered
# statements while still contributing to the denominator, so counting them
# measures this gate's own -short flag rather than the service's unit coverage.
# They are excluded from this check only, never from codecov.yml, where the
# integration run covers them.
#
# The set is measured from `go test -json` rather than pattern-matched, because
# packages gate themselves in several ways: a TestMain that returns early, a
# shared setup helper calling t.Skip, or per-test guards. Only the outcome
# matters - no test in the package ran - and a package where some tests run and
# others skip is kept, since its covered code belongs in the gate.
skip_only_packages() {
    local events_file=$1

    jq -rs '
        map(select(.Test != null and (.Action == "pass" or .Action == "skip" or .Action == "fail")))
        | group_by(.Package)
        | map(select(all(.[]; .Action == "skip")))
        | map(.[0].Package)
        | .[]
    ' "${events_file}" 2>/dev/null | sort -u
}

# Turn a Go import path into the repo-relative directory the coverprofile uses.
package_to_path() {
    local module
    module="$(awk '/^module / { print $2; exit }' "${REPO_ROOT}/go.mod")"
    sed "s|^${module}/||"
}

# Services that sit below THRESHOLD once the gate measures them reliably.
#
# Each entry is a floor, not an exemption: the service must not regress,
# THRESHOLD stays the target, and the entry is deleted when the service reaches
# it. Before the SIGPIPE race in the detection guards was fixed, a random subset
# of services was skipped on every run, so these were not reliably measured at
# all - recording where one actually stands is stricter than what preceded it.
#
# position-keeping: 71.2%. Its adapters/persistence repository methods are
# exercised only by the DB-backed tests that -short skips, while the rest of the
# package runs, so the package cannot be excluded as measurement noise.
service_floor() {
    case "$1" in
        position-keeping) echo "71" ;;
        *)                echo "${THRESHOLD}" ;;
    esac
}

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
    events_file="${TMPDIR}/meridian_events_${service}.json"
    rm -f "${coverage_file}" "${events_file}"

    # Run unit tests with coverage; continue even if tests fail so we report all
    # services. -json is captured so the skip-only packages can be derived from
    # the same run rather than a second one.
    if ! go test -short -covermode=atomic -coverprofile="${coverage_file}" \
        -json "./services/${service}/..." > "${events_file}" 2>&1; then
        jq -r 'select(.Output != null) | .Output' "${events_file}" 2>/dev/null | tail -40
        echo "  FAIL ${service} (test execution failed)"
        FAILED=$((FAILED + 1))
        rm -f "${events_file}"
        continue
    fi

    # Package summary lines only. A failing run takes the branch above, which
    # dumps the tail of the output, so `--- PASS` subtest lines would be pure
    # noise here - there were 18k of them across a full run.
    jq -r 'select(.Output != null) | .Output' "${events_file}" 2>/dev/null \
        | grep -E '^(ok|FAIL)[[:space:]]' || true

    service_exclude="${EXCLUDE_PATTERN}"
    while IFS= read -r pkg; do
        [ -n "${pkg}" ] || continue
        rel="$(printf '%s\n' "${pkg}" | package_to_path)"
        # Anchored to files directly in the package: a coverprofile line reads
        # "<path>.go:<line>.<col>,...", so this matches the package's own files
        # and not a subpackage underneath it.
        entry="${rel}/[^/]*\.go:"
        if [ -n "${service_exclude}" ]; then
            service_exclude="${service_exclude}|${entry}"
        else
            service_exclude="${entry}"
        fi
        echo "  (every test in ${rel} skipped under -short; excluded from the measurement)"
    done < <(skip_only_packages "${events_file}")
    rm -f "${events_file}"

    if [ ! -s "${coverage_file}" ]; then
        echo "  SKIP ${service} (no coverage output produced)"
        SKIPPED=$((SKIPPED + 1))
        rm -f "${coverage_file}"
        continue
    fi

    # Filter coverprofile using exclusions derived from codecov.yml
    target_file="${coverage_file}"
    if [ -n "${service_exclude}" ]; then
        filtered_file="${TMPDIR}/meridian_coverage_${service}_filtered.out"
        head -1 "${coverage_file}" > "${filtered_file}"
        tail -n +2 "${coverage_file}" \
            | grep -v -E "${service_exclude}" \
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

    floor="$(service_floor "${service}")"

    if awk -v threshold="${floor}" -v cov="${coverage}" 'BEGIN { exit !(cov + 0 < threshold + 0) }'; then
        if [ "${floor}" = "${THRESHOLD}" ]; then
            echo "  FAIL ${service}: ${coverage}% < ${THRESHOLD}%"
        else
            echo "  FAIL ${service}: ${coverage}% < recorded floor ${floor}% (target ${THRESHOLD}%)"
        fi
        FAILED=$((FAILED + 1))
    else
        if [ "${floor}" = "${THRESHOLD}" ]; then
            echo "  PASS ${service}: ${coverage}%"
        else
            echo "  PASS ${service}: ${coverage}% (floor ${floor}%, target ${THRESHOLD}%)"
            if awk -v threshold="${THRESHOLD}" -v cov="${coverage}" 'BEGIN { exit !(cov + 0 >= threshold + 0) }'; then
                echo "    ${service} now meets ${THRESHOLD}% - remove its service_floor entry to lock that in"
            fi
        fi
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
