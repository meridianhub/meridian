-- Append-Only Positions Table Migration
-- Implements O(1) constant-time position writes for high-throughput multi-asset tracking (Task 21)
-- Uses unqualified table names (relies on database-per-service architecture per ADR-002)
--
-- ARCHITECTURAL NOTES:
-- - This table uses append-only semantics: INSERT is the ONLY write operation
-- - Position consolidation happens at read-time via GROUP BY aggregation
-- - Database trigger prevents UPDATE on amount column to enforce append-only at DB level
-- - Schema-per-tenant isolation means NO tenant_id column needed (per ADR-0016)

-- Create "position" table (singular, unqualified)
CREATE TABLE "position" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "account_id" character varying(34) NOT NULL,
  "instrument_code" character varying(32) NOT NULL,
  "bucket_key" character varying(256) NOT NULL,
  "amount" decimal(38, 18) NOT NULL,
  "dimension" character varying(32) NOT NULL DEFAULT 'Monetary',
  "attributes" jsonb NULL,
  "reference_id" uuid NULL,
  PRIMARY KEY ("id")
);

-- Create indexes for position lookups and aggregation
-- Index for account-based queries
CREATE INDEX "idx_position_account_id" ON "position" ("account_id");

-- Composite index for position aggregation: (account_id, instrument_code, bucket_key)
-- Enables efficient GROUP BY for read-time consolidation
CREATE INDEX "idx_position_aggregation" ON "position" ("account_id", "instrument_code", "bucket_key");

-- Index for soft delete filtering
CREATE INDEX "idx_position_deleted_at" ON "position" ("deleted_at");

-- Partial composite index for active positions queries (optimizes GetAggregatedPosition, ListByAccount)
-- More efficient than separate indexes for queries filtering on deleted_at IS NULL
CREATE INDEX "idx_position_active" ON "position" ("account_id", "instrument_code", "bucket_key")
WHERE deleted_at IS NULL;

-- Index for reference lookups (linking to measurements/transactions)
CREATE INDEX "idx_position_reference_id" ON "position" ("reference_id");

-- Index for time-based queries
CREATE INDEX "idx_position_created_at" ON "position" ("created_at");

-- Add validation constraint for dimension values
ALTER TABLE "position"
  ADD CONSTRAINT "chk_position_dimension"
  CHECK ("dimension" IN ('Monetary', 'Energy', 'Compute', 'Carbon', 'Time', 'Physical', 'Custom'));

-- ============================================================================
-- APPEND-ONLY ENFORCEMENT TRIGGER
-- ============================================================================
-- This trigger enforces append-only semantics at the database level.
-- It prevents UPDATE operations on the amount column to guarantee data immutability.
-- This is critical for audit trail integrity and concurrent write performance.

-- Create the trigger function
CREATE OR REPLACE FUNCTION positions_append_only()
RETURNS TRIGGER AS $$
BEGIN
  -- Check if amount column is being modified
  IF OLD.amount IS DISTINCT FROM NEW.amount THEN
    RAISE EXCEPTION 'positions table is append-only - UPDATE on amount column is forbidden'
      USING ERRCODE = 'P0001',
            HINT = 'Create a new position record instead of updating the existing one';
  END IF;

  -- Also prevent modification of immutable fields
  IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
    RAISE EXCEPTION 'positions table is append-only - UPDATE on account_id column is forbidden'
      USING ERRCODE = 'P0001';
  END IF;

  IF OLD.instrument_code IS DISTINCT FROM NEW.instrument_code THEN
    RAISE EXCEPTION 'positions table is append-only - UPDATE on instrument_code column is forbidden'
      USING ERRCODE = 'P0001';
  END IF;

  IF OLD.bucket_key IS DISTINCT FROM NEW.bucket_key THEN
    RAISE EXCEPTION 'positions table is append-only - UPDATE on bucket_key column is forbidden'
      USING ERRCODE = 'P0001';
  END IF;

  IF OLD.reference_id IS DISTINCT FROM NEW.reference_id THEN
    RAISE EXCEPTION 'positions table is append-only - UPDATE on reference_id column is forbidden'
      USING ERRCODE = 'P0001';
  END IF;

  -- Allow UPDATE only for deleted_at (soft delete) and attributes (metadata updates)
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create the trigger
CREATE TRIGGER positions_append_only
  BEFORE UPDATE ON "position"
  FOR EACH ROW
  EXECUTE FUNCTION positions_append_only();

-- Add comment documenting the append-only architecture
COMMENT ON TABLE "position" IS 'Append-only position records for O(1) writes. Use INSERT only - UPDATE on amount is forbidden by trigger.';
COMMENT ON TRIGGER positions_append_only ON "position" IS 'Enforces append-only semantics by preventing UPDATE on amount and identity columns.';
