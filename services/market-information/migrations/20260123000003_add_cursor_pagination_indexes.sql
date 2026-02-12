-- atlas:txn false
-- Migration: Add Cursor-Based Pagination Indexes
-- Optimizes cursor-based pagination queries for list endpoints.
-- Uses (created_at DESC, id DESC) composite indexes to support stable cursor ordering.
--
-- Performance notes:
-- - These indexes support efficient pagination without sorting entire tables
-- - Composite index with id DESC ensures stable ordering for same-timestamp records
-- - WHERE deleted_at IS NULL filters match the soft-delete pattern
--
-- Note: Original design used date_trunc('second', created_at) expression indexes,
-- but CockroachDB does not support context-dependent operators in expression indexes.
-- Using created_at directly provides equivalent cursor ordering with full precision.

--------------------------------------------------------------------------------
-- Section 1: Data Source Cursor Index
--------------------------------------------------------------------------------

-- Index for SourceRepository.List pagination
-- Supports ORDER BY created_at DESC, id DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_data_source_cursor
  ON data_source (created_at DESC, id DESC)
  WHERE deleted_at IS NULL;

COMMENT ON INDEX idx_data_source_cursor IS 'Optimizes cursor-based pagination for ListDataSources endpoint';

--------------------------------------------------------------------------------
-- Section 2: Dataset Definition Cursor Index
--------------------------------------------------------------------------------

-- Index for DataSetRepository.List pagination
-- Supports ORDER BY created_at DESC, id DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_dataset_definition_cursor
  ON dataset_definition (created_at DESC, id DESC)
  WHERE deleted_at IS NULL;

COMMENT ON INDEX idx_dataset_definition_cursor IS 'Optimizes cursor-based pagination for ListDataSets endpoint';

--------------------------------------------------------------------------------
-- Section 3: Market Price Observation Cursor Index
--------------------------------------------------------------------------------

-- Index for ObservationRepository.Query pagination
-- Supports ORDER BY created_at DESC, id DESC
-- Note: No deleted_at filter - observations are append-only
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_price_observation_cursor
  ON market_price_observation (created_at DESC, id DESC);

COMMENT ON INDEX idx_market_price_observation_cursor IS 'Optimizes cursor-based pagination for ListObservations endpoint';
