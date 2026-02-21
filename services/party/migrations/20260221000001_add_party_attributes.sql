-- Add attributes JSONB column to party table for structured key-value metadata
ALTER TABLE "party" ADD COLUMN "attributes" JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN "party"."attributes" IS
  'Structured key-value attributes for this party, validated against the party type attribute_schema. Stored as a JSON array of {key, value} objects.';
