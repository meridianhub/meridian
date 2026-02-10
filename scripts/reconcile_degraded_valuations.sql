-- Reconciliation Query for Degraded Mode Valuation Entries
--
-- This script identifies position entries where valuation was performed in degraded mode
-- (e.g., using stale market data, missing rate sources). These entries may need adjustment
-- once accurate market data becomes available.
--
-- Usage:
--   psql -h <host> -U <user> -d <database> -f scripts/reconcile_degraded_valuations.sql
--
-- Context: Part of Task 11 (valuation-engine.11) - enabling audit trails for atomic valuations

-- Find all position entries with degraded valuation analysis
SELECT
    p.id,
    p.account_id,
    p.instrument_code,
    p.amount,
    p.dimension,
    p.bucket_key,
    p.created_at,
    p.created_by,
    -- Extract valuation analysis metadata
    p.attributes->>'valuation_analysis' AS valuation_analysis_json,
    (p.attributes->>'valuation_analysis')::jsonb->>'method_id' AS method_id,
    (p.attributes->>'valuation_analysis')::jsonb->>'method_version' AS method_version,
    (p.attributes->>'valuation_analysis')::jsonb->>'degraded_mode' AS degraded_mode,
    (p.attributes->>'valuation_analysis')::jsonb->>'degraded_reason' AS degraded_reason,
    (p.attributes->>'valuation_analysis')::jsonb->'applied_rates' AS applied_rates,
    (p.attributes->>'valuation_analysis')::jsonb->>'knowledge_at' AS knowledge_at,
    (p.attributes->>'valuation_analysis')::jsonb->>'computed_at' AS computed_at
FROM positions p
WHERE
    -- Filter for entries with valuation_analysis present
    p.attributes ? 'valuation_analysis'
    -- AND where degraded_mode is explicitly true
    AND (p.attributes->>'valuation_analysis')::jsonb->>'degraded_mode' = 'true'
ORDER BY p.created_at DESC;

-- Summary statistics for degraded valuations
SELECT
    COUNT(*) AS total_degraded_entries,
    COUNT(DISTINCT p.account_id) AS affected_accounts,
    MIN(p.created_at) AS oldest_degraded_entry,
    MAX(p.created_at) AS newest_degraded_entry,
    SUM(p.amount) AS total_degraded_amount
FROM positions p
WHERE
    p.attributes ? 'valuation_analysis'
    AND (p.attributes->>'valuation_analysis')::jsonb->>'degraded_mode' = 'true';

-- Breakdown by method_id to identify which valuation methods are most affected
SELECT
    (p.attributes->>'valuation_analysis')::jsonb->>'method_id' AS method_id,
    COUNT(*) AS entry_count,
    COUNT(DISTINCT p.account_id) AS account_count,
    SUM(p.amount) AS total_amount
FROM positions p
WHERE
    p.attributes ? 'valuation_analysis'
    AND (p.attributes->>'valuation_analysis')::jsonb->>'degraded_mode' = 'true'
GROUP BY method_id
ORDER BY entry_count DESC;
