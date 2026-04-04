#!/usr/bin/env bash
# backfill-tenant-reference-data.sh
#
# Backfills reference data (instruments, saga scripts) into existing tenant schemas.
# This is needed after the tenant isolation migration that removes public from search_path.
#
# Previously, tenant saga_definition rows used platform_ref pointing to
# public.platform_saga_definition with NULL scripts. This script copies the actual
# script content from the platform table into each tenant's saga_definition.
#
# Usage:
#   ./scripts/backfill-tenant-reference-data.sh <database-url>
#
# Example:
#   ./scripts/backfill-tenant-reference-data.sh "postgres://user:pass@localhost:26257/meridian_reference_data?sslmode=disable"
#
# What it does:
#   1. Finds all tenant schemas (org_* pattern)
#   2. For each tenant schema:
#      a. Copies saga scripts from public.platform_saga_definition into
#         tenant saga_definition rows that have platform_ref but NULL script
#      b. Seeds missing platform instruments (GBP, USD, EUR, TONNE_CO2E, KWH)
#   3. Reports summary of changes
#
# This script is idempotent - safe to run multiple times.
# Dry-run mode: set DRY_RUN=1 to see what would change without modifying data.

set -euo pipefail

DATABASE_URL="${1:?Usage: $0 <database-url>}"
DRY_RUN="${DRY_RUN:-0}"

if [ "$DRY_RUN" = "1" ]; then
    echo "=== DRY RUN MODE - no changes will be made ==="
fi

echo "Backfilling tenant reference data..."
echo "Database: ${DATABASE_URL%%://*}://***"

# Find all tenant schemas
SCHEMAS=$(psql "$DATABASE_URL" -t -A -c "
    SELECT nspname FROM pg_namespace
    WHERE nspname LIKE 'org_%'
    ORDER BY nspname
")

if [ -z "$SCHEMAS" ]; then
    echo "No tenant schemas found. Nothing to backfill."
    exit 0
fi

SCHEMA_COUNT=$(echo "$SCHEMAS" | wc -l | tr -d ' ')
echo "Found $SCHEMA_COUNT tenant schema(s)"

TOTAL_SAGAS_UPDATED=0
TOTAL_INSTRUMENTS_SEEDED=0

for SCHEMA in $SCHEMAS; do
    echo ""
    echo "--- Processing $SCHEMA ---"

    # Step 1: Copy saga scripts from platform table into tenant rows with platform_ref
    if [ "$DRY_RUN" = "1" ]; then
        COUNT=$(psql "$DATABASE_URL" -t -A -c "
            SELECT COUNT(*)
            FROM \"$SCHEMA\".saga_definition sd
            JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
            WHERE (sd.script IS NULL OR sd.script = '')
        ")
        echo "  Would update $COUNT saga(s) with script content"
    else
        RESULT=$(psql "$DATABASE_URL" -t -A -c "
            UPDATE \"$SCHEMA\".saga_definition sd
            SET script = psd.script,
                updated_at = NOW()
            FROM public.platform_saga_definition psd
            WHERE sd.platform_ref = psd.id
              AND (sd.script IS NULL OR sd.script = '')
            RETURNING sd.name
        ")
        COUNT=$(echo "$RESULT" | grep -c . || true)
        if [ "$COUNT" -gt 0 ]; then
            echo "  Updated $COUNT saga(s) with script content"
            TOTAL_SAGAS_UPDATED=$((TOTAL_SAGAS_UPDATED + COUNT))
        else
            echo "  No sagas needed script backfill"
        fi
    fi

    # Step 2: Seed platform instruments if missing
    INSTRUMENTS="GBP:MONETARY:2 USD:MONETARY:2 EUR:MONETARY:2 TONNE_CO2E:CARBON:6 KWH:ENERGY:3"
    for SPEC in $INSTRUMENTS; do
        CODE=$(echo "$SPEC" | cut -d: -f1)
        DIMENSION=$(echo "$SPEC" | cut -d: -f2)
        PRECISION=$(echo "$SPEC" | cut -d: -f3)

        # Check if table exists in this schema
        TABLE_EXISTS=$(psql "$DATABASE_URL" -t -A -c "
            SELECT EXISTS(
                SELECT 1 FROM information_schema.tables
                WHERE table_schema = '$SCHEMA' AND table_name = 'instrument_definition'
            )
        ")

        if [ "$TABLE_EXISTS" != "t" ]; then
            continue
        fi

        if [ "$DRY_RUN" = "1" ]; then
            EXISTS=$(psql "$DATABASE_URL" -t -A -c "
                SELECT EXISTS(
                    SELECT 1 FROM \"$SCHEMA\".instrument_definition
                    WHERE code = '$CODE' AND version = 1
                )
            ")
            if [ "$EXISTS" = "f" ]; then
                echo "  Would seed instrument: $CODE ($DIMENSION, precision=$PRECISION)"
            fi
        else
            INSERTED=$(psql "$DATABASE_URL" -t -A -c "
                INSERT INTO \"$SCHEMA\".instrument_definition (
                    id, code, version, dimension, precision, status, is_system,
                    fungibility_key_expression, display_name, description,
                    created_at, updated_at, activated_at
                ) VALUES (
                    gen_random_uuid(), '$CODE', 1, '$DIMENSION', $PRECISION, 'ACTIVE', true,
                    '', '$CODE instrument', 'Platform default instrument: $CODE',
                    NOW(), NOW(), NOW()
                ) ON CONFLICT (code, version) DO NOTHING
                RETURNING code
            ")
            if [ -n "$INSERTED" ]; then
                echo "  Seeded instrument: $CODE"
                TOTAL_INSTRUMENTS_SEEDED=$((TOTAL_INSTRUMENTS_SEEDED + 1))
            fi
        fi
    done
done

echo ""
echo "=== Summary ==="
echo "Schemas processed: $SCHEMA_COUNT"
echo "Sagas updated with scripts: $TOTAL_SAGAS_UPDATED"
echo "Instruments seeded: $TOTAL_INSTRUMENTS_SEEDED"

if [ "$DRY_RUN" = "1" ]; then
    echo ""
    echo "This was a dry run. Run without DRY_RUN=1 to apply changes."
fi
