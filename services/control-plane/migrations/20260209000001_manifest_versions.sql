-- Manifest Version History
-- Stores immutable snapshots of tenant manifests with audit trail.
-- Uses forward-only versioning: rollbacks create new versions, never delete history.
-- This table lives in each tenant schema (org_{tenant_id}).

CREATE TABLE manifest_versions (
    -- Unique identifier for this manifest version record
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Manifest schema version (e.g., "1.0", "1.1", "2.0")
    version VARCHAR(50) NOT NULL,

    -- Complete manifest snapshot as JSONB (protobuf JSON serialization)
    manifest_json JSONB NOT NULL,

    -- When this manifest version was applied
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Who applied this manifest (staff identity, API key, or system)
    applied_by VARCHAR(255) NOT NULL,

    -- Outcome of applying this manifest
    apply_status VARCHAR(20) NOT NULL DEFAULT 'APPLIED',

    -- Reference to the job that applied this manifest
    apply_job_id UUID,

    -- Human-readable summary of changes from the previous version
    diff_summary TEXT,

    -- Record creation timestamp
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT valid_apply_status CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK'))
);

-- Index for retrieving the latest applied manifest (most common query)
CREATE INDEX idx_manifest_versions_applied_at ON manifest_versions(applied_at DESC);

-- Index for looking up a specific version
CREATE INDEX idx_manifest_versions_version ON manifest_versions(version);

-- Index for filtering by apply status (e.g., finding all APPLIED versions)
CREATE INDEX idx_manifest_versions_status ON manifest_versions(apply_status);

-- Comments
COMMENT ON TABLE manifest_versions IS 'Immutable audit trail of tenant manifest versions';
COMMENT ON COLUMN manifest_versions.version IS 'Manifest schema version (e.g., 1.0)';
COMMENT ON COLUMN manifest_versions.manifest_json IS 'Complete manifest snapshot as protobuf JSON';
COMMENT ON COLUMN manifest_versions.applied_by IS 'Identity that applied this manifest version';
COMMENT ON COLUMN manifest_versions.apply_status IS 'Outcome: APPLIED, FAILED, or ROLLED_BACK';
COMMENT ON COLUMN manifest_versions.diff_summary IS 'Human-readable diff from previous applied version';
