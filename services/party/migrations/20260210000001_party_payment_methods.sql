-- Party Payment Methods table for storing tokenized payment method references from Stripe
-- Tracks payment methods (cards, bank accounts, SEPA) linked to parties via external providers
-- Uses unqualified table names (relies on search_path for tenant isolation)

-- Create "party_payment_method" table (singular, unqualified - uses search_path for schema routing)
CREATE TABLE "party_payment_method" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "party_id" uuid NOT NULL,
    "provider" character varying(50) NOT NULL,
    "provider_customer_id" character varying(255) NOT NULL,
    "provider_method_id" character varying(255) NOT NULL,
    "method_type" character varying(50) NOT NULL,
    "is_default" boolean NOT NULL DEFAULT false,
    "metadata" jsonb NULL DEFAULT '{}',
    "status" character varying(20) NOT NULL DEFAULT 'ACTIVE',
    "version" bigint NOT NULL DEFAULT 1,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "fk_party_payment_method_party" FOREIGN KEY ("party_id")
        REFERENCES "party" ("id") ON DELETE CASCADE,
    CONSTRAINT "chk_party_payment_method_provider" CHECK ("provider" IN ('STRIPE')),
    CONSTRAINT "chk_party_payment_method_status" CHECK ("status" IN ('ACTIVE', 'EXPIRED', 'REMOVED'))
);

-- Index on party_id for listing active payment methods by party
CREATE INDEX "idx_party_payment_method_party" ON "party_payment_method" ("party_id")
    WHERE "status" = 'ACTIVE';

-- Unique index on provider + provider_method_id for active methods
-- Prevents duplicate tokenized references from the same provider
CREATE UNIQUE INDEX "idx_party_payment_method_provider_method" ON "party_payment_method" ("provider", "provider_method_id")
    WHERE "status" = 'ACTIVE';

-- Unique partial index ensuring at most one default payment method per party among active methods
CREATE UNIQUE INDEX "idx_party_payment_method_default" ON "party_payment_method" ("party_id")
    WHERE "is_default" = true AND "status" = 'ACTIVE';
