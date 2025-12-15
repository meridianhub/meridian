-- Party Service initial migration
-- Uses unqualified table names for multi-tenant schema routing.
-- Tables are created in whichever schema is set via search_path (e.g., org_acme_bank).

-- Create schema for party service
CREATE SCHEMA IF NOT EXISTS "party";

-- Create "parties" table
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
