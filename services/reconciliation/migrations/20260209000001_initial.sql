-- Reconciliation Service Schema
-- Uses unqualified table names (relies on database-per-service architecture)
-- Tenant isolation via SET LOCAL search_path TO org_{tenant_id}

-- Create "settlement_run" table (CR - Command Record)
CREATE TABLE "settlement_run" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "run_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "scope" character varying(20) NOT NULL,
  "settlement_type" character varying(20) NOT NULL,
  "status" character varying(20) NOT NULL DEFAULT 'PENDING',
  "period_start" timestamptz NOT NULL,
  "period_end" timestamptz NOT NULL,
  "initiated_by" character varying(100) NOT NULL,
  "completed_at" timestamptz NULL,
  "variance_count" integer NOT NULL DEFAULT 0,
  "failure_reason" text NULL,
  "attributes" jsonb NULL,
  "version" bigint NOT NULL DEFAULT 1,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "idx_settlement_run_run_id" ON "settlement_run" ("run_id");
CREATE INDEX "idx_settlement_run_account_id" ON "settlement_run" ("account_id");
CREATE INDEX "idx_settlement_run_status" ON "settlement_run" ("status");
CREATE INDEX "idx_settlement_run_period" ON "settlement_run" ("period_start", "period_end");
CREATE INDEX "idx_settlement_run_created_at" ON "settlement_run" ("created_at");

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_scope"
  CHECK ("scope" IN ('ACCOUNT', 'INSTRUMENT', 'PORTFOLIO', 'FULL'));
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_settlement_type"
  CHECK ("settlement_type" IN ('DAILY', 'WEEKLY', 'MONTHLY', 'ON_DEMAND', 'END_OF_DAY', 'REAL_TIME'));
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_status"
  CHECK ("status" IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED'));
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_period"
  CHECK ("period_start" < "period_end");

-- Create "settlement_snapshot" table (BQ - Business Query)
CREATE TABLE "settlement_snapshot" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "snapshot_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "instrument_code" character varying(20) NOT NULL,
  "expected_balance" decimal(38, 18) NOT NULL,
  "actual_balance" decimal(38, 18) NOT NULL,
  "variance_amount" decimal(38, 18) NOT NULL,
  "source_system" character varying(100) NOT NULL,
  "attributes" jsonb NULL,
  "captured_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_settlement_snapshot_run" FOREIGN KEY ("run_id")
    REFERENCES "settlement_run" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

CREATE UNIQUE INDEX "idx_settlement_snapshot_snapshot_id" ON "settlement_snapshot" ("snapshot_id");
CREATE INDEX "idx_settlement_snapshot_run_id" ON "settlement_snapshot" ("run_id");
CREATE INDEX "idx_settlement_snapshot_account_id" ON "settlement_snapshot" ("account_id");
CREATE INDEX "idx_settlement_snapshot_captured_at" ON "settlement_snapshot" ("captured_at");

-- Create "variance" table (BQ - Business Query)
CREATE TABLE "variance" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "variance_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "snapshot_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "instrument_code" character varying(20) NOT NULL,
  "expected_amount" decimal(38, 18) NOT NULL,
  "actual_amount" decimal(38, 18) NOT NULL,
  "variance_amount" decimal(38, 18) NOT NULL,
  "value_delta" decimal(38, 18) NOT NULL DEFAULT 0,
  "currency" character varying(10) NOT NULL DEFAULT '',
  "reason" character varying(30) NOT NULL,
  "status" character varying(20) NOT NULL DEFAULT 'DETECTED',
  "resolution_note" text NULL,
  "resolved_by" character varying(100) NULL,
  "resolved_at" timestamptz NULL,
  "attributes" jsonb NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_variance_run" FOREIGN KEY ("run_id")
    REFERENCES "settlement_run" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "fk_variance_snapshot" FOREIGN KEY ("snapshot_id")
    REFERENCES "settlement_snapshot" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

CREATE UNIQUE INDEX "idx_variance_variance_id" ON "variance" ("variance_id");
CREATE INDEX "idx_variance_run_id" ON "variance" ("run_id");
CREATE INDEX "idx_variance_snapshot_id" ON "variance" ("snapshot_id");
CREATE INDEX "idx_variance_account_id" ON "variance" ("account_id");
CREATE INDEX "idx_variance_status" ON "variance" ("status");

ALTER TABLE "variance"
  ADD CONSTRAINT "chk_variance_reason"
  CHECK ("reason" IN ('AMOUNT_MISMATCH', 'MISSING_ENTRY', 'DUPLICATE_ENTRY', 'TIMING_DIFFERENCE', 'CURRENCY_MISMATCH', 'DIRECTION_ERROR', 'QUALITY_UPGRADE', 'EXTERNAL_MISMATCH', 'CORRECTION_APPLIED', 'OTHER'));
ALTER TABLE "variance"
  ADD CONSTRAINT "chk_variance_status"
  CHECK ("status" IN ('DETECTED', 'VALUED', 'OPEN', 'INVESTIGATING', 'DISPUTED', 'RESOLVED', 'ACCEPTED'));

-- Create "dispute" table (BQ - Business Query)
CREATE TABLE "dispute" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "dispute_id" uuid NOT NULL,
  "variance_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "status" character varying(20) NOT NULL DEFAULT 'OPEN',
  "reason" text NOT NULL,
  "resolution" text NULL,
  "raised_by" character varying(100) NOT NULL,
  "resolved_by" character varying(100) NULL,
  "resolved_at" timestamptz NULL,
  "attributes" jsonb NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_dispute_variance" FOREIGN KEY ("variance_id")
    REFERENCES "variance" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "fk_dispute_run" FOREIGN KEY ("run_id")
    REFERENCES "settlement_run" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

CREATE UNIQUE INDEX "idx_dispute_dispute_id" ON "dispute" ("dispute_id");
CREATE INDEX "idx_dispute_variance_id" ON "dispute" ("variance_id");
CREATE INDEX "idx_dispute_run_id" ON "dispute" ("run_id");
CREATE INDEX "idx_dispute_account_id" ON "dispute" ("account_id");
CREATE INDEX "idx_dispute_status" ON "dispute" ("status");

ALTER TABLE "dispute"
  ADD CONSTRAINT "chk_dispute_status"
  CHECK ("status" IN ('OPEN', 'UNDER_REVIEW', 'ESCALATED', 'RESOLVED', 'REJECTED'));

-- Create "balance_assertion" table (BQ - Business Query)
CREATE TABLE "balance_assertion" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "assertion_id" uuid NOT NULL,
  "run_id" uuid NULL,
  "account_id" character varying(34) NOT NULL,
  "instrument_code" character varying(20) NOT NULL,
  "expression" text NOT NULL,
  "expected_balance" decimal(38, 18) NOT NULL,
  "actual_balance" decimal(38, 18) NOT NULL DEFAULT 0,
  "status" character varying(20) NOT NULL DEFAULT 'PENDING',
  "failure_reason" text NULL,
  "override_reason" text NULL,
  "attributes" jsonb NULL,
  "asserted_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_balance_assertion_run" FOREIGN KEY ("run_id")
    REFERENCES "settlement_run" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);

CREATE UNIQUE INDEX "idx_balance_assertion_assertion_id" ON "balance_assertion" ("assertion_id");
CREATE INDEX "idx_balance_assertion_run_id" ON "balance_assertion" ("run_id");
CREATE INDEX "idx_balance_assertion_account_id" ON "balance_assertion" ("account_id");
CREATE INDEX "idx_balance_assertion_status" ON "balance_assertion" ("status");

ALTER TABLE "balance_assertion"
  ADD CONSTRAINT "chk_balance_assertion_status"
  CHECK ("status" IN ('PENDING', 'PASSED', 'FAILED', 'OVERRIDE'));
