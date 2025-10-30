#!/bin/bash
#
# validate-tiltfile.sh - Validate Tiltfile registry configuration
#
# This script checks that default_registry() is called with a valid
# host:port format (e.g., localhost:5000) instead of just a container
# name (e.g., ctlptl-registry).
#
# Usage: ./scripts/validate-tiltfile.sh [path-to-Tiltfile]
#

set -e

TILTFILE="${1:-Tiltfile}"

if [[ ! -f "$TILTFILE" ]]; then
    echo "Error: Tiltfile not found at $TILTFILE" >&2
    exit 1
fi

echo "Validating Tiltfile configuration..."

# Extract default_registry() calls
registry_calls=$(grep -o 'default_registry([^)]*)' "$TILTFILE" || true)

if [[ -z "$registry_calls" ]]; then
    echo "✓ No default_registry() calls found (OK)"
    exit 0
fi

errors=0

while IFS= read -r call; do
    # Extract the argument (variable name or literal value)
    arg=$(echo "$call" | sed -E "s/default_registry\(([^)]+)\)/\1/" | tr -d "\"'" | xargs)

    # Check if it looks like a variable reference (doesn't match host:port pattern)
    if [[ ! "$arg" =~ ^[a-zA-Z0-9\.\-]+:[0-9]+$ ]]; then
        # Try to find the variable assignment
        if grep -q "${arg}.*=.*os\.getenv" "$TILTFILE"; then
            # Extract default value from os.getenv('VAR', 'default')
            default_value=$(grep "${arg}.*=.*os\.getenv" "$TILTFILE" | sed -E "s/.*os\.getenv\([^,]+, *['\"]([^'\"]+)['\"].*/\1/")
            echo "Found variable $arg = os.getenv(..., '$default_value')"

            # Check if default value is in host:port format
            if [[ "$default_value" =~ ^[a-zA-Z0-9\.\-]+:[0-9]+$ ]]; then
                echo "✓ Registry configuration valid: $arg -> $default_value"
            else
                echo "✗ Invalid registry format: $arg = '$default_value' (must be host:port)" >&2
                errors=$((errors + 1))
            fi
        elif grep -q "${arg}.*=" "$TILTFILE"; then
            # Simple variable assignment
            value=$(grep "${arg}.*=" "$TILTFILE" | head -1 | sed -E "s/.*= *['\"]([^'\"]+)['\"].*/\1/")
            echo "Found variable $arg = '$value'"

            # Check if value is in host:port format
            if [[ "$value" =~ ^[a-zA-Z0-9\.\-]+:[0-9]+$ ]]; then
                echo "✓ Registry configuration valid: $arg -> $value"
            else
                echo "✗ Invalid registry format: $arg = '$value' (must be host:port)" >&2
                errors=$((errors + 1))
            fi
        else
            echo "✗ Variable '$arg' not found or uses invalid format (must be host:port)" >&2
            errors=$((errors + 1))
        fi
    else
        # It's a literal value, validate directly
        echo "✓ Registry configuration valid: $arg"
    fi
done <<< "$registry_calls"

if [[ $errors -gt 0 ]]; then
    echo ""
    echo "✗ Registry configuration errors found: $errors"
    exit 1
fi

echo ""
echo "✓ Tiltfile validation complete"
exit 0
