-- Email verification tokens for self-registration flow
CREATE TABLE "email_verification_token" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" VARCHAR(50) NOT NULL,
  "identity_id" uuid NOT NULL,
  "token_hash" VARCHAR(64) NOT NULL,
  "expires_at" TIMESTAMPTZ NOT NULL,
  "consumed_at" TIMESTAMPTZ NULL,
  "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_verification_token_identity"
    FOREIGN KEY ("identity_id") REFERENCES "identity" ("id") ON DELETE CASCADE
);

CREATE UNIQUE INDEX "idx_verification_token_hash" ON "email_verification_token" ("token_hash");
CREATE INDEX "idx_verification_token_identity_pending" ON "email_verification_token" ("identity_id", "created_at" DESC) WHERE "consumed_at" IS NULL;
CREATE INDEX "idx_verification_token_tenant" ON "email_verification_token" ("tenant_id");
