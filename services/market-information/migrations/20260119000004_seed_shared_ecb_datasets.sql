-- Mark ECB datasets as shared/public (deferred from 20260119000002).
-- CockroachDB requires columns added by ALTER TABLE to be committed in a
-- prior transaction before they can be referenced by DML statements.
--
-- This UPDATE is idempotent and forward-looking: it will mark ECB datasets as shared
-- when they are created by the ECB adapter worker. Until then, this safely matches
-- zero rows since seed data uses 'FX_RATE' (a generic test dataset), not ECB-specific codes.
UPDATE dataset_definition
SET
  is_shared = TRUE,
  access_level = 'PUBLIC',
  updated_at = NOW(),
  updated_by = 'MIGRATION'
WHERE code IN ('ECB_DAILY_FX')
  AND status = 'ACTIVE'
  AND deleted_at IS NULL;
