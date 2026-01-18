-- Market Information Service Schema
-- BIAN Service Domain: Market Information Management
-- Manages price benchmarks, market data feeds, and reference prices with bi-temporal support
-- Uses database-per-service architecture with unqualified table names
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed

--------------------------------------------------------------------------------
-- Section 1: Data Source Table
-- Represents external/internal sources of market data with trust levels
--------------------------------------------------------------------------------

CREATE TABLE "data_source" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "trust_level" integer NOT NULL DEFAULT 50,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

-- Code must be unique
ALTER TABLE "data_source"
  ADD CONSTRAINT "uq_data_source_code"
  UNIQUE ("code");

-- Trust level must be 0-100 (0=untrusted, 100=authoritative)
ALTER TABLE "data_source"
  ADD CONSTRAINT "chk_data_source_trust_level"
  CHECK ("trust_level" >= 0 AND "trust_level" <= 100);

-- Index for lookups by trust level
CREATE INDEX "idx_data_source_trust_level" ON "data_source" ("trust_level" DESC);

--------------------------------------------------------------------------------
-- Section 2: Dataset Definition Table
-- Defines types of market data with CEL validation expressions and versioning
--------------------------------------------------------------------------------

CREATE TABLE "dataset_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "data_category" character varying(50) NULL,
  "validation_expression" text NULL,
  "resolution_key_expression" text NOT NULL,
  "error_message_expression" text NULL,
  "attribute_schema" jsonb NULL,
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);

-- Status validation
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_status"
  CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED'));

-- Limit expression columns to 4KB to prevent abuse
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_validation_expression_length"
  CHECK ("validation_expression" IS NULL OR length("validation_expression") <= 4096);

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_resolution_key_expression_length"
  CHECK (length("resolution_key_expression") <= 4096);

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_error_message_length"
  CHECK ("error_message_expression" IS NULL OR length("error_message_expression") <= 4096);

-- Ensure code+version is unique (allows multiple versions of same dataset)
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "uq_dataset_definition_code_version"
  UNIQUE ("code", "version");

-- Partial index for quickly finding active datasets by code
CREATE INDEX "idx_dataset_definition_code_active" ON "dataset_definition" ("code") WHERE "status" = 'ACTIVE';

-- Index for listing datasets by status
CREATE INDEX "idx_dataset_definition_status" ON "dataset_definition" ("status");

-- Index for temporal queries
CREATE INDEX "idx_dataset_definition_created_at" ON "dataset_definition" ("created_at");

-- Trigger function to enforce dataset lifecycle rules
-- Immutable fields cannot be changed once dataset is ACTIVE or DEPRECATED
-- Status transitions are strictly controlled
CREATE OR REPLACE FUNCTION "enforce_dataset_lifecycle"()
RETURNS TRIGGER AS $$
BEGIN
  -- Always update updated_at on any change
  NEW."updated_at" = NOW();

  -- Allow all edits when status is DRAFT
  IF OLD."status" = 'DRAFT' THEN
    -- If transitioning from DRAFT to ACTIVE, set activated_at
    IF NEW."status" = 'ACTIVE' THEN
      NEW."activated_at" = NOW();
    -- If transitioning from DRAFT to DEPRECATED, set deprecated_at
    ELSIF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
    RETURN NEW;
  END IF;

  -- For ACTIVE or DEPRECATED datasets, certain fields become immutable
  IF OLD."status" IN ('ACTIVE', 'DEPRECATED') THEN
    -- Prevent changes to immutable fields (validation rules that affect data integrity)
    IF OLD."validation_expression" IS DISTINCT FROM NEW."validation_expression" THEN
      RAISE EXCEPTION 'Cannot modify validation_expression for % dataset', OLD."status";
    END IF;
    IF OLD."resolution_key_expression" IS DISTINCT FROM NEW."resolution_key_expression" THEN
      RAISE EXCEPTION 'Cannot modify resolution_key_expression for % dataset', OLD."status";
    END IF;
    IF OLD."error_message_expression" IS DISTINCT FROM NEW."error_message_expression" THEN
      RAISE EXCEPTION 'Cannot modify error_message_expression for % dataset', OLD."status";
    END IF;
  END IF;

  -- Prevent invalid status transitions
  IF OLD."status" = 'ACTIVE' THEN
    IF NEW."status" = 'DRAFT' THEN
      RAISE EXCEPTION 'Cannot transition from ACTIVE back to DRAFT';
    END IF;
    -- Allow ACTIVE to DEPRECATED, set deprecated_at
    IF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
  END IF;

  IF OLD."status" = 'DEPRECATED' THEN
    IF NEW."status" IN ('DRAFT', 'ACTIVE') THEN
      RAISE EXCEPTION 'Cannot transition from DEPRECATED to %', NEW."status";
    END IF;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to enforce lifecycle rules on UPDATE
CREATE TRIGGER "trg_enforce_dataset_lifecycle"
  BEFORE UPDATE ON "dataset_definition"
  FOR EACH ROW
  EXECUTE FUNCTION "enforce_dataset_lifecycle"();

--------------------------------------------------------------------------------
-- Section 3: Market Price Observation Table
-- Bi-temporal table storing actual market data points with quality ladder
-- Event time: observed_at (when the event occurred in the real world)
-- Knowledge time: created_at (when we learned about it)
--------------------------------------------------------------------------------

CREATE TABLE "market_price_observation" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "dataset_definition_id" uuid NOT NULL,
  "data_source_id" uuid NOT NULL,
  "resolution_key" character varying(255) NOT NULL,
  "observed_at" timestamptz NOT NULL,
  -- valid_from/valid_to: Reserved for state-based temporal validity (e.g., "this rate was
  -- valid from 2026-01-01 to 2026-01-31"). Distinct from bi-temporal observed_at/created_at.
  -- Enables queries like "what rate applied on date X?" vs "what did we know at time T?"
  "valid_from" timestamptz NULL,
  "valid_to" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "quality" integer NOT NULL,
  "observation_context" jsonb NOT NULL,
  "numeric_value" numeric NULL,
  "text_value" text NULL,
  "superseded_by" uuid NULL,
  "causation_id" uuid NULL,
  PRIMARY KEY ("id")
);

-- Foreign key to dataset definition (RESTRICT: prevent deletion of referenced definitions)
ALTER TABLE "market_price_observation"
  ADD CONSTRAINT "fk_observation_dataset_definition"
  FOREIGN KEY ("dataset_definition_id") REFERENCES "dataset_definition"("id") ON DELETE RESTRICT;

-- Foreign key to data source (RESTRICT: prevent deletion of referenced sources)
ALTER TABLE "market_price_observation"
  ADD CONSTRAINT "fk_observation_data_source"
  FOREIGN KEY ("data_source_id") REFERENCES "data_source"("id") ON DELETE RESTRICT;

-- Self-referencing FK for supersession chain (SET NULL: allow deletion without breaking chain)
ALTER TABLE "market_price_observation"
  ADD CONSTRAINT "fk_observation_superseded_by"
  FOREIGN KEY ("superseded_by") REFERENCES "market_price_observation"("id") ON DELETE SET NULL;

-- Quality must be 1 (ESTIMATE), 2 (ACTUAL), or 3 (VERIFIED)
ALTER TABLE "market_price_observation"
  ADD CONSTRAINT "chk_observation_quality"
  CHECK ("quality" IN (1, 2, 3));

-- At least one value must be present
ALTER TABLE "market_price_observation"
  ADD CONSTRAINT "chk_observation_value_present"
  CHECK ("numeric_value" IS NOT NULL OR "text_value" IS NOT NULL);

--------------------------------------------------------------------------------
-- Section 4: Bi-Temporal Indexes
-- Critical for efficient point-in-time queries with quality precedence
--------------------------------------------------------------------------------

-- CRITICAL: Primary bi-temporal query index
-- Enables efficient queries like "What was the best known value for EUR/USD at time T?"
-- Quality DESC ensures higher quality (VERIFIED > ACTUAL > ESTIMATE) is preferred
-- WHERE superseded_by IS NULL filters to only current knowledge
CREATE INDEX "idx_observation_resolution_bitemporal"
  ON "market_price_observation" ("resolution_key", "quality" DESC, "observed_at" DESC, "created_at" DESC)
  WHERE "superseded_by" IS NULL;

-- Fast queries by dataset
CREATE INDEX "idx_observation_dataset"
  ON "market_price_observation" ("dataset_definition_id", "observed_at" DESC);

-- Source audit queries
CREATE INDEX "idx_observation_source"
  ON "market_price_observation" ("data_source_id", "created_at" DESC);

-- Knowledge time queries for audit replay
CREATE INDEX "idx_observation_created_at"
  ON "market_price_observation" ("created_at" DESC)
  WHERE "superseded_by" IS NULL;

-- Traverse supersession chains
CREATE INDEX "idx_observation_superseded_by"
  ON "market_price_observation" ("superseded_by")
  WHERE "superseded_by" IS NOT NULL;

-- Causation tracking for data lineage
CREATE INDEX "idx_observation_causation"
  ON "market_price_observation" ("causation_id")
  WHERE "causation_id" IS NOT NULL;

--------------------------------------------------------------------------------
-- Section 5: Seed Data
-- System data sources and initial dataset definitions
--------------------------------------------------------------------------------

-- Insert system data sources
INSERT INTO "data_source" ("id", "code", "name", "description", "trust_level") VALUES
  (gen_random_uuid(), 'ECB_DAILY', 'ECB Daily Rates', 'European Central Bank daily reference rates', 90),
  (gen_random_uuid(), 'INTERNAL_ADMIN', 'Internal Admin', 'Manual administrative overrides', 100),
  (gen_random_uuid(), 'SYSTEM_DEFAULT', 'System Defaults', 'Fallback rates when no other source available', 50);

-- Insert dataset definitions with CEL expressions
-- FX_RATE: Foreign exchange rates
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "activated_at"
) VALUES (
  gen_random_uuid(),
  'FX_RATE', 1,
  'Foreign Exchange Rate',
  'Exchange rates between currency pairs',
  'PRICE',
  'parse_decimal(observation_context.rate) > 0',
  'observation_context.base_currency + "/" + observation_context.quote_currency',
  '"Invalid exchange rate: must be positive"',
  '{"type":"object","properties":{"base_currency":{"type":"string","minLength":3,"maxLength":3},"quote_currency":{"type":"string","minLength":3,"maxLength":3},"rate":{"type":"number","exclusiveMinimum":0}},"required":["base_currency","quote_currency","rate"]}',
  'ACTIVE',
  now()
);

-- ENERGY_SPOT: Energy spot prices
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "activated_at"
) VALUES (
  gen_random_uuid(),
  'ENERGY_SPOT', 1,
  'Energy Spot Price',
  'Spot prices for energy commodities (electricity, gas, etc.)',
  'PRICE',
  'parse_decimal(observation_context.price) >= 0',
  'observation_context.market + "/" + observation_context.commodity + "/" + observation_context.delivery_period',
  '"Invalid energy spot price: must be non-negative"',
  '{"type":"object","properties":{"market":{"type":"string"},"commodity":{"type":"string","enum":["ELECTRICITY","NATURAL_GAS","COAL","OIL"]},"delivery_period":{"type":"string"},"price":{"type":"number","minimum":0},"unit":{"type":"string"}},"required":["market","commodity","delivery_period","price"]}',
  'ACTIVE',
  now()
);

-- ENERGY_TARIFF: Energy tariff rates
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "activated_at"
) VALUES (
  gen_random_uuid(),
  'ENERGY_TARIFF', 1,
  'Energy Tariff Rate',
  'Published tariff rates for energy consumption',
  'RATE',
  'parse_decimal(observation_context.rate) >= 0',
  'observation_context.provider + "/" + observation_context.tariff_code + "/" + observation_context.effective_date',
  '"Invalid tariff rate: must be non-negative"',
  '{"type":"object","properties":{"provider":{"type":"string"},"tariff_code":{"type":"string"},"effective_date":{"type":"string","format":"date"},"rate":{"type":"number","minimum":0},"unit":{"type":"string"}},"required":["provider","tariff_code","effective_date","rate"]}',
  'ACTIVE',
  now()
);

-- CARBON_PRICE: Carbon credit prices
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "activated_at"
) VALUES (
  gen_random_uuid(),
  'CARBON_PRICE', 1,
  'Carbon Credit Price',
  'Prices for carbon credits and emission allowances',
  'PRICE',
  'parse_decimal(observation_context.price) >= 0',
  'observation_context.scheme + "/" + observation_context.credit_type + "/" + observation_context.vintage',
  '"Invalid carbon price: must be non-negative"',
  '{"type":"object","properties":{"scheme":{"type":"string","enum":["EU_ETS","VCS","GOLD_STANDARD","CDM"]},"credit_type":{"type":"string"},"vintage":{"type":"integer","minimum":2000},"price":{"type":"number","minimum":0},"currency":{"type":"string"}},"required":["scheme","credit_type","vintage","price"]}',
  'ACTIVE',
  now()
);

-- WEATHER_TEMP: Temperature observations for weather derivatives
-- Canonical unit: Celsius. Clients must convert Fahrenheit before submission.
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "activated_at"
) VALUES (
  gen_random_uuid(),
  'WEATHER_TEMP', 1,
  'Weather Temperature',
  'Temperature observations for weather derivatives and hedging. All values stored in Celsius.',
  'MEASUREMENT',
  'parse_decimal(observation_context.temperature_celsius) >= -100 && parse_decimal(observation_context.temperature_celsius) <= 100',
  'observation_context.station_code + "/" + string(observation_context.observation_date)',
  '"Invalid temperature: must be between -100 and 100 Celsius"',
  '{"type":"object","properties":{"station_code":{"type":"string"},"observation_date":{"type":"string","format":"date"},"temperature_celsius":{"type":"number","minimum":-100,"maximum":100}},"required":["station_code","observation_date","temperature_celsius"]}',
  'ACTIVE',
  now()
);
