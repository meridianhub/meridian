-- Backfill identity valuation methods for existing accounts
-- This migration creates valuation features for all existing accounts
-- mapping their native currency to itself (e.g., USD→USD, GBP→GBP)

-- Note: This migration assumes the Valuation Engine Service has been deployed
-- with identity methods registered. The method IDs are placeholders and should
-- be updated with actual method IDs from the Valuation Engine Service.

-- TODO: Replace these placeholder UUIDs with actual identity method IDs from Valuation Engine Service
-- These would be retrieved via: SELECT id FROM valuation_methods WHERE method_type = 'IDENTITY' AND input_instrument = output_instrument

-- For now, this migration is a template. The actual backfill should be done via
-- an application-level data migration that:
-- 1. Queries all accounts
-- 2. For each account currency, looks up the identity method from Valuation Engine Service
-- 3. Creates an ACTIVE valuation feature for that account

-- Example of what the backfill would look like (commented out as it requires runtime data):
/*
INSERT INTO valuation_features (
    id,
    account_id,
    instrument_code,
    valuation_method_id,
    valuation_method_version,
    parameters,
    lifecycle_status,
    valid_from,
    valid_to,
    created_at,
    created_by,
    updated_at,
    updated_by,
    version
)
SELECT
    gen_random_uuid() as id,
    a.id as account_id,
    a.currency as instrument_code,
    '00000000-0000-0000-0000-000000000000'::uuid as valuation_method_id, -- Placeholder - needs actual identity method ID
    1 as valuation_method_version,
    NULL as parameters,
    'ACTIVE' as lifecycle_status,
    NOW() as valid_from,
    '9999-12-31 23:59:59+00'::timestamptz as valid_to,
    NOW() as created_at,
    'system-migration' as created_by,
    NOW() as updated_at,
    'system-migration' as updated_by,
    1 as version
FROM account a
WHERE NOT EXISTS (
    -- Don't create duplicate features
    SELECT 1 FROM valuation_features vf
    WHERE vf.account_id = a.id
    AND vf.instrument_code = a.currency
    AND vf.lifecycle_status = 'ACTIVE'
);
*/

-- This migration intentionally left empty - backfill will be done via application code
-- after the Valuation Engine Service is deployed and identity methods are registered.

-- Comment documenting the backfill strategy
COMMENT ON TABLE valuation_features IS
    'Stores valuation method assignments for accounts. Each feature maps an input instrument to the account native instrument using a specific valuation method. Backfill migration postponed until Valuation Engine Service is deployed.';
