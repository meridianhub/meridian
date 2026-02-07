#!/usr/bin/env bash
# lint-bucket-id.sh - Detect manual bucket_id string construction
#
# All services MUST use shared/pkg/bucketing.CalculateBucketID() to compute
# bucket IDs. This script detects manual string construction patterns that
# bypass the canonical library and could cause Bucket Drift.
#
# Usage:
#   ./scripts/lint-bucket-id.sh
#
# Exit codes:
#   0 - No violations found
#   1 - Manual bucket_id construction detected

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Patterns that indicate manual bucket_id string construction.
# These match fmt.Sprintf or string concatenation that builds dimension_instrument_attr patterns.
#
# Excluded paths:
#   - shared/pkg/bucketing/ (the canonical library itself)
#   - *_test.go (test files may construct bucket IDs for assertions)
#   - *.pb.go (generated protobuf files)
#   - vendor/ (third-party code)
VIOLATIONS=0

# Pattern 1: fmt.Sprintf with bucket-like format strings containing dimension prefixes
# e.g., fmt.Sprintf("currency_%s", code) or fmt.Sprintf("energy_%s_%s=%s", ...)
# Only matches when the dimension word is at the start of a string literal (after opening quote)
while IFS= read -r match; do
    if [ -n "$match" ]; then
        echo "VIOLATION: Manual bucket_id construction detected:"
        echo "  $match"
        echo "  Use bucketing.CalculateBucketID() from shared/pkg/bucketing instead."
        echo ""
        VIOLATIONS=$((VIOLATIONS + 1))
    fi
done < <(grep -rn \
    --include='*.go' \
    --exclude-dir='vendor' \
    --exclude-dir='.git' \
    --exclude='*_test.go' \
    --exclude='*.pb.go' \
    -E 'fmt\.Sprintf\(\s*"(monetary|commodity|currency|energy|compute|carbon|data|volume|mass|count)_' \
    "$REPO_ROOT" | grep -v 'shared/pkg/bucketing/' || true)

# Pattern 2: String concatenation building bucket ID patterns
# e.g., dimension + "_" + instrumentCode + "_" + key + "=" + value
while IFS= read -r match; do
    if [ -n "$match" ]; then
        echo "VIOLATION: Manual bucket_id string concatenation detected:"
        echo "  $match"
        echo "  Use bucketing.CalculateBucketID() from shared/pkg/bucketing instead."
        echo ""
        VIOLATIONS=$((VIOLATIONS + 1))
    fi
done < <(grep -rn \
    --include='*.go' \
    --exclude-dir='vendor' \
    --exclude-dir='.git' \
    --exclude='*_test.go' \
    --exclude='*.pb.go' \
    -E 'strings\.Join\(.*"_"\).*bucket' \
    "$REPO_ROOT" | grep -v 'shared/pkg/bucketing/' || true)

# Pattern 3: Direct assignment of string-literal bucket IDs with dimension prefixes
while IFS= read -r match; do
    if [ -n "$match" ]; then
        echo "VIOLATION: Hardcoded bucket_id literal detected:"
        echo "  $match"
        echo "  Use bucketing.CalculateBucketID() from shared/pkg/bucketing instead."
        echo ""
        VIOLATIONS=$((VIOLATIONS + 1))
    fi
done < <(grep -rn \
    --include='*.go' \
    --exclude-dir='vendor' \
    --exclude-dir='.git' \
    --exclude='*_test.go' \
    --exclude='*.pb.go' \
    -E '[Bb]ucket[_]?[Ii][Dd].*[:=].*"(currency|energy|compute|carbon|data|volume|mass|count)_' \
    "$REPO_ROOT" | grep -v 'shared/pkg/bucketing/' || true)

if [ "$VIOLATIONS" -gt 0 ]; then
    echo "Found $VIOLATIONS bucket_id construction violation(s)."
    echo "All bucket IDs must be computed using shared/pkg/bucketing.CalculateBucketID()"
    exit 1
fi

echo "No manual bucket_id construction detected."
exit 0
