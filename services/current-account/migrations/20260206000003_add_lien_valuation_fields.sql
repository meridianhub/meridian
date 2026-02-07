-- Add valuation fields to lien table for atomic price lock support.
-- These columns are nullable for backward compatibility: existing liens created
-- before valuation engine integration will have NULL values.

ALTER TABLE lien ADD COLUMN IF NOT EXISTS reserved_quantity JSONB;
ALTER TABLE lien ADD COLUMN IF NOT EXISTS valued_amount JSONB;
ALTER TABLE lien ADD COLUMN IF NOT EXISTS valuation_analysis JSONB;
