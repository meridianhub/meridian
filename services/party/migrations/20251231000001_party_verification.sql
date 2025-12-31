-- Party Verification table for KYC/AML verification tracking
-- Tracks async verification requests and results from external providers
-- Uses unqualified table names (relies on search_path for tenant isolation)

-- Create verification status enum type
-- Matches verification.Status constants in verification/provider.go
CREATE TYPE verification_status AS ENUM (
    'PENDING',
    'APPROVED',
    'REJECTED',
    'MANUAL_REVIEW'
);

-- Create "party_verification" table (singular, unqualified - uses search_path for schema routing)
CREATE TABLE "party_verification" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "party_id" uuid NOT NULL,
    "verification_id" character varying(255) NOT NULL,
    "provider" character varying(100) NOT NULL,
    "status" verification_status NOT NULL DEFAULT 'PENDING',
    "risk_score" decimal(5,4) NULL,
    "reason" text NULL,
    "completed_at" timestamptz NULL,
    "metadata" jsonb NULL DEFAULT '{}',
    "version" bigint NOT NULL DEFAULT 1,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "fk_party_verification_party" FOREIGN KEY ("party_id")
        REFERENCES "party" ("id") ON DELETE CASCADE
);

-- Create indexes for party_verification
-- Index on party_id for listing verifications by party
CREATE INDEX "idx_party_verification_party_id" ON "party_verification" ("party_id");

-- Unique index on verification_id (provider's external ID) - prevents duplicate tracking
CREATE UNIQUE INDEX "idx_party_verification_verification_id" ON "party_verification" ("verification_id");

-- Index on status for querying pending/in-progress verifications
CREATE INDEX "idx_party_verification_status" ON "party_verification" ("status");

-- Index on created_at for chronological ordering
CREATE INDEX "idx_party_verification_created_at" ON "party_verification" ("created_at");

-- Composite index for common query pattern: party + status
CREATE INDEX "idx_party_verification_party_status" ON "party_verification" ("party_id", "status");
