-- Create party_type_definition table for tenant-configurable party type schemas.
-- Stores JSON Schema for attribute validation and CEL expressions for cross-field
-- validation, account eligibility, and custom error messages.
CREATE TABLE "party_type_definition" (
    "id"               UUID         NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id"        VARCHAR(100) NOT NULL,
    "party_type"       VARCHAR(100) NOT NULL,
    "attribute_schema" TEXT         NOT NULL,
    "validation_cel"   TEXT         NOT NULL DEFAULT '',
    "eligibility_cel"  TEXT         NOT NULL DEFAULT '',
    "error_message_cel" TEXT        NOT NULL DEFAULT '',
    "version"          BIGINT       NOT NULL DEFAULT 1,
    "created_at"       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    "updated_at"       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY ("id")
);
