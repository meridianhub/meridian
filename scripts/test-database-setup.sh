#!/bin/bash
# Integration test for database setup
#
# Validates that the database initialization works correctly:
# 1. Database exists
# 2. User exists
# 3. User has correct permissions
# 4. Can connect with the configured credentials

set -e

NAMESPACE="${NAMESPACE:-default}"
POD_NAME="${POD_NAME:-cockroachdb-0}"
DATABASE_NAME="meridian"
USER_NAME="meridian"

echo "Testing database setup..."
echo

# Test 1: Verify database exists
echo "Test 1: Checking if database exists..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW DATABASES;" 2>/dev/null | grep -q "$DATABASE_NAME"; then
  echo "✓ Database '$DATABASE_NAME' exists"
else
  echo "✗ FAILED: Database '$DATABASE_NAME' not found"
  exit 1
fi

# Test 2: Verify user exists
echo "Test 2: Checking if user exists..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure -e "SHOW USERS;" 2>/dev/null | grep -q "$USER_NAME"; then
  echo "✓ User '$USER_NAME' exists"
else
  echo "✗ FAILED: User '$USER_NAME' not found"
  exit 1
fi

# Test 3: Verify user can connect to database
echo "Test 3: Testing user connection to database..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure --user="$USER_NAME" --database="$DATABASE_NAME" \
  -e "SELECT 1;" 2>/dev/null | grep -q "1"; then
  echo "✓ User can connect to database"
else
  echo "✗ FAILED: User cannot connect to database"
  exit 1
fi

# Test 4: Verify user has CREATE privilege
echo "Test 4: Testing user permissions (CREATE TABLE)..."
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --insecure --user="$USER_NAME" --database="$DATABASE_NAME" \
  -e "CREATE TABLE IF NOT EXISTS test_permissions (id INT); DROP TABLE test_permissions;" 2>/dev/null; then
  echo "✓ User has CREATE privilege"
else
  echo "✗ FAILED: User lacks CREATE privilege"
  exit 1
fi

# Test 5: Verify connection string works (simulate app connection)
echo "Test 5: Testing connection string format..."
CONNECTION_STRING="postgres://$USER_NAME@$POD_NAME:26257/$DATABASE_NAME?sslmode=disable"
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- \
  cockroach sql --url="$CONNECTION_STRING" \
  -e "SELECT current_database();" 2>/dev/null | grep -q "$DATABASE_NAME"; then
  echo "✓ Connection string format is valid"
else
  echo "✗ FAILED: Connection string format invalid"
  exit 1
fi

echo
echo "✓ All database setup tests passed!"
echo "  - Database: $DATABASE_NAME"
echo "  - User: $USER_NAME"
echo "  - Permissions: Verified"
echo "  - Connection: Working"
