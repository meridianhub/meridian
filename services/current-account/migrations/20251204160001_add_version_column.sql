-- Add version column for optimistic locking
-- See GitHub Issue #206 for context

ALTER TABLE "current_account"."accounts"
ADD COLUMN "version" bigint NOT NULL DEFAULT 1;
