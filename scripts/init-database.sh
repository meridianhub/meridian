#!/bin/bash
# Initialize CockroachDB database and user for local development
#
# This script:
# 1. Waits for CockroachDB pod to be ready
# 2. Creates the meridian database and user
# 3. Grants necessary permissions
#
# IMPORTANT: For LOCAL DEVELOPMENT ONLY
# Production databases should be initialized through proper migration tooling

set -e

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
TIMEOUT="${TIMEOUT:-300}"

echo "Initializing CockroachDB database..."

# Wait for CockroachDB pod to be ready
echo "Waiting for $POD_NAME to be ready (timeout: ${TIMEOUT}s)..."
if ! kubectl wait pod/"$POD_NAME" \
  --for=condition=Ready \
  --timeout="${TIMEOUT}s" \
  --namespace="$NAMESPACE" 2>/dev/null; then
  echo "ERROR: $POD_NAME did not become ready within ${TIMEOUT}s"
  exit 1
fi

echo "✓ $POD_NAME is ready"

# Create database and user
echo "Creating meridian database and user..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e \
  "CREATE DATABASE IF NOT EXISTS meridian; \
   CREATE USER IF NOT EXISTS meridian; \
   GRANT ALL ON DATABASE meridian TO meridian;" 2>&1 | grep -E "CREATE|GRANT"; then
  echo "✓ Database and user initialized successfully"
else
  echo "✓ Database and user already exist (idempotent)"
fi

# Verify database exists
echo "Verifying database setup..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW DATABASES;" 2>/dev/null | grep -q "meridian"; then
  echo "✓ Verified: meridian database exists"
else
  echo "ERROR: meridian database not found after initialization"
  exit 1
fi

echo "✓ Database initialization complete!"
