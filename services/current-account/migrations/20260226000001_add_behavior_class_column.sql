-- Add behavior_class column to account table.
-- Derived from product_type_code at creation time and stored for query efficiency.
-- NULL for legacy accounts created before Product Directory integration.

ALTER TABLE "account" ADD COLUMN "behavior_class" VARCHAR(50) NULL;
