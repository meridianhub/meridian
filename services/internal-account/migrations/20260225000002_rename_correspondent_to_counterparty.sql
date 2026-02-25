-- Rename correspondent bank columns to counterparty columns
-- Part of the asset-agnostic accounts initiative to use generic counterparty terminology

ALTER TABLE "internal_account" RENAME COLUMN "correspondent_bank_id" TO "counterparty_id";
ALTER TABLE "internal_account" RENAME COLUMN "correspondent_bank_name" TO "counterparty_name";
ALTER TABLE "internal_account" RENAME COLUMN "correspondent_external_ref" TO "counterparty_external_ref";
