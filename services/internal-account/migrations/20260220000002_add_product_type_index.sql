-- Index on product_type_code for lookups by product type.
-- Separate migration from column creation per CockroachDB requirements
-- (column must be "public" before it can be referenced in an index).

CREATE INDEX "idx_internal_bank_account_product_type_code"
  ON "internal_bank_account" ("product_type_code");
