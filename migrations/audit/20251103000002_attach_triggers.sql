-- Attach audit triggers to business tables
-- This should be run AFTER the main schema migrations

-- Enable audit logging for current_account domain tables
SELECT audit.enable_audit_log('current_account', 'customers');
SELECT audit.enable_audit_log('current_account', 'accounts');

-- Enable audit logging for position_keeping domain tables
SELECT audit.enable_audit_log('position_keeping', 'transactions');

-- Verify triggers are attached
SELECT
    schemaname AS schema_name,
    tablename AS table_name,
    tgname AS trigger_name,
    tgenabled AS enabled
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
JOIN pg_namespace n ON c.relnamespace = n.oid
WHERE tgname LIKE 'audit_%'
ORDER BY schemaname, tablename;
