-- AccountTypeValuationMethods: maps account types to valuation methods per input instrument
-- Supports DRAFT → ACTIVE → DEPRECATED lifecycle with successor chaining
-- Each active (account_type_id, input_instrument) pair must map to exactly one valuation method

CREATE TABLE "account_type_valuation_methods" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "account_type_id" uuid NOT NULL,
  "input_instrument" character varying(50) NOT NULL,
  "valuation_method_id" uuid NOT NULL,
  "valuation_method_version" integer NOT NULL DEFAULT 1,
  "parameters" jsonb NOT NULL DEFAULT '{}',
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "successor_id" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_account_type_val_method_account_type"
    FOREIGN KEY ("account_type_id") REFERENCES "account_type_definitions" ("id"),
  CONSTRAINT "fk_account_type_val_method_successor"
    FOREIGN KEY ("successor_id") REFERENCES "account_type_valuation_methods" ("id") ON DELETE SET NULL,
  CONSTRAINT "chk_account_type_val_method_status"
    CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
  CONSTRAINT "chk_val_method_successor_not_self"
    CHECK ("successor_id" != "id")
);

-- Index for looking up valuation methods by account type
CREATE INDEX "idx_val_method_account_type" ON "account_type_valuation_methods" ("account_type_id");

-- NOTE: Partial unique index (only one ACTIVE mapping per account_type_id, input_instrument)
-- is in a separate migration per CockroachDB requirement.
