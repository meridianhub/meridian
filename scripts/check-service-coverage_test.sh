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
}

# Source only the classifier, not the whole script, which runs the test suite.
# shellcheck disable=SC1090  # the sourced text is extracted from $TARGET at runtime
source <(sed -n '/^integration_only_packages()/,/^}/p' "$TARGET")

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
echo "Integration-only package classification"
echo ""

mkdir -p "$TEST_DIR/services/gated/adapters/persistence"
cat > "$TEST_DIR/services/gated/adapters/persistence/main_test.go" <<'EOF'
package persistence_test

func TestMain(m *testing.M) {
	flag.Parse()
	if os.Getenv("INTEGRATION_TEST") == "" && testing.Short() {
		os.Exit(m.Run())
	}
	startContainer()
}
EOF

mkdir -p "$TEST_DIR/services/pertest/app"
cat > "$TEST_DIR/services/pertest/app/container_test.go" <<'EOF'
package app_test

func TestContainer(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("INTEGRATION_TESTS=1 not set, skipping integration test")
	}
}
EOF
cat > "$TEST_DIR/services/pertest/app/config_test.go" <<'EOF'
package app_test

func TestConfig(t *testing.T) {}
EOF

mkdir -p "$TEST_DIR/services/plain/domain"
cat > "$TEST_DIR/services/plain/domain/domain_test.go" <<'EOF'
package domain_test

func TestThing(t *testing.T) {}
EOF

classified="$(integration_only_packages "$TEST_DIR" | tr '\n' ' ' | sed 's/ $//')"
assert_equals "services/gated/adapters/persistence" "$classified" \
    "only a package whose TestMain short-circuits under -short is excluded"

case " ${classified} " in
    *"services/pertest/app"*) per_test="excluded" ;;
    *) per_test="kept" ;;
esac
assert_equals "kept" "$per_test" "a package that gates individual tests with t.Skip is kept"

echo ""
echo "Results: ${TESTS_PASSED} passed, ${TESTS_FAILED} failed"

if [ "${TESTS_FAILED}" -gt 0 ]; then
    exit 1
fi
