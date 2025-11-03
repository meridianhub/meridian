-- Current Account Audit System
-- This creates a service-specific audit schema for the Current Account domain
-- All changes to current_account tables are logged to current_account_audit.change_log

-- Create audit schema for current_account service
CREATE SCHEMA IF NOT EXISTS current_account_audit;

-- Audit log table - stores all changes to current_account tables
CREATE TABLE current_account_audit.change_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL, -- INSERT, UPDATE, DELETE

    -- Record identification
    record_id UUID NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100) NOT NULL,

    -- Change details
    old_values JSONB, -- NULL for INSERT
    new_values JSONB, -- NULL for DELETE

    -- Additional context
    transaction_id VARCHAR(100), -- For correlating related changes
    client_ip INET,
    user_agent TEXT
);

-- Indexes for efficient audit queries
CREATE INDEX idx_change_log_record_id ON current_account_audit.change_log(record_id);
CREATE INDEX idx_change_log_table ON current_account_audit.change_log(table_name);
CREATE INDEX idx_change_log_changed_at ON current_account_audit.change_log(changed_at);
CREATE INDEX idx_change_log_changed_by ON current_account_audit.change_log(changed_by);
CREATE INDEX idx_change_log_operation ON current_account_audit.change_log(operation);

-- Generic audit trigger function for current_account tables
CREATE OR REPLACE FUNCTION current_account_audit.log_change()
RETURNS TRIGGER AS $$
DECLARE
    audit_row current_account_audit.change_log;
BEGIN
    audit_row.table_name := TG_TABLE_NAME;
    audit_row.operation := TG_OP;
    audit_row.changed_at := now();

    -- Handle different operations
    CASE TG_OP
        WHEN 'INSERT' THEN
            audit_row.record_id := NEW.id;
            audit_row.changed_by := NEW.created_by;
            audit_row.new_values := to_jsonb(NEW);
            audit_row.old_values := NULL;

        WHEN 'UPDATE' THEN
            audit_row.record_id := NEW.id;
            audit_row.changed_by := NEW.updated_by;
            audit_row.new_values := to_jsonb(NEW);
            audit_row.old_values := to_jsonb(OLD);

        WHEN 'DELETE' THEN
            audit_row.record_id := OLD.id;
            audit_row.changed_by := OLD.updated_by;
            audit_row.new_values := NULL;
            audit_row.old_values := to_jsonb(OLD);
    END CASE;

    -- Insert audit record
    INSERT INTO current_account_audit.change_log (
        table_name,
        operation,
        record_id,
        changed_at,
        changed_by,
        old_values,
        new_values
    ) VALUES (
        audit_row.table_name,
        audit_row.operation,
        audit_row.record_id,
        audit_row.changed_at,
        audit_row.changed_by,
        audit_row.old_values,
        audit_row.new_values
    );

    -- Return the appropriate row
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    ELSE
        RETURN NEW;
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Helper function to attach audit trigger to a table
CREATE OR REPLACE FUNCTION current_account_audit.enable_audit_log(p_table_name VARCHAR(100))
RETURNS VOID AS $$
DECLARE
    trigger_name VARCHAR(200);
BEGIN
    trigger_name := 'audit_' || p_table_name || '_trigger';

    EXECUTE format(
        'CREATE TRIGGER %I
         AFTER INSERT OR UPDATE OR DELETE ON current_account.%I
         FOR EACH ROW EXECUTE FUNCTION current_account_audit.log_change()',
        trigger_name,
        p_table_name
    );
END;
$$ LANGUAGE plpgsql;

-- Helper view for easy audit queries
CREATE VIEW current_account_audit.change_summary AS
SELECT
    id,
    'current_account.' || table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' THEN
            (SELECT json_object_agg(key, value)
             FROM jsonb_each(new_values)
             WHERE new_values->key IS DISTINCT FROM old_values->key)
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM current_account_audit.change_log
ORDER BY changed_at DESC;

-- Function to get audit history for a specific record
CREATE OR REPLACE FUNCTION current_account_audit.get_record_history(
    p_record_id UUID,
    p_limit INT DEFAULT 100
)
RETURNS TABLE (
    changed_at TIMESTAMPTZ,
    operation VARCHAR(10),
    changed_by VARCHAR(100),
    changed_fields JSONB
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        cl.changed_at,
        cl.operation,
        cl.changed_by,
        CASE
            WHEN cl.operation = 'UPDATE' THEN
                (SELECT jsonb_object_agg(key, value)
                 FROM jsonb_each(cl.new_values)
                 WHERE cl.new_values->key IS DISTINCT FROM cl.old_values->key)
            WHEN cl.operation = 'INSERT' THEN cl.new_values
            WHEN cl.operation = 'DELETE' THEN cl.old_values
        END AS changed_fields
    FROM current_account_audit.change_log cl
    WHERE cl.record_id = p_record_id
    ORDER BY cl.changed_at DESC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql;

-- Attach audit triggers to current_account tables
SELECT current_account_audit.enable_audit_log('customers');
SELECT current_account_audit.enable_audit_log('accounts');
