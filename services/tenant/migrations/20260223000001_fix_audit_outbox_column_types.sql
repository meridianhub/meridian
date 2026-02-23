-- Fix audit_outbox and audit_log column types: JSONB → TEXT (part 1: schema)
-- The shared audit package (AuditOutbox, AuditLog structs) declares these fields
-- as string/text but the initial migration created them as JSONB. CockroachDB
-- rejects empty-string old_values on INSERT audit events since '' is not valid JSON.
--
-- CockroachDB requires a column to be "public" (committed) before DML can reference it.
-- Part 1: rename JSONB columns and add new TEXT columns (DDL only).
-- Part 2 (next migration): copy data, drop old columns, recreate VIEW.

-- Drop dependent view before altering columns
DROP VIEW IF EXISTS change_summary;

-- audit_outbox: rename JSONB columns, add TEXT replacements
ALTER TABLE audit_outbox RENAME COLUMN old_values TO old_values_jsonb;
ALTER TABLE audit_outbox ADD COLUMN old_values TEXT;
ALTER TABLE audit_outbox RENAME COLUMN new_values TO new_values_jsonb;
ALTER TABLE audit_outbox ADD COLUMN new_values TEXT;

-- audit_log: rename JSONB columns, add TEXT replacements
ALTER TABLE audit_log RENAME COLUMN old_values TO old_values_jsonb;
ALTER TABLE audit_log ADD COLUMN old_values TEXT;
ALTER TABLE audit_log RENAME COLUMN new_values TO new_values_jsonb;
ALTER TABLE audit_log ADD COLUMN new_values TEXT;
