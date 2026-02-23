-- Fix audit_outbox and audit_log column types: JSONB → TEXT
-- The shared audit package (AuditOutbox, AuditLog structs) declares these fields
-- as string/text but the initial migration created them as JSONB. CockroachDB
-- rejects empty-string old_values on INSERT audit events since '' is not valid JSON.

SET enable_experimental_alter_column_type_general = 'on';

ALTER TABLE audit_outbox ALTER COLUMN old_values TYPE TEXT;
ALTER TABLE audit_outbox ALTER COLUMN new_values TYPE TEXT;

ALTER TABLE audit_log ALTER COLUMN old_values TYPE TEXT;
ALTER TABLE audit_log ALTER COLUMN new_values TYPE TEXT;
