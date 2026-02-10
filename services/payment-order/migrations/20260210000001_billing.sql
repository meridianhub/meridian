-- Billing Cycle Domain Tables
-- Manages billing runs and invoices for periodic billing cycle execution.
-- Uses unqualified table names (relies on database-per-service architecture).

-- Create "billing_run" table (singular, unqualified)
CREATE TABLE "billing_run" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" character varying(255) NOT NULL,
  "cycle_start" timestamptz NOT NULL,
  "cycle_end" timestamptz NOT NULL,
  "status" character varying(20) NOT NULL CHECK (status IN ('INITIATED', 'PROCESSING', 'COMPLETED', 'FAILED')),
  "dunning_level" integer NOT NULL DEFAULT 0,
  "failure_reason" character varying(1000) NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CHECK (cycle_end > cycle_start)
);

-- Idempotency: one billing run per tenant per period
CREATE UNIQUE INDEX "idx_billing_run_tenant_period" ON "billing_run" ("tenant_id", "cycle_start", "cycle_end");

-- Query billing runs by status (e.g., find all PROCESSING runs for monitoring)
CREATE INDEX "idx_billing_run_status" ON "billing_run" ("status");

-- Query billing runs by tenant (e.g., billing history)
CREATE INDEX "idx_billing_run_tenant" ON "billing_run" ("tenant_id", "created_at" DESC);

COMMENT ON TABLE "billing_run" IS 'Tracks billing cycle executions per tenant with deterministic idempotency.';
COMMENT ON COLUMN "billing_run"."dunning_level" IS 'Dunning escalation level (0=initial, increments on repeated failures/overdue).';

-- Create "invoice" table (singular, unqualified)
CREATE TABLE "invoice" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "billing_run_id" uuid NOT NULL REFERENCES "billing_run" ("id"),
  "party_id" character varying(255) NOT NULL,
  "account_id" character varying(255) NOT NULL,
  "invoice_number" character varying(50) NOT NULL,
  "period_start" timestamptz NOT NULL,
  "period_end" timestamptz NOT NULL,
  "line_items" jsonb NOT NULL DEFAULT '[]',
  "subtotal_cents" bigint NOT NULL,
  "currency" character(3) NOT NULL,
  "status" character varying(20) NOT NULL CHECK (status IN ('DRAFT', 'ISSUED', 'PAID', 'VOID', 'OVERDUE')),
  "payment_order_id" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

-- Unique invoice numbers (invoice_number includes tenant context to prevent cross-tenant collisions)
CREATE UNIQUE INDEX "idx_invoice_number" ON "invoice" ("invoice_number");

-- Find invoices by billing run
CREATE INDEX "idx_invoice_billing_run" ON "invoice" ("billing_run_id");

-- Find invoices by party (customer billing history)
CREATE INDEX "idx_invoice_party" ON "invoice" ("party_id", "created_at" DESC);

-- Find invoices by status (e.g., all OVERDUE invoices for dunning)
CREATE INDEX "idx_invoice_status" ON "invoice" ("status");

-- Find invoices by account (account-level billing)
CREATE INDEX "idx_invoice_account" ON "invoice" ("account_id");

COMMENT ON TABLE "invoice" IS 'Invoices generated from billing runs, linking position entries to payment orders.';
COMMENT ON COLUMN "invoice"."line_items" IS 'JSONB array of invoice line items with description, quantity, unit_price_cents, total_cents, and valuation_analysis.';
COMMENT ON COLUMN "invoice"."payment_order_id" IS 'Reference to the payment order created to collect this invoice (NULL for DRAFT/shadow invoices).';
