-- Migration: Seed UTILIZATION_* Dataset Definitions and PLATFORM_AUDIT_EVENTS Data Source
-- Registers platform utilization metric datasets for tracking resource consumption
-- (transactions, API calls, storage, compute, network) per tenant.
--
-- Each dataset uses:
--   - resolution_key_expression: Regex pattern validating hierarchical tenant keys
--   - validation_expression: CEL expression constraining observation values
--   - data_category: UTILIZATION (new category for platform usage metrics)

--------------------------------------------------------------------------------
-- Section 1: Register PLATFORM_AUDIT_EVENTS Data Source
-- Internal platform source with highest trust level (100) for audit events
--------------------------------------------------------------------------------

INSERT INTO "data_source" ("id", "code", "name", "description", "trust_level", "created_by", "updated_by")
VALUES (
  gen_random_uuid(),
  'PLATFORM_AUDIT_EVENTS',
  'Platform Audit Events',
  'Internal platform audit event stream for utilization metrics. Highest trust level as data originates from the platform itself.',
  100,
  'SYSTEM',
  'SYSTEM'
);

--------------------------------------------------------------------------------
-- Section 2: Register UTILIZATION_* Dataset Definitions
-- All datasets created as ACTIVE with CEL validation and regex key patterns
--------------------------------------------------------------------------------

-- UTILIZATION_TRANSACTION: Tracks platform transaction counts per tenant and transaction type
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "created_by", "updated_by", "activated_at"
) VALUES (
  gen_random_uuid(),
  'UTILIZATION_TRANSACTION', 1,
  'Platform Transaction Usage',
  'Tracks transaction counts per tenant and transaction type for usage-based billing and capacity planning',
  'UTILIZATION',
  'numeric_value >= 0 && numeric_value < 1000000000000',
  '^tenant/[^/]+/transaction/[^/]+$',
  '"Invalid transaction count: must be non-negative and less than 1 trillion"',
  '{"type":"object","properties":{"tenant_id":{"type":"string"},"transaction_type":{"type":"string"},"count":{"type":"number","minimum":0}},"required":["tenant_id","transaction_type","count"]}',
  'ACTIVE',
  'SYSTEM', 'SYSTEM',
  now()
);

-- UTILIZATION_API_CALL: Tracks API call counts per tenant, service, and endpoint
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "created_by", "updated_by", "activated_at"
) VALUES (
  gen_random_uuid(),
  'UTILIZATION_API_CALL', 1,
  'Platform API Call Usage',
  'Tracks API call counts per tenant, service, and endpoint for rate limiting and billing',
  'UTILIZATION',
  'numeric_value >= 0 && numeric_value < 1000000000000',
  '^tenant/[^/]+/api/[^/]+/[^/]+$',
  '"Invalid API call count: must be non-negative and less than 1 trillion"',
  '{"type":"object","properties":{"tenant_id":{"type":"string"},"service":{"type":"string"},"endpoint":{"type":"string"},"count":{"type":"number","minimum":0}},"required":["tenant_id","service","endpoint","count"]}',
  'ACTIVE',
  'SYSTEM', 'SYSTEM',
  now()
);

-- UTILIZATION_STORAGE_GB: Tracks storage consumption per tenant and storage class
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "created_by", "updated_by", "activated_at"
) VALUES (
  gen_random_uuid(),
  'UTILIZATION_STORAGE_GB', 1,
  'Platform Storage Usage',
  'Tracks storage consumption in gigabytes per tenant and storage class',
  'UTILIZATION',
  'numeric_value >= 0 && numeric_value < 1000000000000',
  '^tenant/[^/]+/storage/[^/]+$',
  '"Invalid storage value: must be non-negative and less than 1 trillion GB"',
  '{"type":"object","properties":{"tenant_id":{"type":"string"},"storage_class":{"type":"string"},"gb":{"type":"number","minimum":0}},"required":["tenant_id","storage_class","gb"]}',
  'ACTIVE',
  'SYSTEM', 'SYSTEM',
  now()
);

-- UTILIZATION_COMPUTE_HOUR: Tracks compute usage per tenant and compute resource type
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "created_by", "updated_by", "activated_at"
) VALUES (
  gen_random_uuid(),
  'UTILIZATION_COMPUTE_HOUR', 1,
  'Platform Compute Usage',
  'Tracks compute consumption in hours per tenant and compute resource type',
  'UTILIZATION',
  'numeric_value >= 0 && numeric_value < 1000000000000',
  '^tenant/[^/]+/compute/[^/]+$',
  '"Invalid compute hours: must be non-negative and less than 1 trillion hours"',
  '{"type":"object","properties":{"tenant_id":{"type":"string"},"compute_type":{"type":"string"},"hours":{"type":"number","minimum":0}},"required":["tenant_id","compute_type","hours"]}',
  'ACTIVE',
  'SYSTEM', 'SYSTEM',
  now()
);

-- UTILIZATION_NETWORK_GB: Tracks network transfer per tenant and network interface
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "attribute_schema", "status", "created_by", "updated_by", "activated_at"
) VALUES (
  gen_random_uuid(),
  'UTILIZATION_NETWORK_GB', 1,
  'Platform Network Usage',
  'Tracks network transfer in gigabytes per tenant and network interface',
  'UTILIZATION',
  'numeric_value >= 0 && numeric_value < 1000000000000',
  '^tenant/[^/]+/network/[^/]+$',
  '"Invalid network transfer: must be non-negative and less than 1 trillion GB"',
  '{"type":"object","properties":{"tenant_id":{"type":"string"},"interface":{"type":"string"},"gb":{"type":"number","minimum":0}},"required":["tenant_id","interface","gb"]}',
  'ACTIVE',
  'SYSTEM', 'SYSTEM',
  now()
);
