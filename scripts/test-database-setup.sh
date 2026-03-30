#!/bin/bash
# Integration test for database-per-service setup
#
# Validates that the database initialization works correctly:
# 1. All service databases exist
# 2. All service users exist
# 3. Users have correct permissions on their own database
# 4. Users CANNOT access other service databases (isolation)

set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
TEST_TABLE="test_permissions_$$"  # Include PID for uniqueness

# Service databases and their users (must match init-database.sh)
DATABASES=(
  "meridian_platform:meridian_platform_user"
  "meridian_control_plane:meridian_control_plane_user"
  "meridian_current_account:meridian_current_account_user"
  "meridian_financial_accounting:meridian_financial_accounting_user"
  "meridian_position_keeping:meridian_position_keeping_user"
  "meridian_payment_order:meridian_payment_order_user"
  "meridian_party:meridian_party_user"
  "meridian_internal_bank_account:meridian_internal_bank_account_user"
)

TESTS_PASSED=0
TESTS_FAILED=0

pass() {
  echo "✓ $1"
  ((TESTS_PASSED++))
}

fail() {
  echo "✗ FAILED: $1"
  ((TESTS_FAILED++))
}

echo "Testing database-per-service setup..."
echo "=============================================="
echo

# Test 1: Verify all databases exist
echo "Test 1: Checking if all databases exist..."
DB_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW DATABASES;" 2>&1) || {
  fail "kubectl exec failed for SHOW DATABASES"
  echo "Output: $DB_OUTPUT"
  exit 1
}

for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  if echo "$DB_OUTPUT" | grep -q "$DB_NAME"; then
    pass "Database '$DB_NAME' exists"
  else
    fail "Database '$DB_NAME' not found"
  fi
done
echo

# Test 2: Verify all users exist
echo "Test 2: Checking if all users exist..."
USER_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW USERS;" 2>&1) || {
  fail "kubectl exec failed for SHOW USERS"
  echo "Output: $USER_OUTPUT"
  exit 1
}

for ENTRY in "${DATABASES[@]}"; do
  USER_NAME="${ENTRY##*:}"
  if echo "$USER_OUTPUT" | grep -q "$USER_NAME"; then
    pass "User '$USER_NAME' exists"
  else
    fail "User '$USER_NAME' not found"
  fi
done
echo

# Test 3: Verify each user can connect to their own database
echo "Test 3: Testing user connections to own database..."
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"

  CONNECT_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
    cockroach sql --insecure --user="$USER_NAME" --database="$DB_NAME" \
    -e "SELECT 1;" 2>&1) || {
    fail "User '$USER_NAME' cannot connect to '$DB_NAME'"
    continue
  }

  if echo "$CONNECT_OUTPUT" | grep -q "1"; then
    pass "User '$USER_NAME' can connect to '$DB_NAME'"
  else
    fail "Unexpected output for '$USER_NAME' on '$DB_NAME'"
  fi
done
echo

# Test 4: Verify each user has CREATE privilege on their database
echo "Test 4: Testing user permissions (CREATE TABLE)..."
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"

  CREATE_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
    cockroach sql --insecure --user="$USER_NAME" --database="$DB_NAME" \
    -e "CREATE TABLE IF NOT EXISTS $TEST_TABLE (id INT); DROP TABLE IF EXISTS $TEST_TABLE;" 2>&1) || {
    fail "User '$USER_NAME' lacks CREATE privilege on '$DB_NAME'"
    continue
  }

  if echo "$CREATE_OUTPUT" | grep -qE "CREATE TABLE|DROP TABLE"; then
    pass "User '$USER_NAME' has CREATE privilege on '$DB_NAME'"
  else
    fail "Unexpected CREATE/DROP output for '$USER_NAME' on '$DB_NAME'"
  fi
done
echo

# Test 5: Verify database isolation (users CANNOT access other databases)
echo "Test 5: Testing database isolation (cross-database access denied)..."
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"

  # Try to access every OTHER database with this user
  for OTHER_ENTRY in "${DATABASES[@]}"; do
    OTHER_DB="${OTHER_ENTRY%%:*}"

    # Skip own database
    if [ "$OTHER_DB" = "$DB_NAME" ]; then
      continue
    fi

    # Attempt to create a table in another database (should fail)
    if ISOLATION_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
      cockroach sql --insecure --user="$USER_NAME" --database="$OTHER_DB" \
      -e "CREATE TABLE IF NOT EXISTS isolation_test_$$ (id INT);" 2>&1); then
      # Command succeeded - check if it was actually a permission error in the output
      if echo "$ISOLATION_OUTPUT" | grep -qiE "permission denied|does not have|insufficient privilege"; then
        pass "User '$USER_NAME' correctly denied access to '$OTHER_DB'"
      else
        fail "User '$USER_NAME' has unauthorized access to '$OTHER_DB'"
        echo "    Output: $ISOLATION_OUTPUT"
      fi
    else
      # Command failed (good - access denied)
      pass "User '$USER_NAME' correctly denied access to '$OTHER_DB'"
    fi
  done
done
echo

# Test 6: Verify connection string format works
echo "Test 6: Testing connection string format..."
for ENTRY in "${DATABASES[@]}"; do
  DB_NAME="${ENTRY%%:*}"
  USER_NAME="${ENTRY##*:}"

  CONNECTION_STRING="postgres://$USER_NAME@$POD_NAME:26257/$DB_NAME?sslmode=disable"
  CONN_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
    cockroach sql --url="$CONNECTION_STRING" \
    -e "SELECT current_database();" 2>&1) || {
    fail "Connection string invalid for '$USER_NAME' on '$DB_NAME'"
    continue
  }

  if echo "$CONN_OUTPUT" | grep -q "$DB_NAME"; then
    pass "Connection string format valid for '$DB_NAME'"
  else
    fail "Unexpected connection string output for '$DB_NAME'"
  fi
done
echo

# Summary
echo "=============================================="
echo "Test Summary:"
echo "  Passed: $TESTS_PASSED"
echo "  Failed: $TESTS_FAILED"
echo

if [ "$TESTS_FAILED" -gt 0 ]; then
  echo "✗ Some tests failed!"
  exit 1
fi

echo "✓ All database-per-service tests passed!"
echo "  - ${#DATABASES[@]} databases with isolated users"
echo "  - Cross-database access correctly denied"
echo "  - All connection strings verified"
