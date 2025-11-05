#!/usr/bin/env bash
#
# Reset Local Database Migrations
#
# This script clears migration state from the local CockroachDB instance
# to resolve "out of order" migration errors during development.
#
# DANGER: This drops ALL data in the specified schemas. Only use locally!
#

set -euo pipefail

# Configuration
DB_URL="${DATABASE_URL:-postgres://root@localhost:26257/defaultdb?sslmode=disable}"
SCHEMAS=("position_keeping" "current_account")

echo "⚠️  WARNING: This will DROP and recreate schemas, losing ALL local data!"
echo ""
echo "Database: $DB_URL"
echo "Schemas: ${SCHEMAS[*]}"
echo ""
read -p "Are you sure you want to continue? (yes/no): " -r
echo

if [[ ! $REPLY =~ ^yes$ ]]; then
    echo "Aborted."
    exit 1
fi

# Drop and recreate each schema
for schema in "${SCHEMAS[@]}"; do
    echo "Resetting schema: $schema"

    # Drop schema cascade (removes all tables, views, etc.)
    psql "$DB_URL" -c "DROP SCHEMA IF EXISTS ${schema} CASCADE;" || true
    psql "$DB_URL" -c "DROP SCHEMA IF EXISTS ${schema}_audit CASCADE;" || true

    # Drop Atlas migration tracking table for this schema
    psql "$DB_URL" -c "DROP TABLE IF EXISTS atlas_schema_revisions CASCADE;" || true

    echo "✓ Schema $schema reset"
done

echo ""
echo "✅ Migration state cleared. Rerun 'tilt up' or trigger migrations manually."
echo ""
echo "To apply migrations:"
echo "  - tilt trigger migrate-current-account"
echo "  - tilt trigger migrate-position-keeping"
