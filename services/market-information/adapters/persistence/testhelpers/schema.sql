-- Market information test schema DDL
-- Embedded via //go:embed for test helper loadSchema()

CREATE TABLE data_source (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    code character varying(50) NOT NULL,
    name character varying(255) NOT NULL,
    description text NULL,
    trust_level integer NOT NULL DEFAULT 50,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
    deleted_at timestamptz NULL,
    version bigint NOT NULL DEFAULT 1,
    status character varying(20) NOT NULL DEFAULT 'ACTIVE',
    deprecated_at timestamptz NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_data_source_code UNIQUE (code),
    CONSTRAINT chk_data_source_trust_level CHECK (trust_level >= 0 AND trust_level <= 100),
    CONSTRAINT chk_data_source_status CHECK (status IN ('ACTIVE', 'DEPRECATED'))
);

CREATE TABLE dataset_definition (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    code character varying(50) NOT NULL,
    version integer NOT NULL DEFAULT 1,
    name character varying(255) NOT NULL,
    description text NULL,
    data_category character varying(50) NULL,
    validation_expression text NULL,
    resolution_key_expression text NOT NULL,
    error_message_expression text NULL,
    attribute_schema jsonb NULL,
    status character varying(20) NOT NULL DEFAULT 'DRAFT',
    is_shared BOOLEAN NOT NULL DEFAULT FALSE,
    access_level VARCHAR(50) NOT NULL DEFAULT 'PRIVATE',
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
    deleted_at timestamptz NULL,
    activated_at timestamptz NULL,
    deprecated_at timestamptz NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_dataset_definition_code_version UNIQUE (code, version),
    CONSTRAINT chk_dataset_definition_status CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    CONSTRAINT chk_dataset_definition_access_level CHECK (access_level IN ('PUBLIC', 'PRIVATE', 'RESTRICTED')),
    CONSTRAINT chk_dataset_definition_lifecycle_timestamps CHECK (
        (status = 'DRAFT' AND activated_at IS NULL AND deprecated_at IS NULL) OR
        (status = 'ACTIVE' AND activated_at IS NOT NULL AND deprecated_at IS NULL) OR
        (status = 'DEPRECATED' AND deprecated_at IS NOT NULL)
    ),
    CONSTRAINT chk_dataset_definition_validation_expression_length CHECK (
        validation_expression IS NULL OR length(validation_expression) <= 4096
    ),
    CONSTRAINT chk_dataset_definition_resolution_key_expression_length CHECK (
        length(resolution_key_expression) <= 4096
    ),
    CONSTRAINT chk_dataset_definition_error_message_length CHECK (
        error_message_expression IS NULL OR length(error_message_expression) <= 4096
    )
);

CREATE INDEX idx_dataset_definition_code_active ON dataset_definition (code) WHERE status = 'ACTIVE';
CREATE INDEX idx_dataset_definition_status ON dataset_definition (status);
CREATE INDEX idx_dataset_definition_created_at ON dataset_definition (created_at);
CREATE INDEX idx_dataset_definition_deleted_at ON dataset_definition (deleted_at);

CREATE TABLE market_price_observation (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    dataset_definition_id uuid NOT NULL,
    data_source_id uuid NOT NULL,
    resolution_key character varying(255) NOT NULL,
    observed_at timestamptz NOT NULL,
    valid_from timestamptz NULL,
    valid_to timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
    quality integer NOT NULL,
    observation_context jsonb NOT NULL DEFAULT '{}'::jsonb,
    numeric_value numeric NULL,
    text_value text NULL,
    superseded_by uuid NULL,
    causation_id uuid NULL,
    PRIMARY KEY (id),
    CONSTRAINT fk_observation_dataset_definition
        FOREIGN KEY (dataset_definition_id) REFERENCES dataset_definition(id) ON DELETE RESTRICT,
    CONSTRAINT fk_observation_data_source
        FOREIGN KEY (data_source_id) REFERENCES data_source(id) ON DELETE RESTRICT,
    CONSTRAINT fk_observation_superseded_by
        FOREIGN KEY (superseded_by) REFERENCES market_price_observation(id) ON DELETE SET NULL,
    CONSTRAINT chk_observation_quality CHECK (quality IN (1, 2, 3)),
    CONSTRAINT chk_observation_value_present CHECK (numeric_value IS NOT NULL OR text_value IS NOT NULL)
);

CREATE INDEX idx_observation_resolution_bitemporal
    ON market_price_observation (resolution_key, quality DESC, observed_at DESC, created_at DESC)
    WHERE superseded_by IS NULL;
CREATE INDEX idx_observation_dataset
    ON market_price_observation (dataset_definition_id, observed_at DESC);
CREATE INDEX idx_observation_source
    ON market_price_observation (data_source_id, created_at DESC);
CREATE INDEX idx_observation_created_at
    ON market_price_observation (created_at DESC)
    WHERE superseded_by IS NULL;
CREATE INDEX idx_observation_superseded_by
    ON market_price_observation (superseded_by)
    WHERE superseded_by IS NOT NULL;
CREATE INDEX idx_observation_causation
    ON market_price_observation (causation_id)
    WHERE causation_id IS NOT NULL;

CREATE INDEX idx_data_source_trust_level ON data_source (trust_level DESC);
CREATE INDEX idx_data_source_deleted_at ON data_source (deleted_at);

-- Cursor pagination indexes (from 20260123000003_add_cursor_pagination_indexes.sql)
CREATE INDEX idx_data_source_cursor
    ON data_source (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_dataset_definition_cursor
    ON dataset_definition (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_market_price_observation_cursor
    ON market_price_observation (created_at DESC, id DESC);

CREATE TABLE tenant_data_entitlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    dataset_code VARCHAR(255) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
    CONSTRAINT uq_tenant_dataset UNIQUE (tenant_id, dataset_code)
);

CREATE INDEX idx_entitlements_tenant_dataset
    ON tenant_data_entitlements(tenant_id, dataset_code, is_active)
    WHERE is_active = TRUE;
CREATE INDEX idx_entitlements_expires_at
    ON tenant_data_entitlements(expires_at)
    WHERE expires_at IS NOT NULL AND is_active = TRUE;
