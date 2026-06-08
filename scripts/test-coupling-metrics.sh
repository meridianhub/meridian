#!/usr/bin/env bash
# Test script to validate coupling metrics calculations.
#
# Assertions are invariant-based and dynamic so the test does not go stale every
# time docs/architecture/coupling-metrics.json is regenerated. It validates the
# structure and the mathematical relationships between fields rather than
# hard-coding a fixed service list or per-service values.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
METRICS_FILE="${REPO_ROOT}/docs/architecture/coupling-metrics.json"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

echo "Testing coupling metrics calculations..."
echo

TESTS_PASSED=0
TESTS_FAILED=0

pass() {
    echo -e "${GREEN}✓${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail() {
    echo -e "${RED}✗${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

# Valid assessment values produced by calculate-coupling-metrics.sh
VALID_ASSESSMENTS="stable acceptable too-dependent too-rigid"

# Test Suite 1: JSON structure
echo "Test Suite 1: JSON Structure"
if jq -e '.timestamp' "$METRICS_FILE" > /dev/null 2>&1; then
    pass "JSON has timestamp field"
else
    fail "JSON missing timestamp field"
fi

if jq -e '.services' "$METRICS_FILE" > /dev/null 2>&1; then
    pass "JSON has services object"
else
    fail "JSON missing services object"
fi
echo

# Test Suite 2: Service coverage matches the services/ tree
echo "Test Suite 2: Service Coverage"
EXPECTED_COUNT=$(find "${REPO_ROOT}/services" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')
ACTUAL_COUNT=$(jq '.services | length' "$METRICS_FILE")
if [ "$ACTUAL_COUNT" = "$EXPECTED_COUNT" ]; then
    pass "JSON covers all $EXPECTED_COUNT services under services/"
else
    fail "JSON covers $ACTUAL_COUNT services (expected $EXPECTED_COUNT from services/)"
fi
echo

# Test Suite 3: Per-service field presence and value invariants
echo "Test Suite 3: Per-Service Invariants"
SERVICES=$(jq -r '.services | keys[]' "$METRICS_FILE")
for service in $SERVICES; do
    entry=$(jq -c ".services.\"$service\"" "$METRICS_FILE")

    # All required fields present
    if echo "$entry" | jq -e 'has("afferent_coupling") and has("efferent_coupling") and has("instability") and has("assessment")' > /dev/null; then
        :
    else
        fail "$service missing required metric field(s)"
        continue
    fi

    ca=$(echo "$entry" | jq -r '.afferent_coupling')
    ce=$(echo "$entry" | jq -r '.efferent_coupling')
    inst=$(echo "$entry" | jq -r '.instability')
    assessment=$(echo "$entry" | jq -r '.assessment')

    # Instability must be within [0, 1]
    range_ok=$(echo "$inst >= 0 && $inst <= 1" | bc -l)
    if [ "$range_ok" != "1" ]; then
        fail "$service instability out of range [0,1]: $inst"
        continue
    fi

    # Instability invariant: I = Ce / (Ca + Ce), or 0 when isolated.
    # Generator rounds to 2 decimals, so allow a small tolerance.
    if [ $((ca + ce)) -eq 0 ]; then
        invariant_ok=$(echo "$inst == 0" | bc -l)
    else
        invariant_ok=$(awk -v i="$inst" -v ce="$ce" -v tot="$((ca + ce))" \
            'BEGIN { d = i - (ce / tot); if (d < 0) d = -d; print (d < 0.02) ? 1 : 0 }')
    fi
    if [ "$invariant_ok" != "1" ]; then
        fail "$service instability $inst != Ce/(Ca+Ce) = $ce/$((ca + ce))"
        continue
    fi

    # Assessment must be one of the known values
    if [[ " ${VALID_ASSESSMENTS} " == *" ${assessment} "* ]]; then
        pass "$service: Ca=$ca Ce=$ce I=$inst ($assessment)"
    else
        fail "$service has unknown assessment: $assessment"
    fi
done
echo

# Test Suite 4: Architectural expectations (dynamic)
echo "Test Suite 4: Architectural Expectations"
ORCHESTRATORS=$(jq '[.services[] | select(.efferent_coupling > 0)] | length' "$METRICS_FILE")
PROVIDERS=$(jq '[.services[] | select(.afferent_coupling > 0)] | length' "$METRICS_FILE")
if [ "$ORCHESTRATORS" -gt 0 ]; then
    pass "At least one orchestrator service exists (Ce > 0): $ORCHESTRATORS"
else
    fail "No orchestrator services found (expected at least one with Ce > 0)"
fi
if [ "$PROVIDERS" -gt 0 ]; then
    pass "At least one provider service exists (Ca > 0): $PROVIDERS"
else
    fail "No provider services found (expected at least one with Ca > 0)"
fi
echo

# Summary
echo "========================================"
echo "Test Results:"
echo -e "  ${GREEN}Passed:${NC} $TESTS_PASSED"
echo -e "  ${RED}Failed:${NC} $TESTS_FAILED"
echo "========================================"
echo

if [ "$TESTS_FAILED" -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
