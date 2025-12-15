-- Party Service initial migration
-- Uses UNQUALIFIED table names to support multi-organization routing via search_path.
-- The schema is created by organization provisioning, not by this migration.
-- Runtime search_path (e.g., org_acme_bank) routes queries to the correct org schema.

-- Create "parties" table (unqualified - uses search_path for schema routing)
CREATE TABLE "parties" (
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
-- Create index "idx_parties_party_type" to table: "parties"
CREATE INDEX "idx_parties_party_type" ON "parties" ("party_type");
-- Create index "idx_parties_status" to table: "parties"
CREATE INDEX "idx_parties_status" ON "parties" ("status");
-- Create index "idx_party_external_ref" to table: "parties"
CREATE UNIQUE INDEX "idx_party_external_ref" ON "parties" ("external_reference", "external_reference_type") WHERE ((external_reference IS NOT NULL) AND (deleted_at IS NULL));
-- Create index "idx_party_parties_deleted_at" to table: "parties"
CREATE INDEX "idx_party_parties_deleted_at" ON "parties" ("deleted_at");
