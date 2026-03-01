-- Operational Gateway Service Schema
-- Manages outbound instructions to external providers and provider connection configurations.
-- Uses unqualified table names (relies on database-per-service architecture).

-- provider_connections stores the configuration for connecting to external provider endpoints.
-- Connections are reused across multiple instructions of matching instruction_types.
CREATE TABLE provider_connections (
    tenant_id UUID NOT NULL,
    connection_id VARCHAR(64) NOT NULL,
    provider_name VARCHAR(255) NOT NULL,
    provider_type VARCHAR(128) NOT NULL,
    -- protocol: HTTPS=1, GRPC=2, WEBHOOK=3, MQTT=4, AMQP=5
    protocol VARCHAR(20) NOT NULL CHECK (protocol IN ('HTTPS', 'GRPC', 'WEBHOOK', 'MQTT', 'AMQP')),
    base_url VARCHAR(2048) NOT NULL,
    -- auth_config stores the serialised authentication configuration (ApiKeyAuth, BasicAuth, OAuth2Auth, HMACAuth, MTLSAuth).
    -- Exactly one auth variant is populated; the auth_type discriminator field identifies which.
    auth_config JSONB NOT NULL,
    -- retry_policy stores the RetryPolicy fields: max_attempts, initial_backoff_seconds, max_backoff_seconds, backoff_multiplier.
    retry_policy JSONB NULL,
    -- rate_limit_config stores the RateLimit fields: requests_per_second, burst_size.
    rate_limit_config JSONB NULL,
    -- health_status: UNSPECIFIED=0, HEALTHY=1, DEGRADED=2, UNHEALTHY=3
    health_status VARCHAR(20) NOT NULL DEFAULT 'UNSPECIFIED' CHECK (health_status IN ('UNSPECIFIED', 'HEALTHY', 'DEGRADED', 'UNHEALTHY')),
    last_health_check_at TIMESTAMPTZ NULL,
    -- circuit_state tracks the circuit-breaker state for this connection.
    -- CLOSED (normal), OPEN (blocking dispatch), HALF_OPEN (probing recovery).
    circuit_state VARCHAR(20) NOT NULL DEFAULT 'CLOSED' CHECK (circuit_state IN ('CLOSED', 'OPEN', 'HALF_OPEN')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, connection_id)
);

-- Index for listing connections per tenant (common read path).
CREATE INDEX idx_provider_connections_tenant ON provider_connections (tenant_id);

-- Index for health-based filtering (worker queries unhealthy connections for health checks).
CREATE INDEX idx_provider_connections_health ON provider_connections (tenant_id, health_status);

-- instructions is the outbox table for outbound operational directives sent to external providers.
-- Each instruction follows the outbox pattern: written atomically with the originating saga step,
-- then picked up by the dispatch worker for async delivery.
CREATE TABLE instructions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    -- instruction_type identifies the category of operation (e.g. "payment.initiate", "account.freeze").
    instruction_type VARCHAR(255) NOT NULL,
    -- provider_connection_id references the connection used to dispatch this instruction.
    provider_connection_id VARCHAR(64) NOT NULL,
    -- correlation_id links all events across services for a single user request.
    correlation_id VARCHAR(255) NULL,
    -- causation_id identifies the event or command that caused this instruction to be created.
    causation_id VARCHAR(255) NULL,
    -- payload is the instruction-specific data serialised as JSON (google.protobuf.Struct).
    payload JSONB NOT NULL,
    -- metadata stores additional key-value pairs for routing, filtering, or audit purposes.
    metadata JSONB NULL,
    -- priority: LOW=1, NORMAL=2, HIGH=3, CRITICAL=4
    priority VARCHAR(20) NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'CRITICAL')),
    -- status follows the InstructionStatus state machine defined in the proto.
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'DISPATCHING', 'DELIVERED', 'ACKNOWLEDGED', 'RETRYING', 'FAILED', 'EXPIRED', 'CANCELLED')),
    scheduled_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMPTZ NULL,
    -- idempotency_key ensures exactly-once dispatch of each instruction.
    idempotency_key VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id)
);

-- Outbox polling index: dispatch worker polls for PENDING/RETRYING instructions ordered by priority
-- then by scheduled_at. Partial index limits scan to actionable rows only.
CREATE INDEX idx_instructions_outbox ON instructions (status, priority DESC, scheduled_at ASC)
    WHERE status IN ('PENDING', 'RETRYING');

-- Tenant + type index: used by list and routing queries.
CREATE INDEX idx_instructions_tenant_type ON instructions (tenant_id, instruction_type);

-- Correlation index: used for distributed tracing lookups.
CREATE INDEX idx_instructions_correlation ON instructions (tenant_id, correlation_id)
    WHERE correlation_id IS NOT NULL;

-- Idempotency index: enforces exactly-once dispatch per tenant.
CREATE UNIQUE INDEX idx_instructions_idempotency ON instructions (tenant_id, idempotency_key);

-- instruction_attempts records the outcome of each individual dispatch attempt.
-- Stored separately to avoid unbounded row growth on the instructions table.
CREATE TABLE instruction_attempts (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    -- instruction_id references the parent instruction.
    instruction_id UUID NOT NULL,
    -- attempt_number is the 1-based ordinal of this attempt.
    attempt_number INTEGER NOT NULL CHECK (attempt_number >= 1),
    dispatched_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    -- response_status_code is the HTTP or gRPC status code returned by the provider (0 if no response).
    response_status_code INTEGER NULL CHECK (response_status_code IS NULL OR (response_status_code >= 0 AND response_status_code <= 599)),
    -- response_body_preview is the first 1KB of the provider response body for diagnostics.
    response_body_preview VARCHAR(1024) NULL,
    -- error_message describes the error if this attempt failed.
    error_message TEXT NULL,
    -- duration_ms is how long the dispatch attempt took in milliseconds.
    duration_ms BIGINT NULL CHECK (duration_ms IS NULL OR duration_ms >= 0),
    PRIMARY KEY (id),
    FOREIGN KEY (instruction_id) REFERENCES instructions (id)
);

-- Lookup index: used to retrieve all attempts for a given instruction in attempt order.
CREATE INDEX idx_instruction_attempts_instruction ON instruction_attempts (instruction_id, attempt_number);
