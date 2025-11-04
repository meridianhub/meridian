-- Position Keeping Audit System
-- Uses the shared audit factory to create service-specific audit infrastructure
-- See migrations/shared/20251103190000_audit_factory.sql for the factory implementation

-- Initialize audit system for position_keeping service
-- This creates:
--   - position_keeping_audit schema
--   - change_log table with indexes
--   - log_change() trigger function
--   - enable_audit_log() helper function
--   - change_summary view
--   - get_record_history() query function
-- And attaches audit triggers to all tables

SELECT _audit_factory.init_service_audit(
    'position_keeping',              -- Service schema to audit
    'position_keeping_audit',        -- Audit schema name
    ARRAY['transactions']            -- Tables to attach triggers to
);
