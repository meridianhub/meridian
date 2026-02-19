-- Partial unique index ensuring only one ACTIVE valuation method per (account_type_id, input_instrument).
-- Separated from the table creation migration per CockroachDB requirement:
-- partial indexes on a column require the table to be fully committed ("public") first.

CREATE UNIQUE INDEX "uq_active_valuation_method"
  ON "account_type_valuation_methods" ("account_type_id", "input_instrument")
  WHERE "status" = 'ACTIVE';
