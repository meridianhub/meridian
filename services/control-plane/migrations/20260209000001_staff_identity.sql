-- Staff Identity Registry
-- Uses UNQUALIFIED table names to support multi-organization routing via search_path.
-- Tables are created in tenant schemas (org_{id}) during provisioning.

-- =============================================================================
-- STAFF USERS
-- =============================================================================

CREATE TABLE "staff_user" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "email" VARCHAR(255) NOT NULL,
    "name" VARCHAR(255),
    "role" VARCHAR(50) NOT NULL DEFAULT 'operator',
    "status" VARCHAR(20) NOT NULL DEFAULT 'invited',
    "auth_provider_id" VARCHAR(255),
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_role CHECK ("role" IN ('admin', 'operator', 'auditor')),
    CONSTRAINT valid_status CHECK ("status" IN ('invited', 'active', 'suspended'))
);

CREATE UNIQUE INDEX "idx_staff_user_email" ON "staff_user" ("email");
CREATE INDEX "idx_staff_user_status" ON "staff_user" ("status");
CREATE INDEX "idx_staff_user_role" ON "staff_user" ("role");

COMMENT ON TABLE "staff_user" IS 'Staff users who operate the Admin Console, distinct from Party (customers with ledger positions)';
COMMENT ON COLUMN "staff_user"."role" IS 'Authorization role: admin (full access), operator (operational), auditor (read-only)';
COMMENT ON COLUMN "staff_user"."status" IS 'Lifecycle: invited (pending activation), active (can authenticate), suspended (access revoked)';
COMMENT ON COLUMN "staff_user"."auth_provider_id" IS 'External identity provider user ID (e.g., Auth0 sub claim)';

-- =============================================================================
-- API KEYS
-- =============================================================================

CREATE TABLE "api_key" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "staff_user_id" UUID NOT NULL REFERENCES "staff_user" ("id") ON DELETE RESTRICT,
    "key_prefix" VARCHAR(100) NOT NULL,
    "key_hash" BYTEA NOT NULL,
    "name" VARCHAR(255),
    "scopes" TEXT[],
    "rate_limit_rps" INTEGER NOT NULL DEFAULT 100,
    "last_used_at" TIMESTAMPTZ,
    "expires_at" TIMESTAMPTZ,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "revoked_at" TIMESTAMPTZ
);

CREATE UNIQUE INDEX "idx_api_key_prefix" ON "api_key" ("key_prefix") WHERE "revoked_at" IS NULL;
CREATE INDEX "idx_api_key_staff_user_id" ON "api_key" ("staff_user_id");
CREATE INDEX "idx_api_key_expires_at" ON "api_key" ("expires_at") WHERE "revoked_at" IS NULL AND "expires_at" IS NOT NULL;

COMMENT ON TABLE "api_key" IS 'API keys for programmatic access, scoped to staff users';
COMMENT ON COLUMN "api_key"."key_prefix" IS 'Prefix format: pk_{tenant_slug}_{first8chars} - enables O(1) routing to correct tenant schema';
COMMENT ON COLUMN "api_key"."key_hash" IS 'SHA-256 hash of the full API key (high-entropy keys do not need argon2id)';
COMMENT ON COLUMN "api_key"."scopes" IS 'Permission scopes for this key (empty = full access for the staff user role)';
COMMENT ON COLUMN "api_key"."rate_limit_rps" IS 'Per-key rate limit in requests per second';
