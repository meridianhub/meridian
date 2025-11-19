#!/usr/bin/env bash
# Test script to validate coupling metrics calculations

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
METRICS_FILE="${REPO_ROOT}/docs/architecture/coupling-metrics.json"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "Testing coupling metrics calculations..."
echo

TESTS_PASSED=0
TESTS_FAILED=0

# Test helper function
test_metric() {
    local service=$1
    local metric=$2
    local expected=$3
    local actual=$(jq -r ".services.\"$service\".$metric" "$METRICS_FILE")

    # Normalize numbers: remove trailing zeros after decimal
    actual_norm=$(echo "$actual" | sed 's/\.0*$//' | sed 's/\([0-9]\)\.\([0-9]*[1-9]\)0*$/\1.\2/')
    expected_norm=$(echo "$expected" | sed 's/\.0*$//' | sed 's/\([0-9]\)\.\([0-9]*[1-9]\)0*$/\1.\2/')

    if [ "$actual_norm" = "$expected_norm" ]; then
        echo -e "${GREEN}✓${NC} $service.$metric = $expected"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}✗${NC} $service.$metric = $actual (expected $expected)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
    return 0
}

# Test 1: Verify current-account has correct efferent coupling
# current-account depends on: position-keeping, financial-accounting
echo "Test Suite 1: Efferent Coupling (Ce)"
test_metric "current-account" "efferent_coupling" "2"
test_metric "position-keeping" "efferent_coupling" "0"
test_metric "financial-accounting" "efferent_coupling" "0"
echo

# Test 2: Verify afferent coupling
# position-keeping is depended on by: current-account (Ca = 1)
# financial-accounting is depended on by: current-account (Ca = 1)
# current-account is depended on by: nobody (Ca = 0)
echo "Test Suite 2: Afferent Coupling (Ca)"
test_metric "position-keeping" "afferent_coupling" "1"
test_metric "financial-accounting" "afferent_coupling" "1"
test_metric "current-account" "afferent_coupling" "0"
echo

# Test 3: Verify instability calculations
# position-keeping: I = 0 / (1 + 0) = 0
# financial-accounting: I = 0 / (1 + 0) = 0
# current-account: I = 2 / (0 + 2) = 1.0
echo "Test Suite 3: Instability (I)"
test_metric "position-keeping" "instability" "0"
test_metric "financial-accounting" "instability" "0"
test_metric "current-account" "instability" "1"
echo

# Test 4: Verify assessments
# position-keeping: I=0 < 0.3, Ca=1 ≤ 3 → stable
# financial-accounting: I=0 < 0.3, Ca=1 ≤ 3 → stable
# current-account: I=1.0 > 0.7, Ce > Ca → too-dependent
echo "Test Suite 4: Assessments"
test_metric "position-keeping" "assessment" "stable"
test_metric "financial-accounting" "assessment" "stable"
test_metric "current-account" "assessment" "too-dependent"
echo

# Test 5: Verify JSON structure
echo "Test Suite 5: JSON Structure"
if jq -e '.timestamp' "$METRICS_FILE" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} JSON has timestamp field"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} JSON missing timestamp field"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if jq -e '.services' "$METRICS_FILE" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} JSON has services object"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} JSON missing services object"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

SERVICE_COUNT=$(jq '.services | length' "$METRICS_FILE")
if [ "$SERVICE_COUNT" = "3" ]; then
    echo -e "${GREEN}✓${NC} JSON contains 3 services"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} JSON contains $SERVICE_COUNT services (expected 3)"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo

# Test 6: Verify instability range (0-1)
echo "Test Suite 6: Value Ranges"
for service in position-keeping current-account financial-accounting; do
    instability=$(jq -r ".services.\"$service\".instability" "$METRICS_FILE")
    # Check if instability is between 0 and 1
    result=$(echo "$instability >= 0 && $instability <= 1" | bc -l)
    if [ "$result" = "1" ]; then
        echo -e "${GREEN}✓${NC} $service instability in valid range [0,1]: $instability"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}✗${NC} $service instability out of range: $instability"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
done
echo

# Test 7: Verify architectural expectations
echo "Test Suite 7: Architectural Expectations"
# current-account should be the orchestrator (depends on others)
CA_CE=$(jq -r '.services."current-account".efferent_coupling' "$METRICS_FILE")
if [ "$CA_CE" -gt 0 ]; then
    echo -e "${GREEN}✓${NC} current-account is an orchestrator (Ce > 0)"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} current-account is not an orchestrator"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# position-keeping and financial-accounting should be providers (depended upon)
PK_CA=$(jq -r '.services."position-keeping".afferent_coupling' "$METRICS_FILE")
FA_CA=$(jq -r '.services."financial-accounting".afferent_coupling' "$METRICS_FILE")
if [ "$PK_CA" -gt 0 ] && [ "$FA_CA" -gt 0 ]; then
    echo -e "${GREEN}✓${NC} position-keeping and financial-accounting are providers (Ca > 0)"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Provider services not correctly identified"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo

# Summary
echo "========================================"
echo "Test Results:"
echo -e "  ${GREEN}Passed:${NC} $TESTS_PASSED"
echo -e "  ${RED}Failed:${NC} $TESTS_FAILED"
echo "========================================"
echo

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
