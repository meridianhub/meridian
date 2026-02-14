-- Add partial index for platform_ref lookups (split from 20260125000002)
-- CockroachDB requires the column to be committed before a partial
-- index can reference it, so this must be a separate migration.
CREATE INDEX "idx_saga_definition_platform_ref" ON "saga_definition" ("platform_ref")
  WHERE "platform_ref" IS NOT NULL;
