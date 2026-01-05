-- Add bucket_id column to lien table for bucket-aware fund reservations
-- The bucket_id represents the fungibility key / bucket identifier computed from the instrument's CEL expression
-- Phase 1: Single-bucket liens only (no multi-bucket lien support)

-- Add bucket_id column with default empty string for backward compatibility with existing rows
ALTER TABLE "lien" ADD COLUMN "bucket_id" character varying(255) NOT NULL DEFAULT '';

-- Create index for queries filtering by account and bucket
CREATE INDEX "idx_lien_account_bucket" ON "lien" ("account_id", "bucket_id");
