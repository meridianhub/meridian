-- Add bucket_id to measurement table for position bucketing
-- Enables grouping measurements by fungibility bucket for aggregation
-- Part of Universal Asset System (Task 20.3 - Type Bridging and Domain Handoff)

-- Add bucket_id column for measurement bucketing
-- This is computed from measurement attributes via CEL expression
-- Nullable because bucket key expression is optional on instruments
ALTER TABLE "measurement"
  ADD COLUMN "bucket_id" character varying(256) NULL;

-- Create index for efficient queries by bucket
-- Enables O(log N) lookup for measurements within a specific bucket
CREATE INDEX "idx_measurement_bucket_id"
  ON "measurement" ("bucket_id");

-- Create composite index for position log + bucket queries
-- Optimizes queries that filter by position and then bucket
CREATE INDEX "idx_measurement_position_bucket"
  ON "measurement" ("financial_position_log_id", "bucket_id");
