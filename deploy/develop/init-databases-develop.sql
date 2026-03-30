-- Database initialization for the Meridian develop environment.
--
-- Creates per-service databases in the dedicated develop postgres container.
-- These use the same names as demo (meridian_platform, etc.) because the
-- application hardcodes database names in ServiceDatabases. Data isolation
-- is achieved by running a separate postgres instance, not by name prefixing.

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
CREATE DATABASE meridian_identity;
CREATE DATABASE meridian_operational_gateway;
CREATE DATABASE meridian_financial_gateway;
CREATE DATABASE meridian_control_plane;
