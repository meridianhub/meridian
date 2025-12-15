-- Add party_id column to platform.tenants
-- Links platform infrastructure (Tenant) to BIAN domain entities (Party.Organization)
-- Task: Link Tenant to Party reference data (Task Master #38)

-- Add nullable party_id column (nullable because existing tenants won't have party_id)
ALTER TABLE platform.tenants ADD COLUMN IF NOT EXISTS party_id VARCHAR(100);

-- Create index for faster party lookups (non-partial for CockroachDB compatibility)
CREATE INDEX IF NOT EXISTS idx_tenants_party_id ON platform.tenants(party_id);

-- Add comment documenting the field's purpose
COMMENT ON COLUMN platform.tenants.party_id IS 'Reference to corresponding Party in BIAN Party Reference Data Directory (auto-populated on tenant creation)';
