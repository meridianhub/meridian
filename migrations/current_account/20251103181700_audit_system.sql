-- Current Account Audit System
-- Uses the shared audit factory to create service-specific audit infrastructure
-- See migrations/shared/20251103190000_audit_factory.sql for the factory implementation

-- Initialize audit system for current_account service
-- This creates:
--   - current_account_audit schema
--   - change_log table with indexes
--   - log_change() trigger function
--   - enable_audit_log() helper function
--   - change_summary view
--   - get_record_history() query function
-- And attaches audit triggers to all tables

SELECT _audit_factory.init_service_audit(
    'current_account',              -- Service schema to audit
    'current_account_audit',        -- Audit schema name
    ARRAY['customers', 'accounts']  -- Tables to attach triggers to
);
