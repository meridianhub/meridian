-- Backfill behavior_class for existing accounts.
-- All existing accounts (account_type = 'current' or 'savings') are customer-facing accounts.
-- New accounts with a product_type_code will have behavior_class set at creation time.

UPDATE "account"
SET "behavior_class" = 'CUSTOMER'
WHERE "behavior_class" IS NULL;
