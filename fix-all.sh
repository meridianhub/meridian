#!/bin/bash
# Fix all markdown files with errors

set -e

# Get list of files with errors
FILES=$(npm run --silent lint:md 2>&1 | grep "^[^[:space:]].*\.md:" | cut -d: -f1 | sort -u)

echo "Fixing markdown files..."
for file in $FILES; do
    echo "  $file"
    npx --yes markdownlint-cli2-fix "$file" 2>/dev/null || true
done

echo ""
echo "Verifying fixes..."
npm run lint:md
