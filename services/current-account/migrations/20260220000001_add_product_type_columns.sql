-- Add product_type_code and product_type_version to account table.
-- These are immutable after account creation and reference the Product Directory.
-- NULL during transition period for accounts created before product type migration.

ALTER TABLE "account" ADD COLUMN "product_type_code" VARCHAR(50) NULL;
ALTER TABLE "account" ADD COLUMN "product_type_version" INT NULL;
