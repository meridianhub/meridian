#!/usr/bin/env bash

# Tests for check-service-coverage.sh service detection and package classification.
#
# Both behaviours under test used to fail silently: a service that was skipped
# reported SKIP rather than an error, and an over-broad package exclusion dropped
# covered code out of the gate without saying so.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${SCRIPT_DIR}/check-service-coverage.sh"
TEST_DIR=$(mktemp -d)
TESTS_PASSED=0
TESTS_FAILED=0

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

cleanup() {
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

assert_equals() {
    local expected=$1
    local actual=$2
    local test_name=$3

    if [ "$expected" = "$actual" ]; then
        echo -e "${GREEN}✓${NC} $test_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}✗${NC} $test_name"
        echo "  Expected: $expected"
        echo "  Actual:   $actual"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
    return 0
}

# Source only the helpers under test, not the whole script, which runs the suite.
# shellcheck disable=SC1090  # the sourced text is extracted from $TARGET at runtime
source <(sed -n '/^skip_only_packages()/,/^}/p;/^package_to_path()/,/^}/p;/^service_floor()/,/^}/p' "$TARGET")

REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
THRESHOLD=80

echo ""
echo "Service detection (the guards that used to race on SIGPIPE)"
echo ""

mkdir -p "$TEST_DIR/services/many/pkg"
for i in $(seq 1 200); do
    echo "package pkg" > "$TEST_DIR/services/many/pkg/file${i}.go"
done
echo "package pkg" > "$TEST_DIR/services/many/pkg/pkg_test.go"
mkdir -p "$TEST_DIR/services/empty/docs"
echo "# no go here" > "$TEST_DIR/services/empty/docs/README.md"

# Under `set -o pipefail`, `find | grep -q` returns 141 once grep exits early,
# so a service with sources reported as having none. Run it repeatedly: the old
# form failed intermittently, so a single pass could pass by luck.
detected=0
for _ in $(seq 1 25); do
    if [ -n "$(find "$TEST_DIR/services/many" -maxdepth 5 -name "*.go" -not -name "*_test.go" -print -quit)" ]; then
        detected=$((detected + 1))
    fi
done
assert_equals "25" "$detected" "a service with Go sources is detected on every run"

if [ -z "$(find "$TEST_DIR/services/empty" -maxdepth 5 -name "*.go" -not -name "*_test.go" -print -quit)" ]; then
    result="none"
else
    result="found"
fi
assert_equals "none" "$result" "a service with no Go sources reports none"

echo ""
echo "Skip-only package classification (measured from go test -json)"
echo ""

M="github.com/meridianhub/meridian"
events="$TEST_DIR/events.json"
{
    # Every test skipped: the package contributes no covered statements.
    printf '{"Action":"skip","Package":"%s/services/a/gated","Test":"TestOne"}\n' "$M"
    printf '{"Action":"skip","Package":"%s/services/a/gated","Test":"TestTwo"}\n' "$M"
    # Some skip, some run: its covered code belongs in the gate.
    printf '{"Action":"skip","Package":"%s/services/a/mixed","Test":"TestGated"}\n' "$M"
    printf '{"Action":"pass","Package":"%s/services/a/mixed","Test":"TestUnit"}\n' "$M"
    # Ordinary package.
    printf '{"Action":"pass","Package":"%s/services/a/plain","Test":"TestUnit"}\n' "$M"
    # Package-level events carry no .Test and must not be classified.
    printf '{"Action":"pass","Package":"%s/services/a/plain"}\n' "$M"
    # Output events are noise and must be ignored by the classifier.
    printf '{"Action":"output","Package":"%s/services/a/gated","Test":"TestOne","Output":"skip"}\n' "$M"
} > "$events"

classified="$(skip_only_packages "$events" | package_to_path | tr '\n' ' ' | sed 's/ $//')"
assert_equals "services/a/gated" "$classified" \
    "only a package where every test skipped is excluded"

case " ${classified} " in
    *"services/a/mixed"*) mixed="excluded" ;;
    *) mixed="kept" ;;
esac
assert_equals "kept" "$mixed" "a package where some tests run is kept"

# A failing test must not read as skip-only, or a broken package would vanish
# from the gate rather than failing it.
printf '{"Action":"fail","Package":"%s/services/a/broken","Test":"TestX"}\n' "$M" > "$TEST_DIR/fail.json"
printf '{"Action":"skip","Package":"%s/services/a/broken","Test":"TestY"}\n' "$M" >> "$TEST_DIR/fail.json"
assert_equals "" "$(skip_only_packages "$TEST_DIR/fail.json")" \
    "a package with a failing test is not treated as skip-only"

echo ""
echo "Per-service floors"
echo ""

assert_equals "80" "$(service_floor some-service)" "a service with no entry uses the threshold"
assert_equals "71.2" "$(service_floor position-keeping)" "position-keeping carries its recorded floor"

echo ""
echo "Results: ${TESTS_PASSED} passed, ${TESTS_FAILED} failed"

if [ "${TESTS_FAILED}" -gt 0 ]; then
    exit 1
fi
