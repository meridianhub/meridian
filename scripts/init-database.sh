#!/bin/bash
# Initialize CockroachDB databases and users for local development
#
# This script:
# 1. Waits for CockroachDB pod to be ready
# 2. Creates service-specific databases with isolated users
# 3. Grants necessary permissions (each user can only access their own database)
#
# IMPORTANT: For LOCAL DEVELOPMENT ONLY
# Production databases should be initialized through proper migration tooling

set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
TIMEOUT="${TIMEOUT:-60}"

# Service databases and their users
# Format: database_name:user_name
DATABASES=(
  "meridian_platform:meridian_platform_user"
  "meridian_control_plane:meridian_control_plane_user"
  "meridian_current_account:meridian_current_account_user"
  "meridian_financial_accounting:meridian_financial_accounting_user"
  "meridian_position_keeping:meridian_position_keeping_user"
  "meridian_payment_order:meridian_payment_order_user"
  "meridian_party:meridian_party_user"
  "meridian_internal_account:meridian_internal_account_user"
  "meridian_market_information:meridian_market_information_user"
  "meridian_reconciliation:meridian_reconciliation_user"
  "meridian_forecasting:meridian_forecasting_user"
  "meridian_reference_data:meridian_reference_data_user"
)

echo "Initializing CockroachDB databases..."

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

# Wait for SQL port to accept connections (pod Ready != SQL ready)
SQL_TIMEOUT="${SQL_TIMEOUT:-$TIMEOUT}"
echo "Waiting for SQL port to accept connections (timeout: ${SQL_TIMEOUT}s)..."
for i in $(seq 1 "$SQL_TIMEOUT"); do
  if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
    cockroach sql --insecure -e "SELECT 1;" &>/dev/null; then
    echo "✓ SQL port is accepting connections"
    break
  fi
  if [ "$i" -eq "$SQL_TIMEOUT" ]; then
    echo "ERROR: SQL port did not become ready within ${SQL_TIMEOUT}s"
    exit 1
  fi
  sleep 1
done

# Create databases and users with restricted access
FAILED=0
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"

  echo "Creating database '$DB_NAME' with user '$USER_NAME'..."
  SQL_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
    cockroach sql --insecure -e \
    "CREATE DATABASE IF NOT EXISTS $DB_NAME; \
     CREATE USER IF NOT EXISTS $USER_NAME; \
     GRANT ALL ON DATABASE $DB_NAME TO $USER_NAME;" 2>&1) || {
    echo "ERROR: Failed to initialize database '$DB_NAME'"
    echo "SQL output: $SQL_OUTPUT"
    FAILED=1
    continue
  }

  if echo "$SQL_OUTPUT" | grep -qiE "error|failed"; then
    echo "ERROR: Database '$DB_NAME' initialization encountered an error"
    echo "SQL output: $SQL_OUTPUT"
    FAILED=1
  else
    echo "✓ Database '$DB_NAME' initialized with user '$USER_NAME'"
  fi
done

if [ "$FAILED" -ne 0 ]; then
  echo "ERROR: One or more database initializations failed"
  exit 1
fi

echo
echo "✓ All databases initialized successfully!"
echo "  Databases created: ${#DATABASES[@]}"
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"
  echo "    - $DB_NAME ($USER_NAME)"
done
