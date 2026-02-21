-- Partial unique index: only one ACTIVE version per (tenant_id, name).
-- Separated into its own migration per CockroachDB requirement:
-- partial indexes on a newly added column/table require the table to be
-- fully committed ("public") before the partial index can reference it.

CREATE UNIQUE INDEX "uq_mapping_tenant_name_active"
  ON "mapping_definition" ("tenant_id", "name")
  WHERE "status" = 'ACTIVE';
