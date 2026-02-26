-- Backfill behavior_class for existing accounts that have a product_type_code.
-- Only accounts with a known product_type_code are backfilled to 'CUSTOMER'.
-- Legacy accounts (product_type_code IS NULL) retain NULL behavior_class, preserving
-- backward compatibility and the domain invariant that behavior_class is empty for
-- accounts created before Product Directory integration.

UPDATE "account"
SET "behavior_class" = 'CUSTOMER'
WHERE "behavior_class" IS NULL
  AND "product_type_code" IS NOT NULL;
