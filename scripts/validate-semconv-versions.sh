#!/bin/bash
# Validate OpenTelemetry semantic convention versions are consistent
#
# This script prevents runtime schema conflicts by ensuring all semconv imports
# use the same version across the codebase.
#
# IMPORTANT: This is a build-time check to catch issues before deployment
# Exit codes:
#   0 - All versions consistent
#   1 - Version mismatch detected

set -euo pipefail

echo "Validating OpenTelemetry semantic convention versions..."

# Find all semconv import versions in Go files
SEMCONV_VERSIONS=$(grep -r "go.opentelemetry.io/otel/semconv/v1\." --include="*.go" . 2>/dev/null | \
  grep -oE "semconv/v1\.[0-9]+" | \
  sort -u) || SEMCONV_VERSIONS=""

# Count unique versions
VERSION_COUNT=$(echo "$SEMCONV_VERSIONS" | grep -v "^$" | wc -l | tr -d ' ')

if [ "$VERSION_COUNT" -eq 0 ]; then
  echo "✓ No semconv imports found (tracing may be disabled)"
  exit 0
fi

if [ "$VERSION_COUNT" -eq 1 ]; then
  VERSION=$(echo "$SEMCONV_VERSIONS" | head -1)
  echo "✓ All semconv imports use consistent version: $VERSION"

  # List files using this version for transparency
  echo ""
  echo "Files using $VERSION:"
  grep -rF "go.opentelemetry.io/otel/$VERSION" --include="*.go" . 2>/dev/null | \
    cut -d: -f1 | \
    sort -u | \
    sed 's/^/  - /'

  exit 0
fi

# Multiple versions detected - this is an error
echo "✗ ERROR: Multiple semconv versions detected"
echo ""
echo "Found versions:"
echo "$SEMCONV_VERSIONS" | sed 's/^/  - /'
echo ""
echo "This causes runtime errors:"
echo "  'conflicting Schema URL: https://opentelemetry.io/schemas/...'"
echo ""
echo "Files by version:"
for version in $SEMCONV_VERSIONS; do
  echo ""
  echo "  $version:"
  grep -rF "go.opentelemetry.io/otel/$version" --include="*.go" . 2>/dev/null | \
    cut -d: -f1 | \
    sort -u | \
    sed 's/^/    - /'
done
echo ""
echo "ACTION REQUIRED:"
echo "  1. Check OpenTelemetry SDK version in go.mod"
echo "  2. Update all semconv imports to match (typically v1.37.0 for SDK v1.38.0)"
echo "  3. Run 'go mod tidy' to update dependencies"
echo "  4. Verify with 'make test'"
echo ""

exit 1
