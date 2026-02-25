-- Rename internal_bank_account to internal_account
-- Part of the asset-agnostic accounts initiative to remove bank-specific terminology

-- Rename main table
ALTER TABLE "internal_bank_account" RENAME TO "internal_account";

-- Rename status history table
ALTER TABLE "internal_bank_account_status_history" RENAME TO "internal_account_status_history";

-- Rename indexes on main table
ALTER INDEX "idx_internal_bank_account_account_id" RENAME TO "idx_internal_account_account_id";
ALTER INDEX "idx_internal_bank_account_type" RENAME TO "idx_internal_account_type";
ALTER INDEX "idx_internal_bank_account_instrument" RENAME TO "idx_internal_account_instrument";
ALTER INDEX "idx_internal_bank_account_status" RENAME TO "idx_internal_account_status";
ALTER INDEX "idx_internal_bank_account_code" RENAME TO "idx_internal_account_code";
ALTER INDEX "idx_internal_bank_account_deleted_at" RENAME TO "idx_internal_account_deleted_at";
ALTER INDEX "idx_internal_bank_account_type_instrument" RENAME TO "idx_internal_account_type_instrument";
ALTER INDEX "idx_internal_bank_account_clearing_purpose" RENAME TO "idx_internal_account_clearing_purpose";
ALTER INDEX "idx_internal_bank_account_org_party" RENAME TO "idx_internal_account_org_party";
ALTER INDEX "idx_internal_bank_account_product_type_code" RENAME TO "idx_internal_account_product_type_code";

-- Rename foreign key constraints
ALTER TABLE "lien" RENAME CONSTRAINT "fk_lien_internal_bank_account" TO "fk_lien_internal_account";
ALTER TABLE "valuation_features" RENAME CONSTRAINT "fk_valuation_feature_internal_bank_account" TO "fk_valuation_feature_internal_account";

-- Update table comments
COMMENT ON TABLE "internal_account" IS
  'Internal accounts for counterparty and operational accounting. Balance computed externally by Position Keeping service.';
