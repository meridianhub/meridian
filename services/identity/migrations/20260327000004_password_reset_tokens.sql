-- Password reset tokens for forgot-password flow
CREATE TABLE "password_reset_token" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" VARCHAR(50) NOT NULL,
  "identity_id" uuid NOT NULL,
  "token_hash" VARCHAR(64) NOT NULL,
  "expires_at" TIMESTAMPTZ NOT NULL,
  "consumed_at" TIMESTAMPTZ NULL,
  "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_password_reset_token_identity"
    FOREIGN KEY ("identity_id") REFERENCES "identity" ("id") ON DELETE CASCADE
);

CREATE UNIQUE INDEX "idx_password_reset_token_hash" ON "password_reset_token" ("token_hash");
CREATE INDEX "idx_password_reset_token_rate_limit" ON "password_reset_token" ("identity_id", "created_at" DESC);
CREATE INDEX "idx_password_reset_token_tenant" ON "password_reset_token" ("tenant_id");
