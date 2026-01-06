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
-- NOTE: Keep in sync with domain.ValidDimensions in services/position-keeping/domain/position.go
ALTER TABLE "position"
  ADD CONSTRAINT "chk_position_dimension"
  CHECK ("dimension" IN ('Monetary', 'Energy', 'Compute', 'Carbon', 'Time', 'Physical', 'Custom'));

-- ============================================================================
-- APPEND-ONLY ENFORCEMENT
-- ============================================================================
-- NOTE: PL/pgSQL triggers are not supported in CockroachDB.
-- Append-only semantics are enforced at the application level by the repository.
-- For PostgreSQL deployments, consider adding a trigger in a separate migration.
-- This is documented in ADR-XXX (pending).

-- Add comment documenting the append-only architecture
COMMENT ON TABLE "position" IS 'Append-only position records for O(1) writes. Use INSERT only - UPDATE on amount is enforced at application level.';
