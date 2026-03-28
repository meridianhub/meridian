-- Manifest Version Table
-- Stores immutable snapshots of tenant manifests with audit trail.
-- Uses forward-only versioning: rollbacks create new versions, never delete history.
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed.

CREATE TABLE manifest_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version INTEGER NOT NULL,
    manifest_json JSONB NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_by VARCHAR(255) NOT NULL,
    apply_status VARCHAR(20) NOT NULL DEFAULT 'APPLIED',
    apply_job_id UUID,
    diff_summary TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_manifest_version_version UNIQUE (version),
    CONSTRAINT valid_apply_status CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK'))
);

-- Index for retrieving the latest applied manifest (most common query)
CREATE INDEX idx_manifest_version_applied_at ON manifest_version (applied_at DESC);

-- Index for version-ordered lookups
CREATE INDEX idx_manifest_version_version ON manifest_version (version DESC);

-- Index for filtering by apply status
CREATE INDEX idx_manifest_version_status ON manifest_version (apply_status);
