-- Drop idx_account_syndicate_scope_integrity (party_id, org_party_id, instrument_code).
--
-- The original constraint (PRD-022) assumed one account per party per organisation per
-- currency, modelled on a syndicate where Alice holds a single GBP position in Venture Alpha.
-- That assumption does not hold for utility billing or any multi-service scenario where a
-- party legitimately holds multiple instrument-equivalent accounts under the same supplier:
-- a residential customer needs separate GBP billing accounts for electricity and gas at the
-- same supplier, and the same shape extends to multi-site customers (one billing account per
-- premise) and multi-product offerings.
--
-- Account-identifier uniqueness is preserved by idx_account_account_identification, so
-- dropping this index does not allow accidental account duplication; it only allows the
-- legitimate "Margaret has GBP-elec and GBP-gas with Utilita" pattern.

DROP INDEX IF EXISTS "idx_account_syndicate_scope_integrity";
