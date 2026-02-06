-- Add validation metadata columns to saga_definition.
-- These store results from the mandatory DryRunValidator that runs
-- during CreateSagaDraft, enabling capacity planning and audit.
ALTER TABLE saga_definition ADD COLUMN IF NOT EXISTS validation_status TEXT NOT NULL DEFAULT 'UNVALIDATED';
ALTER TABLE saga_definition ADD COLUMN IF NOT EXISTS complexity_score INT;
ALTER TABLE saga_definition ADD COLUMN IF NOT EXISTS handler_call_count INT;
ALTER TABLE saga_definition ADD COLUMN IF NOT EXISTS validated_at TIMESTAMPTZ;
