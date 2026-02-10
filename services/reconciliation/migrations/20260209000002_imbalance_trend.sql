-- Add imbalance trend tracking table for persistent imbalance detection.
-- Tracks consecutive days of imbalance per instrument code.
-- A threshold of 3+ consecutive days triggers P1/Critical alerts.

CREATE TABLE "imbalance_trend" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "trend_id" uuid NOT NULL,
  "instrument_code" character varying(20) NOT NULL,
  "consecutive_days" integer NOT NULL DEFAULT 0,
  "last_imbalance_amount" decimal(38, 18) NOT NULL DEFAULT 0,
  "last_assertion_id" uuid NULL,
  "first_detected_at" timestamptz NOT NULL,
  "last_detected_at" timestamptz NOT NULL,
  "resolved_at" timestamptz NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "idx_imbalance_trend_trend_id" ON "imbalance_trend" ("trend_id");
CREATE UNIQUE INDEX "idx_imbalance_trend_instrument_active"
  ON "imbalance_trend" ("instrument_code") WHERE "resolved_at" IS NULL;
CREATE INDEX "idx_imbalance_trend_instrument_code" ON "imbalance_trend" ("instrument_code");
