-- Shared Audit Factory
-- This creates reusable audit infrastructure that can be used by any service
-- Provides a parameterized factory for creating service-specific audit schemas

-- Create shared utilities schema
CREATE SCHEMA IF NOT EXISTS _audit_factory;

-- Factory procedure to initialize audit system for a service
-- Parameters:
--   p_service_schema: The schema to audit (e.g., 'current_account')
--   p_audit_schema: The audit schema name (e.g., 'current_account_audit')
--   p_tables: Array of table names to attach audit triggers to
CREATE OR REPLACE FUNCTION _audit_factory.init_service_audit(
    p_service_schema VARCHAR(100),
    p_audit_schema VARCHAR(100),
    p_tables TEXT[]
) RETURNS VOID AS $$
DECLARE
    table_name TEXT;
BEGIN
    -- Create audit schema
    EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', p_audit_schema);

    -- Create audit log table
    EXECUTE format('
        CREATE TABLE IF NOT EXISTS %I.change_log (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

            -- What changed
            table_name VARCHAR(100) NOT NULL,
            operation VARCHAR(10) NOT NULL,

            -- Record identification
            record_id UUID NOT NULL,

            -- Change metadata
            changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
            changed_by VARCHAR(100),  -- Nullable to support operations before auth context available

            -- Change details
            old_values JSONB,
            new_values JSONB,

            -- Additional context
            transaction_id VARCHAR(100),
            client_ip INET,
            user_agent TEXT
        )', p_audit_schema);

    -- Create indexes for efficient audit queries
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_change_log_record_id ON %I.change_log(record_id)', p_audit_schema);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_change_log_table ON %I.change_log(table_name)', p_audit_schema);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_change_log_changed_at ON %I.change_log(changed_at)', p_audit_schema);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_change_log_changed_by ON %I.change_log(changed_by)', p_audit_schema);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_change_log_operation ON %I.change_log(operation)', p_audit_schema);

    -- Create generic audit trigger function
    EXECUTE format('
        CREATE OR REPLACE FUNCTION %I.log_change()
        RETURNS TRIGGER AS $BODY$
        DECLARE
            audit_row %I.change_log;
        BEGIN
            audit_row.table_name := TG_TABLE_NAME;
            audit_row.operation := TG_OP;
            audit_row.changed_at := now();

            -- Handle different operations
            CASE TG_OP
                WHEN ''INSERT'' THEN
                    audit_row.record_id := NEW.id;
                    audit_row.changed_by := COALESCE(NEW.created_by, ''system'');
                    audit_row.new_values := to_jsonb(NEW);
                    audit_row.old_values := NULL;

                WHEN ''UPDATE'' THEN
                    audit_row.record_id := NEW.id;
                    audit_row.changed_by := COALESCE(NEW.updated_by, ''system'');
                    audit_row.new_values := to_jsonb(NEW);
                    audit_row.old_values := to_jsonb(OLD);

                WHEN ''DELETE'' THEN
                    audit_row.record_id := OLD.id;
                    audit_row.changed_by := COALESCE(OLD.updated_by, ''system'');
                    audit_row.new_values := NULL;
                    audit_row.old_values := to_jsonb(OLD);
            END CASE;

            -- Insert audit record
            INSERT INTO %I.change_log (
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
            IF TG_OP = ''DELETE'' THEN
                RETURN OLD;
            ELSE
                RETURN NEW;
            END IF;
        END;
        $BODY$ LANGUAGE plpgsql;
    ', p_audit_schema, p_audit_schema, p_audit_schema);

    -- Create helper function to attach audit trigger to a table
    EXECUTE format('
        CREATE OR REPLACE FUNCTION %I.enable_audit_log(p_table_name VARCHAR(100))
        RETURNS VOID AS $BODY$
        DECLARE
            trigger_name VARCHAR(200);
        BEGIN
            trigger_name := ''audit_'' || p_table_name || ''_trigger'';

            EXECUTE format(
                ''DROP TRIGGER IF EXISTS %%I ON %I.%%I;
                 CREATE TRIGGER %%I
                 AFTER INSERT OR UPDATE OR DELETE ON %I.%%I
                 FOR EACH ROW EXECUTE FUNCTION %I.log_change()'',
                trigger_name,
                p_table_name,
                trigger_name,
                p_table_name
            );
        END;
        $BODY$ LANGUAGE plpgsql;
    ', p_audit_schema, p_service_schema, p_service_schema, p_audit_schema);

    -- Create helper view for easy audit queries
    EXECUTE format('
        CREATE OR REPLACE VIEW %I.change_summary AS
        SELECT
            id,
            ''%I.'' || table_name AS table_full_name,
            operation,
            record_id,
            changed_at,
            changed_by,
            CASE
                WHEN operation = ''UPDATE'' THEN
                    (SELECT json_object_agg(key, value)
                     FROM jsonb_each(new_values)
                     WHERE new_values->key IS DISTINCT FROM old_values->key)
                ELSE NULL
            END AS changed_fields,
            transaction_id
        FROM %I.change_log
        ORDER BY changed_at DESC
    ', p_audit_schema, p_service_schema, p_audit_schema);

    -- Create function to get audit history for a specific record
    EXECUTE format('
        CREATE OR REPLACE FUNCTION %I.get_record_history(
            p_record_id UUID,
            p_limit INT DEFAULT 100
        )
        RETURNS TABLE (
            changed_at TIMESTAMPTZ,
            operation VARCHAR(10),
            changed_by VARCHAR(100),
            changed_fields JSONB
        ) AS $BODY$
        BEGIN
            RETURN QUERY
            SELECT
                cl.changed_at,
                cl.operation,
                cl.changed_by,
                CASE
                    WHEN cl.operation = ''UPDATE'' THEN
                        (SELECT jsonb_object_agg(key, value)
                         FROM jsonb_each(cl.new_values)
                         WHERE cl.new_values->key IS DISTINCT FROM cl.old_values->key)
                    WHEN cl.operation = ''INSERT'' THEN cl.new_values
                    WHEN cl.operation = ''DELETE'' THEN cl.old_values
                END AS changed_fields
            FROM %I.change_log cl
            WHERE cl.record_id = p_record_id
            ORDER BY cl.changed_at DESC
            LIMIT p_limit;
        END;
        $BODY$ LANGUAGE plpgsql;
    ', p_audit_schema, p_audit_schema);

    -- Attach triggers to all specified tables
    FOREACH table_name IN ARRAY p_tables
    LOOP
        EXECUTE format('SELECT %I.enable_audit_log(%L)', p_audit_schema, table_name);
    END LOOP;

    RAISE NOTICE 'Audit system initialized for %.% with % tables',
        p_service_schema, p_audit_schema, array_length(p_tables, 1);
END;
$$ LANGUAGE plpgsql;

-- Convenience function to check if audit is enabled for a table
CREATE OR REPLACE FUNCTION _audit_factory.is_audit_enabled(
    p_service_schema VARCHAR(100),
    p_table_name VARCHAR(100)
) RETURNS BOOLEAN AS $$
DECLARE
    trigger_exists BOOLEAN;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM information_schema.triggers
        WHERE event_object_schema = p_service_schema
        AND event_object_table = p_table_name
        AND trigger_name LIKE 'audit_%_trigger'
    ) INTO trigger_exists;

    RETURN trigger_exists;
END;
$$ LANGUAGE plpgsql;
