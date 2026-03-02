-- Identity Service Schema
-- Uses UNQUALIFIED table names to support multi-organization routing via search_path.
-- For local dev: search_path routes to default schema
-- For multi-org: org schemas created by provisioning, search_path routes to org schema

-- Create "identity" table (singular, unqualified - uses search_path for schema routing)
CREATE TABLE "identity" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "email" character varying(255) NOT NULL,
  "status" character varying(30) NOT NULL DEFAULT 'PENDING_INVITE',
  "password_hash" character varying(255) NOT NULL DEFAULT '',
  "external_idp" character varying(100) NOT NULL DEFAULT '',
  "external_sub" character varying(255) NOT NULL DEFAULT '',
  "failed_attempts" bigint NOT NULL DEFAULT 0,
  "version" bigint NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "deleted_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_identity_status" CHECK (status IN ('PENDING_INVITE', 'ACTIVE', 'SUSPENDED', 'LOCKED'))
);
-- Indexes for identity
CREATE UNIQUE INDEX "idx_identity_email" ON "identity" ("email") WHERE (deleted_at IS NULL);
CREATE INDEX "idx_identity_deleted_at" ON "identity" ("deleted_at");

-- Create "role_assignment" table
CREATE TABLE "role_assignment" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "identity_id" uuid NOT NULL,
  "granted_by" uuid NOT NULL,
  "role" character varying(50) NOT NULL,
  "expires_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "revoked_by" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_role_assignment_role" CHECK (role IN ('VIEWER', 'OPERATOR', 'ADMIN', 'TENANT_OWNER', 'PLATFORM')),
  CONSTRAINT "fk_role_assignment_identity" FOREIGN KEY ("identity_id") REFERENCES "identity" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "fk_role_assignment_granted_by" FOREIGN KEY ("granted_by") REFERENCES "identity" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "fk_role_assignment_revoked_by" FOREIGN KEY ("revoked_by") REFERENCES "identity" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);
-- Indexes for role_assignment
CREATE INDEX "idx_role_assignment_identity" ON "role_assignment" ("identity_id");
-- Partial unique index enforces one active role per (identity, role) pair.
-- CockroachDB: partial index on existing columns in a separate statement is safe.
CREATE UNIQUE INDEX "idx_role_assignment_active" ON "role_assignment" ("identity_id", "role") WHERE (revoked_at IS NULL);

-- Create "invitation" table
CREATE TABLE "invitation" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "identity_id" uuid NOT NULL,
  "invited_by" uuid NOT NULL,
  "token_hash" character varying(64) NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "status" character varying(20) NOT NULL DEFAULT 'PENDING',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_invitation_status" CHECK (status IN ('PENDING', 'ACCEPTED')),
  CONSTRAINT "fk_invitation_identity" FOREIGN KEY ("identity_id") REFERENCES "identity" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "fk_invitation_invited_by" FOREIGN KEY ("invited_by") REFERENCES "identity" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);
-- Indexes for invitation
CREATE INDEX "idx_invitation_identity" ON "invitation" ("identity_id");
CREATE UNIQUE INDEX "idx_invitation_token_hash" ON "invitation" ("token_hash");
