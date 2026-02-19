-- Partial unique index ensuring only one ACTIVE account type definition per code.
-- Separated from the table creation migration per CockroachDB requirement:
-- partial indexes on a column require the table to be fully committed ("public") first.

CREATE UNIQUE INDEX "uq_active_account_type_code"
  ON "account_type_definitions" ("code")
  WHERE "status" = 'ACTIVE';
