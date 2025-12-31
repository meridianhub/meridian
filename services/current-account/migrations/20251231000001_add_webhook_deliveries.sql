-- Add webhook_deliveries table for audit trail of webhook notifications
-- Per ADR-0003: Using Atlas for database migrations
-- This table stores webhook delivery attempts for regulatory compliance events (Freeze, Close)

CREATE TABLE "webhook_deliveries" (
    -- Primary key: UUID for globally unique delivery identifier
    "id" uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Event identification
    "event_id" varchar(36) NOT NULL,
    "event_type" varchar(50) NOT NULL,

    -- Context
    "tenant_id" varchar(50) NOT NULL,
    "account_id" varchar(36) NOT NULL,
    "webhook_url" varchar(2048) NOT NULL,

    -- Delivery status
    -- pending: queued for delivery
    -- success: delivered successfully
    -- failed: failed after all retries
    "status" varchar(20) NOT NULL DEFAULT 'pending',

    -- Attempt tracking
    "attempts" integer NOT NULL DEFAULT 0,
    "last_attempt_at" timestamptz NULL,
    "last_error" text NULL,
    "response_code" integer NULL,

    -- Timestamps
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "completed_at" timestamptz NULL
);

-- Index for querying by tenant
CREATE INDEX idx_webhook_deliveries_tenant_id ON "webhook_deliveries" ("tenant_id");

-- Index for querying by account
CREATE INDEX idx_webhook_deliveries_account_id ON "webhook_deliveries" ("account_id");

-- Index for querying by status (to find pending/failed deliveries)
CREATE INDEX idx_webhook_deliveries_status ON "webhook_deliveries" ("status");

-- Index for querying by event type and timestamp (for audit queries)
CREATE INDEX idx_webhook_deliveries_event_type_created ON "webhook_deliveries" ("event_type", "created_at");

-- Index for querying recent deliveries by tenant
CREATE INDEX idx_webhook_deliveries_tenant_created ON "webhook_deliveries" ("tenant_id", "created_at" DESC);

-- Add comments for documentation
COMMENT ON TABLE "webhook_deliveries" IS
    'Audit trail for webhook notifications sent for account lifecycle events (freeze, close). Used for regulatory compliance tracking.';

COMMENT ON COLUMN "webhook_deliveries"."event_id" IS
    'Unique identifier for the event that triggered this webhook';

COMMENT ON COLUMN "webhook_deliveries"."event_type" IS
    'Type of event: account.frozen or account.closed';

COMMENT ON COLUMN "webhook_deliveries"."status" IS
    'Delivery status: pending (queued), success (delivered), failed (all retries exhausted)';

COMMENT ON COLUMN "webhook_deliveries"."attempts" IS
    'Number of delivery attempts made (including retries)';

COMMENT ON COLUMN "webhook_deliveries"."last_error" IS
    'Error message from the last failed delivery attempt';

COMMENT ON COLUMN "webhook_deliveries"."response_code" IS
    'HTTP status code from the last delivery attempt';
