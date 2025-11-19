#!/usr/bin/env bash
#
# test-mermaid-generation.sh - Test script for Mermaid diagram generation
#
# This script validates that the generate-coupling-mermaid.sh script works correctly
# with various input scenarios.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Testing Mermaid generation script..."
echo ""

# Test 1: Piped input
echo "Test 1: Piped input from analyze-coupling.sh"
if "${SCRIPT_DIR}/analyze-coupling.sh" 2>/dev/null | "${SCRIPT_DIR}/generate-coupling-mermaid.sh" > /dev/null; then
    echo "  ✓ Piped input works"
else
    echo "  ✗ Piped input failed"
    exit 1
fi

# Test 2: Verify Mermaid syntax is valid
echo "Test 2: Verify Mermaid syntax"
output=$("${SCRIPT_DIR}/analyze-coupling.sh" 2>/dev/null | "${SCRIPT_DIR}/generate-coupling-mermaid.sh")

if echo "${output}" | grep -q '```mermaid'; then
    echo "  ✓ Mermaid code block present"
else
    echo "  ✗ Missing Mermaid code block"
    exit 1
fi

if echo "${output}" | grep -q 'graph TD'; then
    echo "  ✓ Graph definition present"
else
    echo "  ✗ Missing graph definition"
    exit 1
fi

if echo "${output}" | grep -q 'classDef violation'; then
    echo "  ✓ Style definitions present"
else
    echo "  ✗ Missing style definitions"
    exit 1
fi

# Test 3: Verify service nodes
echo "Test 3: Verify service nodes"
if echo "${output}" | grep -q 'CA\[Current Account\]'; then
    echo "  ✓ Current Account node present"
else
    echo "  ✗ Current Account node missing"
    exit 1
fi

if echo "${output}" | grep -q 'FA\[Financial Accounting\]'; then
    echo "  ✓ Financial Accounting node present"
else
    echo "  ✗ Financial Accounting node missing"
    exit 1
fi

if echo "${output}" | grep -q 'PK\[Position Keeping\]'; then
    echo "  ✓ Position Keeping node present"
else
    echo "  ✗ Position Keeping node missing"
    exit 1
fi

if echo "${output}" | grep -q 'PLAT\[Platform\]'; then
    echo "  ✓ Platform node present"
else
    echo "  ✗ Platform node missing"
    exit 1
fi

# Test 4: Verify edges exist
echo "Test 4: Verify dependency edges"
if echo "${output}" | grep -q '-->|proto only|'; then
    echo "  ✓ Proto dependency edges present"
else
    echo "  ✗ Proto dependency edges missing"
    exit 1
fi

if echo "${output}" | grep -q '-.->.*PLAT'; then
    echo "  ✓ Platform coupling edges present"
else
    echo "  ✗ Platform coupling edges missing"
    exit 1
fi

# Test 5: Verify summary
echo "Test 5: Verify HTML comment summary"
if echo "${output}" | grep -q '<!-- Coupling Analysis Summary'; then
    echo "  ✓ Summary comment present"
else
    echo "  ✗ Summary comment missing"
    exit 1
fi

if echo "${output}" | grep -q 'Services analyzed:'; then
    echo "  ✓ Service count present"
else
    echo "  ✗ Service count missing"
    exit 1
fi

echo ""
echo "All tests passed!"
echo ""
echo "Sample output:"
echo "----------------------------------------"
"${SCRIPT_DIR}/analyze-coupling.sh" 2>/dev/null | "${SCRIPT_DIR}/generate-coupling-mermaid.sh" | head -40
echo "----------------------------------------"
