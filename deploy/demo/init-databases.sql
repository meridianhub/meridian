-- Create per-service databases for the Meridian unified binary.
-- Postgres docker-entrypoint-initdb.d runs this on first initialisation only.
-- The list must match internal/migrations/runner.go ServiceDatabases.

CREATE DATABASE meridian_platform;
CREATE DATABASE meridian_current_account;
CREATE DATABASE meridian_financial_accounting;
CREATE DATABASE meridian_position_keeping;
CREATE DATABASE meridian_payment_order;
CREATE DATABASE meridian_party;
CREATE DATABASE meridian_internal_account;
CREATE DATABASE meridian_market_information;
CREATE DATABASE meridian_reconciliation;
CREATE DATABASE meridian_forecasting;
CREATE DATABASE meridian_reference_data;
