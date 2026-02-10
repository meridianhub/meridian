-- Reference Data Node: hierarchical bi-temporal reference data with tenant isolation.
-- Supports arbitrary tree structures (region/zone/rack, dno/gsp/meter, etc.)
-- with denormalized resolution keys for fast MDS correlation.

CREATE TABLE "reference_data_node" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" text NOT NULL,
  "node_type" text NOT NULL,
  "parent_id" uuid NULL,
  "attributes" jsonb NOT NULL DEFAULT '{}',
  "resolution_key" text NOT NULL,
  "valid_from" timestamptz NOT NULL DEFAULT now(),
  "valid_to" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "version" bigint NOT NULL DEFAULT 1,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_reference_data_node_parent"
    FOREIGN KEY ("parent_id") REFERENCES "reference_data_node" ("id") ON DELETE RESTRICT,
  CONSTRAINT "valid_time_order"
    CHECK ("valid_to" IS NULL OR "valid_to" > "valid_from")
);

-- Partial unique constraint: only one active node per (tenant, type, resolution_key)
-- Uses a sentinel value for NULL valid_to to support CockroachDB partial unique indexes
CREATE UNIQUE INDEX "uq_active_node"
  ON "reference_data_node" ("tenant_id", "node_type", "resolution_key")
  WHERE "valid_to" IS NULL;

-- Index for tree traversal: find children of a parent within a tenant
CREATE INDEX "idx_node_tenant_parent"
  ON "reference_data_node" ("tenant_id", "parent_id")
  WHERE "valid_to" IS NULL;

-- Index for resolution key lookups (MDS correlation)
CREATE INDEX "idx_node_resolution_key"
  ON "reference_data_node" ("resolution_key", "tenant_id");

-- Index for bi-temporal range queries
CREATE INDEX "idx_node_valid_time"
  ON "reference_data_node" ("valid_from", "valid_to");

-- Index for listing active nodes by type within a tenant
CREATE INDEX "idx_node_tenant_type_active"
  ON "reference_data_node" ("tenant_id", "node_type")
  WHERE "valid_to" IS NULL;
