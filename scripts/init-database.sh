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

set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
TIMEOUT="${TIMEOUT:-60}"

echo "Initializing CockroachDB database..."

# Fast-fail: Check if pod exists first
if ! kubectl get pod/"$POD_NAME" -n "$NAMESPACE" &>/dev/null; then
  echo "ERROR: Pod $POD_NAME does not exist in namespace $NAMESPACE"
  echo "Available pods:"
  kubectl get pods -n "$NAMESPACE" | grep -E "NAME|cockroach" || echo "  (no cockroachdb pods found)"
  exit 1
fi

# Wait for CockroachDB pod to be ready (reduced timeout for faster feedback)
echo "Waiting for $POD_NAME to be ready (timeout: ${TIMEOUT}s)..."
if ! kubectl wait pod/"$POD_NAME" \
  --for=condition=Ready \
  --timeout="${TIMEOUT}s" \
  --namespace="$NAMESPACE" 2>&1; then
  echo "ERROR: $POD_NAME did not become ready within ${TIMEOUT}s"
  echo "Pod status:"
  kubectl describe pod "$POD_NAME" -n "$NAMESPACE" | grep -A 10 "^Events:" || true
  exit 1
fi

echo "✓ $POD_NAME is ready"

# Create database and user
echo "Creating meridian database and user..."
SQL_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e \
  "CREATE DATABASE IF NOT EXISTS meridian; \
   CREATE USER IF NOT EXISTS meridian; \
   GRANT ALL ON DATABASE meridian TO meridian;" 2>&1) || {
  echo "ERROR: Failed to initialize database"
  echo "SQL output: $SQL_OUTPUT"
  exit 1
}

if echo "$SQL_OUTPUT" | grep -qE "CREATE|GRANT"; then
  echo "✓ Database and user initialized successfully"
elif echo "$SQL_OUTPUT" | grep -qiE "error|failed"; then
  echo "ERROR: Database initialization encountered an error"
  echo "SQL output: $SQL_OUTPUT"
  exit 1
else
  echo "✓ Database and user already exist (idempotent)"
fi

echo "✓ Database initialization complete!"
