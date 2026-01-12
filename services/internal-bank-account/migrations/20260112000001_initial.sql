-- Internal Bank Account Service Schema
-- BIAN Service Domain: Internal Bank Account
-- Manages counterparty and operational accounts for internal accounting
-- Uses unqualified table names (database-per-service architecture)

-- Create "internal_bank_account" table (singular, unqualified)
-- Note: NO balance columns - balance computed by Position Keeping service
CREATE TABLE "internal_bank_account" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,

  -- Account identifiers
  "account_id" character varying(100) NOT NULL,
  "account_code" character varying(50) NOT NULL,
  "name" character varying(255) NOT NULL,

  -- Account classification
  "account_type" character varying(20) NOT NULL,
  "instrument_code" character varying(32) NOT NULL,
  "dimension" character varying(20) NOT NULL,

  -- Account status
  "status" character varying(20) NOT NULL DEFAULT 'ACTIVE',

  -- Correspondent bank details (nullable for non-nostro/vostro accounts)
  "correspondent_bank_id" character varying(50) NULL,
  "correspondent_bank_name" character varying(255) NULL,
  "correspondent_external_ref" character varying(100) NULL,

  -- Extensible attributes
  "attributes" jsonb NOT NULL DEFAULT '{}',

  -- Optimistic locking
  "version" bigint NOT NULL DEFAULT 1,

  PRIMARY KEY ("id"),

  -- Constraints
  CONSTRAINT "chk_account_type" CHECK (account_type IN (
    'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
    'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY'
  )),
  CONSTRAINT "chk_dimension" CHECK (dimension IN (
    'CURRENCY', 'ENERGY', 'MASS', 'VOLUME', 'TIME',
    'COMPUTE', 'CARBON', 'DATA', 'COUNT'
  )),
  CONSTRAINT "chk_status" CHECK (status IN (
    'ACTIVE', 'SUSPENDED', 'CLOSED'
  ))
);

-- Unique constraints
CREATE UNIQUE INDEX "idx_internal_bank_account_account_id" ON "internal_bank_account" ("account_id");

-- Query optimization indexes
CREATE INDEX "idx_internal_bank_account_type" ON "internal_bank_account" ("account_type");
CREATE INDEX "idx_internal_bank_account_instrument" ON "internal_bank_account" ("instrument_code");
CREATE INDEX "idx_internal_bank_account_status" ON "internal_bank_account" ("status");
CREATE INDEX "idx_internal_bank_account_code" ON "internal_bank_account" ("account_code");
CREATE INDEX "idx_internal_bank_account_deleted_at" ON "internal_bank_account" ("deleted_at");

-- Composite index for common query patterns (e.g., "find all NOSTRO accounts for USD")
CREATE INDEX "idx_internal_bank_account_type_instrument" ON "internal_bank_account" ("account_type", "instrument_code");

-- Create "internal_bank_account_status_history" table for audit trail
CREATE TABLE "internal_bank_account_status_history" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "account_id" character varying(100) NOT NULL,
  "from_status" character varying(20) NOT NULL,
  "to_status" character varying(20) NOT NULL,
  "reason" text NULL,
  "changed_by" character varying(100) NOT NULL,
  "changed_at" timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY ("id"),

  -- Status value constraints (matching main table)
  CONSTRAINT "chk_from_status" CHECK (from_status IN (
    'ACTIVE', 'SUSPENDED', 'CLOSED'
  )),
  CONSTRAINT "chk_to_status" CHECK (to_status IN (
    'ACTIVE', 'SUSPENDED', 'CLOSED'
  )),

  -- Foreign key to internal_bank_account via account_id
  CONSTRAINT "fk_status_history_account" FOREIGN KEY ("account_id")
    REFERENCES "internal_bank_account" ("account_id")
    ON UPDATE NO ACTION ON DELETE RESTRICT
);

-- Indexes for status history queries
-- Composite index optimized for audit log queries (filter by account, sort by time DESC)
CREATE INDEX "idx_status_history_account_changed" ON "internal_bank_account_status_history" ("account_id", "changed_at" DESC);
CREATE INDEX "idx_status_history_changed_at" ON "internal_bank_account_status_history" ("changed_at");

-- Comments
COMMENT ON TABLE "internal_bank_account" IS
  'Internal bank accounts for counterparty and operational accounting. Balance computed externally by Position Keeping service.';

COMMENT ON COLUMN "internal_bank_account"."account_type" IS
  'Account classification: CLEARING (settlement), NOSTRO (our account at another bank), VOSTRO (their account at our bank), HOLDING (custody), SUSPENSE (temporary), REVENUE/EXPENSE (P&L), INVENTORY (physical goods)';

COMMENT ON COLUMN "internal_bank_account"."dimension" IS
  'Unit dimension for the instrument: CURRENCY (fiat/crypto), ENERGY (kWh), MASS (kg), VOLUME (liters), TIME (hours), COMPUTE (GPU-hours), CARBON (tonnes CO2), DATA (bytes), COUNT (units)';

COMMENT ON COLUMN "internal_bank_account"."attributes" IS
  'Extensible JSONB field for custom attributes like cost centers, regulatory flags, or integration metadata';
