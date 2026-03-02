-- instruction_routes maps instruction_type to a provider connection for outbound dispatch.
-- Routes are configured via manifest apply and determine how the dispatch worker resolves
-- which connection and transformation to use for each instruction type.
CREATE TABLE instruction_routes (
    tenant_id UUID NOT NULL,
    -- instruction_type is the unique key for route lookup (e.g. "payment.initiate").
    instruction_type VARCHAR(255) NOT NULL,
    -- connection_id is the primary ProviderConnection UUID for this route.
    connection_id UUID NOT NULL,
    -- fallback_connection_id is an optional secondary connection used when the primary is unhealthy.
    fallback_connection_id UUID NULL,
    -- outbound_mapping is the name of the MappingDefinition for outbound payload transformation.
    outbound_mapping VARCHAR(255) NOT NULL DEFAULT '',
    -- inbound_mapping is the name of the MappingDefinition for inbound response transformation.
    inbound_mapping VARCHAR(255) NOT NULL DEFAULT '',
    -- http_method is the HTTP verb for HTTPS/WEBHOOK protocols (e.g. "POST").
    http_method VARCHAR(10) NOT NULL DEFAULT '',
    -- path_template is the URL path template appended to the connection base_url.
    path_template VARCHAR(1024) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, instruction_type),
    FOREIGN KEY (tenant_id, connection_id) REFERENCES provider_connections (tenant_id, connection_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, fallback_connection_id) REFERENCES provider_connections (tenant_id, connection_id) ON DELETE SET NULL
);

-- Index for finding routes by connection (e.g. validating impact before deleting a connection).
CREATE INDEX idx_instruction_routes_connection_id ON instruction_routes (tenant_id, connection_id);
