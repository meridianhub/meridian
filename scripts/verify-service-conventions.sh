#!/usr/bin/env bash
# verify-service-conventions.sh - Check Meridian service conventions
#
# Scans the codebase for convention violations across five areas:
#   1. File size limits (warn >1000 lines for non-test, non-pb Go files)
#   2. time.Sleep in test files (should use shared/platform/await instead)
#   3. doc.go in shared packages (shared/platform/*, shared/pkg/*)
#   4. Proto freshness (generated .pb.go files match .proto sources)
#   5. service/server.go naming convention in service packages
#
# Usage:
#   ./scripts/verify-service-conventions.sh            # check all
#   ./scripts/verify-service-conventions.sh [service]  # scope to one service
#
# Exit codes:
#   0 - all checks pass (warnings reported but do not fail)
#   1 - one or more errors detected

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_FILTER="${1:-}"
ERRORS=0
WARNINGS=0

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

log_warn() {
    echo -e "${YELLOW}WARN${NC}  $*"
    WARNINGS=$((WARNINGS + 1))
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
# Check 1: File size (warn >1000 lines for non-test, non-pb Go files)
# ---------------------------------------------------------------------------

check_file_size() {
    log_section "File Size Limits (>1000 lines)"

    local found=0

    while IFS= read -r file; do
        # Skip if service filter is active and file doesn't match
        skip_service "$file" && continue

        # wc -l returns "   N filename" — extract the count
        local lines
        lines=$(wc -l < "${REPO_ROOT}/${file}")

        if [ "$lines" -gt 1000 ]; then
            log_warn "${file} has ${lines} lines (limit: 1000)"
            echo "       Fix: split into smaller files or extract helpers"
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
        log_ok "No files exceed 1000 lines"
    fi
}

# ---------------------------------------------------------------------------
# Check 2: time.Sleep in test files (should use shared/platform/await)
# ---------------------------------------------------------------------------

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

    log_warn "${count} test file(s) use time.Sleep instead of await"
    echo "       Fix: replace with await.Until() or await.UntilNoError()"
    echo "            import: github.com/meridianhub/meridian/shared/platform/await"

    # List files interactively (capped at 10 to avoid flooding the terminal)
    if [ -t 1 ]; then
        local shown=0
        for f in "${files[@]}"; do
            [ "$shown" -ge 10 ] && break
            echo "       - ${f}"
            shown=$((shown + 1))
        done
        if [ "${#files[@]}" -gt 10 ]; then
            echo "       (showing first 10 of ${count})"
        fi
    fi
}

# ---------------------------------------------------------------------------
# Check 3: doc.go in shared packages
# ---------------------------------------------------------------------------

check_doc_go() {
    log_section "doc.go in Shared Packages"

    local found=0

    # Check every direct child of shared/platform/ and shared/pkg/ that
    # contains at least one .go file (excluding generated files).
    for base in "shared/platform" "shared/pkg"; do
        [ -d "${REPO_ROOT}/${base}" ] || continue

        while IFS= read -r dir; do
            # Does this directory contain any non-generated Go files?
            local go_files
            go_files=$(find "${REPO_ROOT}/${dir}" -maxdepth 1 -name "*.go" \
                ! -name "*.pb.go" ! -name "*.pb.gw.go" 2>/dev/null | wc -l)

            [ "$go_files" -eq 0 ] && continue

            if [ ! -f "${REPO_ROOT}/${dir}/doc.go" ]; then
                log_error "${dir}/ is missing doc.go"
                echo "       Fix: add doc.go with package documentation"
                echo "            // Package $(basename "$dir") ..."
                echo "            package $(basename "$dir")"
                found=1
            fi
        done < <(
            find "${REPO_ROOT}/${base}" -maxdepth 1 -mindepth 1 -type d 2>/dev/null \
                | sed "s|${REPO_ROOT}/||" | sort
        )
    done

    if [ "$found" -eq 0 ]; then
        log_ok "All shared packages have doc.go"
    fi
}

# ---------------------------------------------------------------------------
# Check 4: Proto freshness (generated files match .proto sources)
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
        log_warn "buf generate failed — cannot check proto freshness"
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
# Check 5: service/server.go naming convention
# ---------------------------------------------------------------------------

check_server_naming() {
    log_section "service/server.go Naming Convention"

    local found=0

    while IFS= read -r service_dir; do
        local svc
        svc=$(basename "$(dirname "$service_dir")")

        # Skip if service filter is active
        if [ -n "$SERVICE_FILTER" ] && [ "$SERVICE_FILTER" != "$svc" ]; then
            continue
        fi

        # Does the service/ directory have any gRPC server Go files?
        local go_files
        go_files=$(find "${REPO_ROOT}/${service_dir}" -maxdepth 1 -name "*.go" \
            ! -name "*_test.go" ! -name "doc.go" 2>/dev/null | wc -l)

        [ "$go_files" -eq 0 ] && continue

        if [ ! -f "${REPO_ROOT}/${service_dir}/server.go" ]; then
            log_warn "${service_dir}/ has no server.go"
            echo "       Convention: the main gRPC service file should be named server.go"
            echo "       Found: $(find "${REPO_ROOT}/${service_dir}" -maxdepth 1 -name "*.go" \
                ! -name "*_test.go" ! -name "doc.go" 2>/dev/null | sed "s|${REPO_ROOT}/||" | xargs)"
            found=1
        fi
    done < <(
        cd "$REPO_ROOT"
        find services -type d -name "service" 2>/dev/null \
            | sed "s|${REPO_ROOT}/||" | sort
    )

    if [ "$found" -eq 0 ]; then
        log_ok "All service packages have server.go"
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
check_doc_go
check_proto_freshness
check_server_naming

# Summary
echo ""
echo -e "${BOLD}── Summary ──${NC}"

if [ "$ERRORS" -gt 0 ]; then
    echo -e "${RED}${ERRORS} error(s) detected — CI will fail${NC}"
fi
if [ "$WARNINGS" -gt 0 ]; then
    echo -e "${YELLOW}${WARNINGS} warning(s) — fix encouraged but not required${NC}"
fi
if [ "$ERRORS" -eq 0 ] && [ "$WARNINGS" -eq 0 ]; then
    echo -e "${GREEN}All convention checks passed${NC}"
elif [ "$ERRORS" -eq 0 ]; then
    echo -e "${GREEN}No errors (${WARNINGS} warning(s))${NC}"
fi

if [ "$ERRORS" -gt 0 ]; then
    exit 1
fi
exit 0
