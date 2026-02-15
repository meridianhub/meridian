-- Add metadata and lifecycle fields to party_association for syndicate structuring
ALTER TABLE "party_association" ADD COLUMN "metadata" JSONB NULL DEFAULT '{}'::jsonb;
ALTER TABLE "party_association" ADD COLUMN "status" VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE "party_association" ADD COLUMN "effective_from" TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE "party_association" ADD COLUMN "effective_to" TIMESTAMPTZ NULL;

COMMENT ON COLUMN "party_association"."metadata" IS
  'Extensible JSONB field for syndicate structuring data (e.g., allocation_share, role). Used by sagas via party.get_structuring_data.';
COMMENT ON COLUMN "party_association"."status" IS
  'Association lifecycle status: ACTIVE, SUSPENDED, or TERMINATED. Follows BIAN Arrangement Lifecycle pattern.';
