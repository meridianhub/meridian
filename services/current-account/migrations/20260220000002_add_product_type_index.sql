-- Index on product_type_code for querying accounts by product type.
-- Separate migration from column addition per CockroachDB requirement:
-- columns must be "public" before being referenced in indexes.

CREATE INDEX "idx_account_product_type_code" ON "account" ("product_type_code");
