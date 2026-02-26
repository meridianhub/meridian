-- Seed master tenant for market-information service shared data access
-- This tenant hosts canonical shared datasets accessible to all tenants
-- Uses ON CONFLICT DO NOTHING for idempotent reruns

INSERT INTO tenant (
    id,
    display_name,
    settlement_asset,
    status,
    metadata,
    version,
    created_at
) VALUES (
    'meridian_master',
    'Meridian Platform',
    'USD',
    'active',
    '{"is_system_tenant": true, "description": "System tenant for shared platform data"}'::jsonb,
    1,
    NOW()
) ON CONFLICT (id) DO NOTHING;
