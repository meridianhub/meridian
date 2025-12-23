-- Party Service Business Qualifiers (BIAN Support)
-- Adds tables for party_association, party_demographic, party_reference, and party_bank_relation
-- Uses UNQUALIFIED table names to support multi-organization routing via search_path.

-- Create "party_association" table (many-to-many relationship between parties)
CREATE TABLE "party_association" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "party_id" uuid NOT NULL,
  "related_party_id" uuid NOT NULL,
  "relationship_type" character varying(50) NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_party_association_party" FOREIGN KEY ("party_id") REFERENCES "party" ("id") ON DELETE CASCADE,
  CONSTRAINT "fk_party_association_related_party" FOREIGN KEY ("related_party_id") REFERENCES "party" ("id") ON DELETE CASCADE,
  CONSTRAINT "chk_party_association_no_self" CHECK ("party_id" != "related_party_id"),
  CONSTRAINT "uq_party_association_parties" UNIQUE ("party_id", "related_party_id")
);

-- Create indexes for party_association
CREATE INDEX "idx_party_association_party_id" ON "party_association" ("party_id");
CREATE INDEX "idx_party_association_related_party_id" ON "party_association" ("related_party_id");
CREATE INDEX "idx_party_association_relationship_type" ON "party_association" ("relationship_type");

-- Create "party_demographic" table (one-to-one with party)
CREATE TABLE "party_demographic" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "party_id" uuid NOT NULL,
  "socio_economic_data" jsonb NULL,
  "employment_history" jsonb NULL,
  "income_level" character varying(50) NULL,
  "education_level" character varying(50) NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_party_demographic_party" FOREIGN KEY ("party_id") REFERENCES "party" ("id") ON DELETE CASCADE,
  CONSTRAINT "uq_party_demographic_party_id" UNIQUE ("party_id")
);

-- Create indexes for party_demographic
CREATE INDEX "idx_party_demographic_party_id" ON "party_demographic" ("party_id");
CREATE INDEX "idx_party_demographic_socio_economic" ON "party_demographic" USING GIN ("socio_economic_data");
CREATE INDEX "idx_party_demographic_employment" ON "party_demographic" USING GIN ("employment_history");

-- Create "party_reference" table (multiple reference identifiers per party)
CREATE TABLE "party_reference" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "party_id" uuid NOT NULL,
  "reference_type" character varying(50) NOT NULL,
  "reference_value" character varying(255) NOT NULL,
  "issuing_authority" character varying(100) NULL,
  "issue_date" date NULL,
  "expiry_date" date NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_party_reference_party" FOREIGN KEY ("party_id") REFERENCES "party" ("id") ON DELETE CASCADE
);

-- Create indexes for party_reference
CREATE INDEX "idx_party_reference_party_id" ON "party_reference" ("party_id");
CREATE INDEX "idx_party_reference_type_value" ON "party_reference" ("reference_type", "reference_value");
CREATE INDEX "idx_party_reference_expiry_date" ON "party_reference" ("expiry_date") WHERE ("expiry_date" IS NOT NULL);

-- Create "party_bank_relation" table (one-to-one with party)
CREATE TABLE "party_bank_relation" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "party_id" uuid NOT NULL,
  "account_officer_id" uuid NULL,
  "relationship_manager_id" uuid NULL,
  "assigned_branch" character varying(100) NULL,
  "relationship_start_date" date NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_party_bank_relation_party" FOREIGN KEY ("party_id") REFERENCES "party" ("id") ON DELETE CASCADE,
  CONSTRAINT "uq_party_bank_relation_party_id" UNIQUE ("party_id")
);

-- Create indexes for party_bank_relation
CREATE INDEX "idx_party_bank_relation_party_id" ON "party_bank_relation" ("party_id");
CREATE INDEX "idx_party_bank_relation_account_officer" ON "party_bank_relation" ("account_officer_id") WHERE ("account_officer_id" IS NOT NULL);
CREATE INDEX "idx_party_bank_relation_relationship_manager" ON "party_bank_relation" ("relationship_manager_id") WHERE ("relationship_manager_id" IS NOT NULL);
