#!/usr/bin/env bash

# Basic automated tests for doctor.sh
# Tests critical functionality: git hooks validation, install commands, security model

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_DIR=$(mktemp -d)
TESTS_PASSED=0
TESTS_FAILED=0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

cleanup() {
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

# Test helper functions
assert_equals() {
    local expected=$1
    local actual=$2
    local test_name=$3

    if [ "$expected" = "$actual" ]; then
        echo -e "${GREEN}✓${NC} $test_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "${RED}✗${NC} $test_name"
        echo "  Expected: $expected"
        echo "  Actual:   $actual"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_contains() {
    local haystack=$1
    local needle=$2
    local test_name=$3

    # Use grep -F for literal string matching, with -- to prevent option interpretation
    if echo "$haystack" | grep -qF -- "$needle"; then
        echo -e "${GREEN}✓${NC} $test_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "${RED}✗${NC} $test_name"
        echo "  Expected to contain: $needle"
        echo "  Actual output: $haystack"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_exit_code() {
    local expected=$1
    local actual=$2
    local test_name=$3

    if [ "$expected" -eq "$actual" ]; then
        echo -e "${GREEN}✓${NC} $test_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "${RED}✗${NC} $test_name"
        echo "  Expected exit code: $expected"
        echo "  Actual exit code:   $actual"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

echo "Running doctor.sh automated tests..."
echo "===================================="
echo ""

# Test 1: Script exists and is executable
echo "Test Suite: Basic Script Properties"
echo "------------------------------------"
if [ -f "$SCRIPT_DIR/doctor.sh" ] && [ -x "$SCRIPT_DIR/doctor.sh" ]; then
    echo -e "${GREEN}✓${NC} doctor.sh exists and is executable"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} doctor.sh exists and is executable"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 2: Script shows help with --help flag
echo "Test Suite: Command Line Arguments"
echo "-----------------------------------"
help_output=$("$SCRIPT_DIR/doctor.sh" --help 2>&1 || true)
assert_contains "$help_output" "Usage:" "Shows help message with --help"
assert_contains "$help_output" "--check" "Help includes --check option"
assert_contains "$help_output" "--fix" "Help includes --fix option"
assert_contains "$help_output" "--verbose" "Help includes --verbose option"

# Behaviour: --help exits 0 (observable contract, not source structure)
"$SCRIPT_DIR/doctor.sh" --help >/dev/null 2>&1
assert_exit_code 0 $? "Exits 0 after printing help"

# Behaviour: an unknown option is rejected at runtime with a non-zero exit
# and a diagnostic message. This exercises the actual argument parser rather
# than grepping the source for the case statement.
unknown_exit=0
unknown_output=$("$SCRIPT_DIR/doctor.sh" --not-a-real-flag 2>&1) || unknown_exit=$?
assert_exit_code 1 "$unknown_exit" "Exits 1 on unknown option"
assert_contains "$unknown_output" "Unknown option" "Reports the unknown option to the user"
echo ""

# Test 3: PKG_MANAGER validation (security test)
echo "Test Suite: Security Model"
echo "--------------------------"
# Extract and test the get_install_cmd function logic
# We'll test this by checking the script's actual commands
if grep -q 'case "$PKG_MANAGER" in' "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} PKG_MANAGER validation exists"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} PKG_MANAGER validation exists"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Check that dangerous patterns are not present
if grep -q 'eval.*install_cmd' "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${RED}✗${NC} Script does not use eval with install commands"
    TESTS_FAILED=$((TESTS_FAILED + 1))
else
    echo -e "${GREEN}✓${NC} Script does not use eval with install commands"
    TESTS_PASSED=$((TESTS_PASSED + 1))
fi

# Check security documentation exists
if grep -q "Security model:" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Security model documentation present"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Security model documentation present"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 4: Git hooks validation logic
echo "Test Suite: Git Hooks Validation"
echo "---------------------------------"

# Check that doctor.sh has git hooks validation function
if grep -qF -- "check_git_hooks" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Git hooks validation function exists"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Git hooks validation function exists"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Create a mock git repository to test cmp logic
mkdir -p "$TEST_DIR/test-repo/.git/hooks"
mkdir -p "$TEST_DIR/test-repo/.githooks"

# Create a source hook
echo "#!/bin/bash" > "$TEST_DIR/test-repo/.githooks/pre-commit"
echo "echo 'test hook v1'" >> "$TEST_DIR/test-repo/.githooks/pre-commit"
chmod +x "$TEST_DIR/test-repo/.githooks/pre-commit"

# Test the cmp command logic used by doctor.sh
# Test case: Out of sync hook detection
cp "$TEST_DIR/test-repo/.githooks/pre-commit" "$TEST_DIR/test-repo/.git/hooks/pre-commit"
echo "# modified" >> "$TEST_DIR/test-repo/.githooks/pre-commit"

# The cmp command (as used in doctor.sh) should detect the difference
if ! cmp -s "$TEST_DIR/test-repo/.githooks/pre-commit" "$TEST_DIR/test-repo/.git/hooks/pre-commit"; then
    echo -e "${GREEN}✓${NC} cmp correctly detects out-of-sync hooks"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} cmp correctly detects out-of-sync hooks"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 5: Install command generation
echo "Test Suite: Install Command Generation"
echo "---------------------------------------"

# Check that get_install_cmd function exists and has proper structure
if grep -q "get_install_cmd()" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} get_install_cmd function exists"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} get_install_cmd function exists"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Check for platform-specific commands
if grep -q "macos-go.*brew install go" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} macOS Go install command present"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} macOS Go install command present"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if grep -q "linux-go.*golang-go" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Linux Go install command present"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Linux Go install command present"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Check that PKG_MANAGER is actually used in commands
if grep -q '\$PKG_MANAGER' "$SCRIPT_DIR/doctor.sh" && grep -q "get_install_cmd" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} PKG_MANAGER variable is utilized"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} PKG_MANAGER variable is utilized"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 6: Network detection logic
echo "Test Suite: Network Detection"
echo "------------------------------"

# Check that network detection uses curl with --fail
if grep -q "curl.*--fail" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Network detection uses curl with --fail flag"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Network detection uses curl with --fail flag"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Check for timeout portability handling
if grep -q "gtimeout" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Handles GNU timeout (gtimeout) for macOS"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Handles GNU timeout (gtimeout) for macOS"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 7: Docker daemon check
echo "Test Suite: Docker Validation"
echo "------------------------------"

# Check that script validates docker daemon, not just docker binary
if grep -q "docker info" "$SCRIPT_DIR/doctor.sh" || grep -q "check_docker_daemon" "$SCRIPT_DIR/doctor.sh"; then
    echo -e "${GREEN}✓${NC} Validates Docker daemon is running"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}✗${NC} Validates Docker daemon is running"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Summary
echo "===================================="
echo "Test Results Summary"
echo "===================================="
echo -e "${GREEN}Passed:${NC} $TESTS_PASSED"
echo -e "${RED}Failed:${NC} $TESTS_FAILED"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some tests failed${NC}"
    exit 1
fi
