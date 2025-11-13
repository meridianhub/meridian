#!/bin/bash
# Integration test for database setup
#
# Validates that the database initialization works correctly:
# 1. Database exists
# 2. User exists
# 3. User has correct permissions
# 4. Can connect with the configured credentials

set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
DATABASE_NAME="meridian"
USER_NAME="meridian"
TEST_TABLE="test_permissions_$$"  # Include PID for uniqueness

echo "Testing database setup..."
echo

# Test 1: Verify database exists
echo "Test 1: Checking if database exists..."
DB_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW DATABASES;" 2>&1) || {
  echo "✗ FAILED: kubectl exec failed"
  echo "Output: $DB_OUTPUT"
  exit 1
}

if echo "$DB_OUTPUT" | grep -q "$DATABASE_NAME"; then
  echo "✓ Database '$DATABASE_NAME' exists"
else
  echo "✗ FAILED: Database '$DATABASE_NAME' not found"
  echo "Available databases:"
  echo "$DB_OUTPUT"
  exit 1
fi

# Test 2: Verify user exists
echo "Test 2: Checking if user exists..."
USER_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW USERS;" 2>&1) || {
  echo "✗ FAILED: kubectl exec failed"
  echo "Output: $USER_OUTPUT"
  exit 1
}

if echo "$USER_OUTPUT" | grep -q "$USER_NAME"; then
  echo "✓ User '$USER_NAME' exists"
else
  echo "✗ FAILED: User '$USER_NAME' not found"
  echo "Available users:"
  echo "$USER_OUTPUT"
  exit 1
fi

# Test 3: Verify user can connect to database
echo "Test 3: Testing user connection to database..."
CONNECT_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure --user="$USER_NAME" --database="$DATABASE_NAME" \
  -e "SELECT 1;" 2>&1) || {
  echo "✗ FAILED: User cannot connect to database"
  echo "Output: $CONNECT_OUTPUT"
  exit 1
}

if echo "$CONNECT_OUTPUT" | grep -q "1"; then
  echo "✓ User can connect to database"
else
  echo "✗ FAILED: Unexpected connection output"
  echo "Output: $CONNECT_OUTPUT"
  exit 1
fi

# Test 4: Verify user has CREATE privilege
echo "Test 4: Testing user permissions (CREATE TABLE)..."
CREATE_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure --user="$USER_NAME" --database="$DATABASE_NAME" \
  -e "CREATE TABLE IF NOT EXISTS $TEST_TABLE (id INT); DROP TABLE IF EXISTS $TEST_TABLE;" 2>&1) || {
  echo "✗ FAILED: User lacks CREATE privilege"
  echo "Output: $CREATE_OUTPUT"
  exit 1
}

if echo "$CREATE_OUTPUT" | grep -qE "CREATE TABLE|DROP TABLE"; then
  echo "✓ User has CREATE privilege"
else
  echo "✗ FAILED: Unexpected CREATE/DROP output"
  echo "Output: $CREATE_OUTPUT"
  exit 1
fi

# Test 5: Verify connection string works (simulate app connection)
echo "Test 5: Testing connection string format..."
CONNECTION_STRING="postgres://$USER_NAME@$POD_NAME:26257/$DATABASE_NAME?sslmode=disable"
CONN_STRING_OUTPUT=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --url="$CONNECTION_STRING" \
  -e "SELECT current_database();" 2>&1) || {
  echo "✗ FAILED: Connection string format invalid"
  echo "Output: $CONN_STRING_OUTPUT"
  exit 1
}

if echo "$CONN_STRING_OUTPUT" | grep -q "$DATABASE_NAME"; then
  echo "✓ Connection string format is valid"
else
  echo "✗ FAILED: Unexpected connection string output"
  echo "Output: $CONN_STRING_OUTPUT"
  exit 1
fi

echo
echo "✓ All database setup tests passed!"
echo "  - Database: $DATABASE_NAME"
echo "  - User: $USER_NAME"
echo "  - Permissions: Verified"
echo "  - Connection: Working"
