#!/usr/bin/env bash
# verify-service-conventions.sh - Check Meridian service conventions
#
# Scans the codebase for convention violations across four areas:
#   1. File size limits (error >800 lines for non-test, non-pb Go files)
#   2. time.Sleep in test files (should use shared/platform/await instead)
#   3. Proto freshness (generated .pb.go files match .proto sources)
#   4. Stale or incomplete //nolint directives
#
# Note: doc.go presence and service/server.go naming are enforced by
# tests/architecture/structure_test.go and are not duplicated here.
#
# Usage:
#   ./scripts/verify-service-conventions.sh            # check all
#   ./scripts/verify-service-conventions.sh [service]  # scope to one service
#
# Exit codes:
#   0 - all checks pass
#   1 - one or more errors detected
#
# Escape hatches:
#   //meridian:large-file  Add anywhere in a file (gofmt may place it after
#                           the package doc block) to exempt it from the
#                           800-line limit. Use sparingly.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_FILTER="${1:-}"
ERRORS=0

# Colors (disabled when not writing to a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    YELLOW='\033[0;33m'
    GREEN='\033[0;32m'
    CYAN='\033[0;36m'
    BOLD='\033[1m'
    NC='\033[0m'
else
    RED=''
    YELLOW=''
    GREEN=''
    CYAN=''
    BOLD=''
    NC=''
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log_error() {
    echo -e "${RED}ERROR${NC} $*"
    ERRORS=$((ERRORS + 1))
}

log_ok() {
    echo -e "${GREEN}OK${NC}    $*"
}

log_section() {
    echo ""
    echo -e "${BOLD}${CYAN}── $* ──${NC}"
}

# Returns the service name from a file path (e.g. "party" from "services/party/...")
service_from_path() {
    echo "${1#services/}" | cut -d/ -f1
}

# Returns true if $1 should be skipped due to SERVICE_FILTER
skip_service() {
    [ -n "$SERVICE_FILTER" ] && [ "$SERVICE_FILTER" != "$(service_from_path "$1")" ]
}

# ---------------------------------------------------------------------------
# Check 1: File size (error >800 lines for non-test, non-pb Go files)
#
# Files with a //meridian:large-file comment anywhere in the file are exempt.
# Note: tests/architecture/size_test.go enforces the same limit via a known-
# oversize allowlist. This check provides fast feedback before running Go tests.
# ---------------------------------------------------------------------------

check_file_size() {
    log_section "File Size Limits (>800 lines)"

    local found=0

    while IFS= read -r file; do
        # Skip if service filter is active and file doesn't match
        skip_service "$file" && continue

        # Honour escape hatch: //meridian:large-file anywhere in the file.
        # gofmt may place this comment after the package doc block, so we
        # scan the whole file rather than just the first N lines.
        if grep -q "//meridian:large-file" "${REPO_ROOT}/${file}" 2>/dev/null; then
            continue
        fi

        local lines
        lines=$(wc -l < "${REPO_ROOT}/${file}")

        if [ "$lines" -gt 800 ]; then
            log_error "${file} has ${lines} lines (limit: 800)"
            echo "       Fix: split into smaller files or extract helpers"
            echo "       Exempt: add '//meridian:large-file' comment anywhere in the file"
            found=1
        fi
    done < <(
        cd "$REPO_ROOT"
        find services shared -name "*.go" \
            ! -name "*_test.go" \
            ! -name "*.pb.go" \
            ! -name "*.pb.gw.go" \
            -type f 2>/dev/null | sort
    )

    if [ "$found" -eq 0 ]; then
        log_ok "No files exceed 800 lines"
    fi
}

# ---------------------------------------------------------------------------
# Check 2: time.Sleep in test files (should use shared/platform/await)
#
# Uses a ratchet: the check fails only if the count INCREASES above the
# baseline. This prevents new violations while existing ones are cleaned up.
# Baseline last measured: 2025-03-19 (develop branch at 6da0c66a)
# To reduce the baseline: fix time.Sleep usages, then lower the constant.
# ---------------------------------------------------------------------------

# BASELINE_TIME_SLEEP is the number of test files using time.Sleep at the
# time this check was promoted to an error. Do NOT increase this value.
BASELINE_TIME_SLEEP=88

check_time_sleep() {
    log_section "time.Sleep in Tests (use shared/platform/await)"

    local count=0
    local files=()

    while IFS= read -r file; do
        skip_service "$file" && continue
        files+=("$file")
        count=$((count + 1))
    done < <(
        cd "$REPO_ROOT"
        grep -rl "time\.Sleep" \
            --include="*_test.go" \
            services/ shared/ cmd/ tests/ 2>/dev/null | sort || true
    )

    if [ "$count" -eq 0 ]; then
        log_ok "No time.Sleep found in test files"
        return
    fi

    if [ "$count" -gt "$BASELINE_TIME_SLEEP" ]; then
        log_error "${count} test file(s) use time.Sleep — increased from baseline of ${BASELINE_TIME_SLEEP}"
        echo "       Fix: replace new usages with await.Until() or await.UntilNoError()"
        echo "            import: github.com/meridianhub/meridian/shared/platform/await"

        local shown=0
        for f in "${files[@]}"; do
            [ "$shown" -ge 10 ] && break
            echo "       - ${f}"
            shown=$((shown + 1))
        done
        if [ "${#files[@]}" -gt 10 ]; then
            echo "       (showing first 10 of ${count})"
        fi
    else
        echo -e "       ${YELLOW}RATCHET${NC}  ${count} test file(s) use time.Sleep (baseline: ${BASELINE_TIME_SLEEP})"
        echo "       Hint: reduce by replacing time.Sleep with await.Until()"
        if [ "$count" -lt "$((BASELINE_TIME_SLEEP - 5))" ]; then
            echo "       HINT: Baseline can be reduced from ${BASELINE_TIME_SLEEP} to ${count} — update BASELINE_TIME_SLEEP in verify-service-conventions.sh"
        fi
    fi
}

# ---------------------------------------------------------------------------
# Check 3: Proto freshness (generated files match .proto sources)
# ---------------------------------------------------------------------------

check_proto_freshness() {
    log_section "Proto Freshness (generated files up to date)"

    if ! command -v buf >/dev/null 2>&1; then
        echo "       Skipped: buf not installed (install with: brew install bufbuild/buf/buf)"
        return
    fi

    cd "$REPO_ROOT"

    # Regenerate protos in place, then check if the working tree changed.
    # buf.gen.yaml outputs to api/proto (pb.go files) and api/openapi (Swagger).
    # Restore all generated outputs afterwards so the script is side-effect-free.
    if ! buf generate 2>/dev/null; then
        log_error "buf generate failed — cannot check proto freshness"
        return
    fi

    # Check all outputs declared in buf.gen.yaml: api/proto and api/openapi.
    # Use git status --short to catch both modified tracked files AND new
    # untracked files (e.g. a new .proto generates a new .pb.go that was
    # never committed). git diff alone misses untracked files.
    local stale_files
    stale_files=$(git status --short -- api/proto/ api/openapi/ 2>/dev/null | awk '{print $2}')

    if [ -z "$stale_files" ]; then
        log_ok "Generated proto files are up to date"
    else
        log_error "Generated files are stale — run 'buf generate' and commit the result"
        echo "       Fix: buf generate"
        echo "$stale_files" | while IFS= read -r f; do
            echo "       - ${f}"
        done
        # Restore tracked files and remove untracked generated files to leave
        # the working tree clean.
        git checkout -- api/proto/ api/openapi/ 2>/dev/null || true
        git clean -f -- api/proto/ api/openapi/ 2>/dev/null || true
    fi
}

# ---------------------------------------------------------------------------
# Check 4: Stale or incomplete //nolint directives
#
# Note: golangci-lint runs nolintlint with require-specific and require-
# explanation (task 29). This check provides fast feedback before linting.
#
# Valid:   //nolint:errcheck // reason for suppression
# Invalid: //nolint                     (no linter specified)
# Invalid: //nolint:errcheck            (no explanation comment)
# ---------------------------------------------------------------------------

check_nolint_directives() {
    log_section "//nolint Directive Quality"

    local count=0
    local matches=()

    # (a) Bare //nolint with no linter name (no colon follows)
    while IFS= read -r match; do
        matches+=("$match")
        count=$((count + 1))
    done < <(
        cd "$REPO_ROOT"
        grep -rnE "//nolint($|[^:])" \
            --include="*.go" \
            services/ shared/ cmd/ tests/ 2>/dev/null | sort || true
    )

    # (b) //nolint:linter with no explanation (no // after the linter list)
    while IFS= read -r match; do
        matches+=("$match")
        count=$((count + 1))
    done < <(
        cd "$REPO_ROOT"
        grep -rnE "//nolint:[a-zA-Z]" \
            --include="*.go" \
            services/ shared/ cmd/ tests/ 2>/dev/null \
        | grep -vE "//nolint:.*// " | sort || true
    )

    if [ "$count" -eq 0 ]; then
        log_ok "All //nolint directives specify a linter name and explanation"
        return
    fi

    log_error "${count} //nolint directive(s) missing linter name or explanation"
    echo "       Valid format: //nolint:lintername // reason for suppression"

    local shown=0
    for m in "${matches[@]}"; do
        [ "$shown" -ge 10 ] && break
        echo "       - ${m}"
        shown=$((shown + 1))
    done
    if [ "${#matches[@]}" -gt 10 ]; then
        echo "       (showing first 10 of ${count})"
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

echo -e "${BOLD}Meridian Service Convention Verification${NC}"
if [ -n "$SERVICE_FILTER" ]; then
    echo "Scope: services/${SERVICE_FILTER}"
fi
echo ""

check_file_size
check_time_sleep
check_proto_freshness
check_nolint_directives

# Summary
echo ""
echo -e "${BOLD}── Summary ──${NC}"

if [ "$ERRORS" -gt 0 ]; then
    echo -e "${RED}${ERRORS} error(s) detected — CI will fail${NC}"
    exit 1
fi

echo -e "${GREEN}All convention checks passed${NC}"
exit 0
