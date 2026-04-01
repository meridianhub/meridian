-- Add lifecycle status to provider_connections and instruction_routes for deprecation support.
-- Config-only resources go ACTIVE on creation, DEPRECATED on deprecation (no DRAFT state).

-- provider_connections: lifecycle status
ALTER TABLE provider_connections ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE provider_connections ADD COLUMN deprecated_at TIMESTAMPTZ NULL;

ALTER TABLE provider_connections ADD CONSTRAINT chk_provider_connection_status
  CHECK (status IN ('ACTIVE', 'DEPRECATED'));

CREATE INDEX idx_provider_connections_status ON provider_connections (tenant_id, status);

-- instruction_routes: lifecycle status
ALTER TABLE instruction_routes ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE instruction_routes ADD COLUMN deprecated_at TIMESTAMPTZ NULL;

ALTER TABLE instruction_routes ADD CONSTRAINT chk_instruction_route_status
  CHECK (status IN ('ACTIVE', 'DEPRECATED'));

CREATE INDEX idx_instruction_routes_status ON instruction_routes (tenant_id, status);
