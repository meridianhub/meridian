-- Add tenant_id column to identity and role_assignment tables for row-level tenant isolation.
-- This supplements the existing schema-based isolation (search_path routing).

ALTER TABLE "identity" ADD COLUMN "tenant_id" VARCHAR(50) NOT NULL DEFAULT '';

ALTER TABLE "role_assignment" ADD COLUMN "tenant_id" VARCHAR(50) NOT NULL DEFAULT '';
