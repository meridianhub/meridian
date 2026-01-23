-- Bucket-aware solvency fields for fungibility constraints
-- These fields enable payment orders to validate solvency against bucket-scoped balances
-- when the payment instrument has fungibility constraints (e.g., rice by grade).

-- Add instrument_code column to identify the payment instrument
-- Used to look up fungibility_key_expression from reference-data service
ALTER TABLE "payment_order" ADD COLUMN "instrument_code" character varying(32) NULL;

-- Add payment_attributes as JSONB for instrument-specific metadata
-- Example: {"grade": "A", "lot_number": "LOT123"} for rice payments
ALTER TABLE "payment_order" ADD COLUMN "payment_attributes" jsonb NULL;

-- Add bucket_id for the evaluated fungibility bucket key
-- Computed from payment_attributes using the instrument's CEL expression
-- Empty/NULL means default fungibility (all quantities are fungible)
ALTER TABLE "payment_order" ADD COLUMN "bucket_id" character varying(255) NULL;

-- Index for querying by bucket_id (useful for analytics and debugging)
CREATE INDEX "idx_payment_order_bucket_id" ON "payment_order" ("bucket_id")
WHERE bucket_id IS NOT NULL;

-- Comments for documentation
COMMENT ON COLUMN "payment_order"."instrument_code" IS 'Payment instrument code (e.g., RICE-v1, USD). Used to look up fungibility rules from reference-data.';
COMMENT ON COLUMN "payment_order"."payment_attributes" IS 'Instrument-specific metadata for fungibility evaluation. JSON map of string key-value pairs.';
COMMENT ON COLUMN "payment_order"."bucket_id" IS 'Evaluated fungibility bucket key. NULL means default bucket (full fungibility).';
