-- Corrective migration: Skip broken COMMENT ON COLUMN from 20260221000001
-- Root cause: processMigrationSQL naively rewrites "party"."attributes" to
-- "org_tenant"."attributes", interpreting column name as table name.
-- The party.attributes column works fine - only the metadata comment was lost.
SELECT 1;
