-- Create per-service databases for the Meridian develop environment.
-- Uses dev_ prefix to isolate from the demo environment's databases.
--
-- Run manually against the demo stack's postgres container:
--   docker exec -i meridian-postgres-1 psql -U meridian < /opt/meridian-develop/init-databases-develop.sql
--
-- The list must match internal/migrations/runner.go ServiceDatabases.

CREATE DATABASE dev_meridian;
CREATE DATABASE dev_meridian_platform;
CREATE DATABASE dev_meridian_current_account;
CREATE DATABASE dev_meridian_financial_accounting;
CREATE DATABASE dev_meridian_position_keeping;
CREATE DATABASE dev_meridian_payment_order;
CREATE DATABASE dev_meridian_party;
CREATE DATABASE dev_meridian_internal_bank_account;
CREATE DATABASE dev_meridian_market_information;
CREATE DATABASE dev_meridian_reconciliation;
CREATE DATABASE dev_meridian_forecasting;
CREATE DATABASE dev_meridian_reference_data;
CREATE DATABASE dev_meridian_identity;
CREATE DATABASE dev_meridian_operational_gateway;
CREATE DATABASE dev_meridian_financial_gateway;
