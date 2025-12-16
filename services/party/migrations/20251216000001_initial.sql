-- Party Service Schema
-- Uses UNQUALIFIED table names to support multi-organization routing via search_path.
-- For local dev: search_path routes to default schema
-- For multi-org: org schemas created by provisioning, search_path routes to org schema

-- Create "party" table (singular, unqualified - uses search_path for schema routing)
CREATE TABLE "party" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "party_type" character varying(20) NOT NULL,
  "legal_name" character varying(255) NOT NULL,
  "display_name" character varying(255) NULL,
  "status" character varying(20) NOT NULL DEFAULT 'ACTIVE',
  "external_reference" character varying(100) NULL,
  "external_reference_type" character varying(30) NULL,
  "version" bigint NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create indexes for party
CREATE INDEX "idx_party_party_type" ON "party" ("party_type");
CREATE INDEX "idx_party_status" ON "party" ("status");
CREATE UNIQUE INDEX "idx_party_external_ref" ON "party" ("external_reference", "external_reference_type") WHERE ((external_reference IS NOT NULL) AND (deleted_at IS NULL));
CREATE INDEX "idx_party_deleted_at" ON "party" ("deleted_at");
