-- Multi-Asset Columns Migration
-- Extends financial_position_log with universal asset support per Universal Asset System (Task 19)
-- Enables tracking of non-monetary assets (energy, compute, carbon credits) alongside monetary positions
--
-- ARCHITECTURAL NOTES (ADR-0002, ADR-0016):
-- - Schema-per-tenant isolation means NO tenant_id column needed
-- - Each tenant's positions are in separate schemas (e.g., org_acme_bank.financial_position_log)
-- - Queries automatically scoped via search_path
-- - Uses unqualified table names (relies on database-per-service architecture)

-- Add instrument_code column for asset identification
-- References instrument definitions from Reference Data service via gRPC (no FK - service boundary per ADR-002)
ALTER TABLE "financial_position_log"
  ADD COLUMN "instrument_code" character varying(32) NULL;

-- Add bucket_id column for position bucketing and aggregation
-- Used with instrument_code for O(log N) position aggregation queries
ALTER TABLE "financial_position_log"
  ADD COLUMN "bucket_id" character varying(256) NULL;

-- Add dimension column to classify asset type
-- Enables reading positions without Reference Data dependency (read availability decoupling)
-- Defaults to 'Monetary' for backward compatibility with existing rows
ALTER TABLE "financial_position_log"
  ADD COLUMN "dimension" character varying(32) NOT NULL DEFAULT 'Monetary';

-- Add attributes column for flexible metadata storage
-- Stores AttributeEntry data as JSONB for CEL-based fungibility key generation
ALTER TABLE "financial_position_log"
  ADD COLUMN "attributes" jsonb NULL;

-- Create composite index for efficient position aggregation by bucket
-- Index order: (instrument_code, bucket_id) enables filtering by instrument before aggregating by bucket
-- Achieves O(log N) lookup performance instead of full table scans
-- No tenant_id in index - schema-per-tenant isolation handles tenant scoping automatically
CREATE INDEX "idx_financial_position_log_instrument_bucket"
  ON "financial_position_log" ("instrument_code", "bucket_id");

-- Create standalone index on bucket_id for pure bucket-based queries
-- Optimizes queries that filter or aggregate solely by bucket_id
CREATE INDEX "idx_financial_position_log_bucket_id"
  ON "financial_position_log" ("bucket_id");

-- Add validation constraint for dimension values
-- Matches common dimension types used across Meridian
ALTER TABLE "financial_position_log"
  ADD CONSTRAINT "chk_financial_position_log_dimension"
  CHECK ("dimension" IN ('Monetary', 'Energy', 'Compute', 'Carbon', 'Time', 'Physical', 'Custom'));
