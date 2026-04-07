-- Tenant Schedule table for manifest-driven cron scheduling.
-- Written by manifest application, read by ScheduleProvider.
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed.
-- manifest_version_id is a soft cross-schema reference for audit/debugging - no FK constraint.

CREATE TABLE tenant_schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_name VARCHAR(128) NOT NULL,
    saga_name VARCHAR(128) NOT NULL,
    cron_expr VARCHAR(64) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    manifest_version_id UUID,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tenant_schedule_name UNIQUE (schedule_name)
);

-- Index for ScheduleProvider to load all enabled schedules efficiently.
CREATE INDEX idx_tenant_schedule_enabled ON tenant_schedule (enabled);

-- Index for manifest applier to look up schedules by saga.
CREATE INDEX idx_tenant_schedule_saga_name ON tenant_schedule (saga_name);
