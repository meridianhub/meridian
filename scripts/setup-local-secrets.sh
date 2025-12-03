#!/bin/bash
# Setup local development secrets from .example templates
#
# This script generates local secret.yaml files from .example templates
# for use with Kind/Tilt local development.
#
# IMPORTANT: Generated secrets are for LOCAL DEVELOPMENT ONLY
# Production deployments MUST use External Secrets Operator or Sealed Secrets

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Setting up local development secrets..."
echo

# Function to setup secret from example
setup_secret() {
    local example_file="$1"
    local secret_file="${example_file%.example}"

    if [ -f "$secret_file" ]; then
        echo "⚠️  $secret_file already exists, skipping..."
        return
    fi

    echo "Creating $secret_file from template..."

    # For local dev, use the meridian user/database
    # CockroachDB runs in insecure mode (no passwords), so omit password from connection string
    sed 's|<REPLACE_WITH_DATABASE_URL>|postgres://meridian@cockroachdb:26257/meridian?sslmode=disable|g' \
        "$example_file" > "$secret_file"

    echo "✓ Created $secret_file"
}

# Setup main service secret
if [ -f "$PROJECT_ROOT/deployments/k8s/base/secret.yaml.example" ]; then
    setup_secret "$PROJECT_ROOT/deployments/k8s/base/secret.yaml.example"
fi

echo
echo "✓ Local secrets setup complete!"
echo
echo "⚠️  SECURITY REMINDER:"
echo "  - These credentials are for LOCAL DEVELOPMENT ONLY"
echo "  - Never commit secret.yaml files to git (they're in .gitignore)"
echo "  - Production deployments MUST use External Secrets Operator"
echo "  - See docs/secrets-management.md for production setup"
echo
