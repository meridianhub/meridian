-- Corrective migration: documents broken COMMENT ON COLUMN from 20260221000001.
-- Root cause: processMigrationSQL naively rewrites "party"."attributes" to
-- "org_tenant"."attributes", interpreting column name as table name.
-- The party.attributes column works fine - only the metadata comment was lost.
--
-- Note: The rewriter fix in processMigrationSQL (which makes 20260221000001
-- succeed) is the companion change that makes this no-op safe. Without the
-- rewriter fix, 20260221000001 would fail before reaching this migration.
-- With the rewriter fix, this serves as an audit trail marker.
SELECT 1;
