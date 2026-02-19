-- AccountTypeDefinitions: versioned registry of account type schemas
-- Reference data is global per tenant (schema-per-tenant architecture, no tenant_id column)
-- Supports DRAFT → ACTIVE → DEPRECATED lifecycle with successor chaining for versioning

CREATE TABLE "account_type_definitions" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "display_name" character varying(255) NOT NULL,
  "description" text NULL,
  "normal_balance" character varying(10) NOT NULL,
  "behavior_class" character varying(20) NOT NULL,
  "instrument_code" character varying(50) NOT NULL,
  "default_saga_prefix" character varying(100) NULL,
  "default_conversion_method_id" uuid NULL,
  "default_conversion_method_version" integer NULL,
  "validation_cel" text NULL,
  "bucketing_cel" text NULL,
  "eligibility_cel" text NULL,
  "attribute_schema" jsonb NULL,
  "attributes" jsonb NOT NULL DEFAULT '{}',
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "is_system" boolean NOT NULL DEFAULT false,
  "successor_id" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_account_type_code_version"
    UNIQUE ("code", "version"),
  CONSTRAINT "fk_account_type_successor"
    FOREIGN KEY ("successor_id") REFERENCES "account_type_definitions" ("id") ON DELETE SET NULL,
  CONSTRAINT "chk_account_type_normal_balance"
    CHECK ("normal_balance" IN ('DEBIT', 'CREDIT')),
  CONSTRAINT "chk_account_type_behavior_class"
    CHECK ("behavior_class" IN ('CUSTOMER', 'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING', 'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY')),
  CONSTRAINT "chk_account_type_status"
    CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
  CONSTRAINT "chk_acct_type_successor_not_self"
    CHECK ("successor_id" != "id"),
  CONSTRAINT "chk_default_conversion_method_pair"
    CHECK (("default_conversion_method_id" IS NULL) = ("default_conversion_method_version" IS NULL))
);

-- Index for listing account types by status
CREATE INDEX "idx_account_type_status" ON "account_type_definitions" ("status");

-- Index for looking up all versions of a code
CREATE INDEX "idx_account_type_code" ON "account_type_definitions" ("code");

-- NOTE: Lifecycle enforcement (status transitions, immutable fields, timestamp management)
-- is handled at the application layer. CockroachDB does not support PL/pgSQL triggers.
-- CHECK constraints above provide database-level safety.
-- Partial unique index (only one ACTIVE version per code) is in a separate migration
-- per CockroachDB requirement: partial indexes on a table require the table to be fully committed first.
