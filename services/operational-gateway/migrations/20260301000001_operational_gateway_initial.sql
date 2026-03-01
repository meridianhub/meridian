-- Operational Gateway Service Schema
-- Manages external provider connections and instruction dispatch
-- Implements outbox pattern for reliable at-least-once delivery
-- Uses unqualified table names (relies on database-per-service architecture)

-- ============================================================
-- Table: provider_connections
-- Stores connection config from manifest + runtime health state
-- ============================================================
CREATE TABLE "provider_connections" (
  "tenant_id"          UUID          NOT NULL,
  "connection_id"      VARCHAR(64)   NOT NULL,
  "provider_name"      VARCHAR(255)  NOT NULL,
  -- provider_type max_len=128 from proto ProviderConnection.provider_type
  "provider_type"      VARCHAR(128)  NOT NULL,
  "protocol"           VARCHAR(32)   NOT NULL,
  "base_url"           VARCHAR(2048) NOT NULL,
  -- Credentials / connection secrets - encrypted at rest
  "auth_config"        JSONB         NOT NULL DEFAULT '{}',
  "retry_policy"       JSONB         NULL,
  "rate_limit_config"  JSONB         NULL,
  -- Runtime health state
  "health_status"      VARCHAR(32)   NOT NULL DEFAULT 'UNKNOWN',
  "circuit_state"      VARCHAR(32)   NOT NULL DEFAULT 'CLOSED',
  "circuit_opened_at"  TIMESTAMPTZ   NULL,
  "created_at"         TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  "updated_at"         TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("tenant_id", "connection_id")
);

-- Index: look up all connections for a tenant
CREATE INDEX "idx_provider_connections_tenant" ON "provider_connections" ("tenant_id");

-- Index: find unhealthy / open-circuit connections for monitoring
CREATE INDEX "idx_provider_connections_health" ON "provider_connections" ("health_status", "circuit_state");

-- ============================================================
-- Table: instructions
-- Main dispatch table; doubles as the outbox
-- ============================================================
CREATE TABLE "instructions" (
  "id"                     UUID         NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id"              UUID         NOT NULL,
  "instruction_type"       VARCHAR(255) NOT NULL,
  "provider_connection_id" VARCHAR(64)  NOT NULL,
  "correlation_id"         VARCHAR(255) NULL,
  "causation_id"           VARCHAR(255) NULL,
  "payload"                JSONB        NOT NULL DEFAULT '{}',
  "metadata"               JSONB        NULL,
  "priority"               VARCHAR(16)  NOT NULL DEFAULT 'NORMAL',
  "status"                 VARCHAR(32)  NOT NULL DEFAULT 'PENDING',
  "scheduled_at"           TIMESTAMPTZ  NULL,
  "expires_at"             TIMESTAMPTZ  NULL,
  "dispatched_at"          TIMESTAMPTZ  NULL,
  "completed_at"           TIMESTAMPTZ  NULL,
  "max_attempts"           INT          NOT NULL DEFAULT 3,
  "attempt_count"          INT          NOT NULL DEFAULT 0,
  "created_at"             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  "updated_at"             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  -- Tenant-scoped FK ensures provider_connection_id cannot be orphaned or reference
  -- a connection belonging to a different tenant
  CONSTRAINT "fk_instructions_provider_connection"
    FOREIGN KEY ("tenant_id", "provider_connection_id")
    REFERENCES "provider_connections" ("tenant_id", "connection_id")
);

-- Index: outbox polling - worker picks up PENDING/RETRYING instructions ordered by priority.
-- Status values match InstructionStatus proto enum suffixes (PENDING, RETRYING).
CREATE INDEX "idx_instructions_outbox_poll" ON "instructions" ("status", "priority", "scheduled_at")
  WHERE status IN ('PENDING', 'RETRYING');

-- Index: look up by tenant + correlation for saga tracing
CREATE INDEX "idx_instructions_tenant_correlation" ON "instructions" ("tenant_id", "correlation_id");

-- Index: look up by tenant + type + status for operational dashboards
CREATE INDEX "idx_instructions_tenant_type_status" ON "instructions" ("tenant_id", "instruction_type", "status");

-- ============================================================
-- Table: instruction_attempts
-- Per-attempt outcome log for retry tracking and debugging
-- tenant_id is included for direct tenant-scoped queries without joining instructions
-- ============================================================
CREATE TABLE "instruction_attempts" (
  "id"                    UUID          NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id"             UUID          NOT NULL,
  "instruction_id"        UUID          NOT NULL REFERENCES "instructions" ("id"),
  "attempt_number"        INT           NOT NULL,
  "dispatched_at"         TIMESTAMPTZ   NULL,
  "completed_at"          TIMESTAMPTZ   NULL,
  "response_status_code"  INT           NULL,
  "response_body_preview" VARCHAR(1024) NULL,
  "error_message"         TEXT          NULL,
  "duration_ms"           INT           NULL,
  PRIMARY KEY ("id"),
  UNIQUE ("instruction_id", "attempt_number")
);

-- Index: retrieve all attempts for an instruction (chronological)
CREATE INDEX "idx_instruction_attempts_instruction" ON "instruction_attempts" ("instruction_id", "attempt_number");

-- Index: tenant-scoped attempt queries (aligns with WithGormTenantScope)
CREATE INDEX "idx_instruction_attempts_tenant" ON "instruction_attempts" ("tenant_id", "instruction_id");
