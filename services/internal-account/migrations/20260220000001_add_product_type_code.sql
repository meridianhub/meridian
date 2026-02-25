-- Add product_type_code and product_type_version columns to internal_bank_account.
-- These columns link accounts to the Product Directory's AccountTypeRegistry.
-- product_type_code is nullable during migration; existing accounts retain
-- their account_type enum value until backfilled.

ALTER TABLE "internal_bank_account"
  ADD COLUMN "product_type_code" character varying(100) NULL,
  ADD COLUMN "product_type_version" integer NULL;
