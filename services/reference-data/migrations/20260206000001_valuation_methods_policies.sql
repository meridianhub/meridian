-- Valuation Methods and Valuation Policies tables
-- ValuationMethods store Starlark procedures for converting between instruments
-- ValuationPolicies store named CEL expressions used by valuation methods
-- Both support bi-temporal queries and SYSTEM defaults with tenant fallback

-- Create "valuation_method" table
CREATE TABLE "valuation_method" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "name" character varying(64) NOT NULL,
  "version" integer NOT NULL,
  "input_instrument" character varying(32) NOT NULL,
  "output_instrument" character varying(32) NOT NULL,
  "logic_script" text NOT NULL,
  "logic_hash" character varying(64) NOT NULL,
  "required_policies" text[] NOT NULL DEFAULT '{}',
  "lifecycle_status" character varying(16) NOT NULL DEFAULT 'INITIATED',
  "is_system" boolean NOT NULL DEFAULT false,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  "valid_from" timestamptz NOT NULL DEFAULT now(),
  "valid_to" timestamptz NULL,
  PRIMARY KEY ("id")
);

-- Constraints
ALTER TABLE "valuation_method"
  ADD CONSTRAINT "chk_valuation_method_lifecycle_status"
  CHECK ("lifecycle_status" IN ('INITIATED', 'ACTIVE', 'DEPRECATED'));

ALTER TABLE "valuation_method"
  ADD CONSTRAINT "uq_valuation_method_name_version"
  UNIQUE ("name", "version");

-- Indexes
CREATE INDEX "idx_valuation_method_instruments_status"
  ON "valuation_method" ("input_instrument", "output_instrument", "lifecycle_status");

CREATE INDEX "idx_valuation_method_name_temporal"
  ON "valuation_method" ("name", "valid_from", "valid_to");

-- Create "valuation_policy" table
CREATE TABLE "valuation_policy" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "name" character varying(64) NOT NULL,
  "version" integer NOT NULL,
  "cel_expression" text NOT NULL,
  "cel_hash" character varying(64) NOT NULL,
  "input_schema" jsonb NULL,
  "output_type" character varying(32) NOT NULL DEFAULT '',
  "estimated_cost" integer NOT NULL DEFAULT 1,
  "lifecycle_status" character varying(16) NOT NULL DEFAULT 'INITIATED',
  "is_system" boolean NOT NULL DEFAULT false,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  "valid_from" timestamptz NOT NULL DEFAULT now(),
  "valid_to" timestamptz NULL,
  PRIMARY KEY ("id")
);

-- Constraints
ALTER TABLE "valuation_policy"
  ADD CONSTRAINT "chk_valuation_policy_lifecycle_status"
  CHECK ("lifecycle_status" IN ('INITIATED', 'ACTIVE', 'DEPRECATED'));

ALTER TABLE "valuation_policy"
  ADD CONSTRAINT "chk_valuation_policy_estimated_cost"
  CHECK ("estimated_cost" > 0 AND "estimated_cost" < 10000);

ALTER TABLE "valuation_policy"
  ADD CONSTRAINT "uq_valuation_policy_name_version"
  UNIQUE ("name", "version");

-- Indexes
CREATE INDEX "idx_valuation_policy_name_status"
  ON "valuation_policy" ("name", "lifecycle_status");

CREATE INDEX "idx_valuation_policy_name_temporal"
  ON "valuation_policy" ("name", "valid_from", "valid_to");
