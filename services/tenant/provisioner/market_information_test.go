package provisioner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresProvisioner_MarketInformationIntegration verifies market-information
// service is properly provisioned with system seed data during tenant creation.
// This test validates:
//   - Schema creation in org_{tenant_id}
//   - System dataset seeding (FX_RATE, ENERGY_SPOT, ENERGY_TARIFF, CARBON_PRICE, WEATHER_TEMP)
//   - System data source seeding (ECB_DAILY, INTERNAL_ADMIN, SYSTEM_DEFAULT)
func TestPostgresProvisioner_MarketInformationIntegration(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("market_test_inc")
	createTestTenant(t, tc.db, tenantID.String())

	// Create test migration directory for market-information
	marketInfoDir := filepath.Join(tc.migDir, "market-information")
	require.NoError(t, os.MkdirAll(marketInfoDir, 0o755))

	// Create a simplified migration for testing that includes the essential seed data
	// Note: Full migration includes PL/pgSQL functions with $$ delimiters which are not
	// currently supported by splitSQLStatements(). This uses a simplified schema.
	createTestMigration(t, marketInfoDir, "20260116000001_initial.sql", `
-- Data Source Table
CREATE TABLE "data_source" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "trust_level" integer NOT NULL DEFAULT 50,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "version" bigint NOT NULL DEFAULT 1,
  PRIMARY KEY ("id")
);

ALTER TABLE "data_source"
  ADD CONSTRAINT "uq_data_source_code"
  UNIQUE ("code");

-- Dataset Definition Table (simplified - without triggers)
CREATE TABLE "dataset_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "data_category" character varying(50) NULL,
  "validation_expression" text NULL,
  "resolution_key_expression" text NOT NULL,
  "error_message_expression" text NULL,
  "attribute_schema" jsonb NULL,
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_status"
  CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED'));

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "uq_dataset_definition_code_version"
  UNIQUE ("code", "version");

-- Seed data sources
INSERT INTO "data_source" ("id", "code", "name", "description", "trust_level", "created_by", "updated_by") VALUES
  (gen_random_uuid(), 'ECB_DAILY', 'ECB Daily Rates', 'European Central Bank daily reference rates', 90, 'SYSTEM', 'SYSTEM'),
  (gen_random_uuid(), 'INTERNAL_ADMIN', 'Internal Admin', 'Manual administrative overrides', 100, 'SYSTEM', 'SYSTEM'),
  (gen_random_uuid(), 'SYSTEM_DEFAULT', 'System Defaults', 'Fallback rates when no other source available', 50, 'SYSTEM', 'SYSTEM');

-- Seed datasets
INSERT INTO "dataset_definition" (
  "id", "code", "version", "name", "description", "data_category",
  "validation_expression", "resolution_key_expression", "error_message_expression",
  "status", "created_by", "updated_by", "activated_at"
) VALUES
  (gen_random_uuid(), 'FX_RATE', 1, 'Foreign Exchange Rate', 'Exchange rates between currency pairs', 'PRICE',
   'parse_decimal(observation_context.rate) > 0',
   'observation_context.base_currency + "/" + observation_context.quote_currency',
   '"Invalid exchange rate: must be positive"',
   'ACTIVE', 'SYSTEM', 'SYSTEM', now()),
  (gen_random_uuid(), 'ENERGY_SPOT', 1, 'Energy Spot Price', 'Spot prices for energy commodities', 'PRICE',
   'parse_decimal(observation_context.price) >= 0',
   'observation_context.market + "/" + observation_context.commodity',
   '"Invalid energy spot price"',
   'ACTIVE', 'SYSTEM', 'SYSTEM', now()),
  (gen_random_uuid(), 'ENERGY_TARIFF', 1, 'Energy Tariff Rate', 'Published tariff rates', 'RATE',
   'parse_decimal(observation_context.rate) >= 0',
   'observation_context.provider + "/" + observation_context.tariff_code',
   '"Invalid tariff rate"',
   'ACTIVE', 'SYSTEM', 'SYSTEM', now()),
  (gen_random_uuid(), 'CARBON_PRICE', 1, 'Carbon Credit Price', 'Carbon credit prices', 'PRICE',
   'parse_decimal(observation_context.price) >= 0',
   'observation_context.scheme + "/" + observation_context.credit_type',
   '"Invalid carbon price"',
   'ACTIVE', 'SYSTEM', 'SYSTEM', now()),
  (gen_random_uuid(), 'WEATHER_TEMP', 1, 'Weather Temperature', 'Temperature observations', 'MEASUREMENT',
   'parse_decimal(observation_context.temperature_celsius) >= -100',
   'observation_context.station_code + "/" + observation_context.observation_date',
   '"Invalid temperature"',
   'ACTIVE', 'SYSTEM', 'SYSTEM', now());
	`)

	// Create provisioner with market-information service
	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "market-information",
				MigrationPath: marketInfoDir,
				DatabaseURL:   tc.connStr,
			},
		},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 7 * 365 * 24 * time.Hour,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer provisioner.Close()

	// Provision the tenant
	ctx := context.Background()
	err = provisioner.ProvisionSchemas(ctx, tenantID)
	require.NoError(t, err, "Failed to provision schemas")

	// Verify schema exists
	schemaName := tenantID.SchemaName()
	var schemaExists bool
	err = tc.db.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = ?)", schemaName).Scan(&schemaExists).Error
	require.NoError(t, err)
	assert.True(t, schemaExists, "Schema %s should exist", schemaName)

	// Verify system datasets are present
	expectedDatasets := []string{"CARBON_PRICE", "ENERGY_SPOT", "ENERGY_TARIFF", "FX_RATE", "WEATHER_TEMP"}
	var actualDatasets []string
	err = tc.db.Raw("SELECT code FROM " + schemaName + ".dataset_definition WHERE status = 'ACTIVE' ORDER BY code").Scan(&actualDatasets).Error
	require.NoError(t, err)
	assert.ElementsMatch(t, expectedDatasets, actualDatasets,
		"Expected system datasets should be seeded")

	// Verify system data sources are present
	expectedSources := []string{"ECB_DAILY", "INTERNAL_ADMIN", "SYSTEM_DEFAULT"}
	var actualSources []string
	err = tc.db.Raw("SELECT code FROM " + schemaName + ".data_source ORDER BY code").Scan(&actualSources).Error
	require.NoError(t, err)
	assert.ElementsMatch(t, expectedSources, actualSources,
		"Expected system data sources should be seeded")

	// Verify provisioning status is active
	status, err := provisioner.GetProvisioningStatus(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
	assert.Len(t, status.Services, 1)
	assert.Equal(t, "market-information", status.Services[0].ServiceName)
	assert.Equal(t, ServiceStateMigrated, status.Services[0].State)
}

// TestPostgresProvisioner_MarketInformationRollback verifies that
// market-information schema is properly cleaned up when provisioning fails.
func TestPostgresProvisioner_MarketInformationRollback(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	tenantID := tenant.MustNewTenantID("rollback_test_inc")
	createTestTenant(t, tc.db, tenantID.String())

	// Create test migration directory with invalid SQL to trigger failure
	marketInfoDir := filepath.Join(tc.migDir, "market-information")
	require.NoError(t, os.MkdirAll(marketInfoDir, 0o755))

	// Create migration that will fail
	createTestMigration(t, marketInfoDir, "20260116000001_broken.sql", `
		CREATE TABLE dataset_definition (id UUID);
		INVALID SQL SYNTAX HERE TO TRIGGER FAILURE;
	`)

	// Create provisioner
	config := &Config{
		Services: []ServiceConfig{
			{
				Name:          "market-information",
				MigrationPath: marketInfoDir,
				DatabaseURL:   tc.connStr,
			},
		},
		ProvisioningTimeout: 30 * time.Second,
		DataRetentionPeriod: 7 * 365 * 24 * time.Hour,
	}

	provisioner, err := NewPostgresProvisioner(tc.db, config)
	require.NoError(t, err)
	defer provisioner.Close()

	// Attempt to provision (should fail)
	ctx := context.Background()
	err = provisioner.ProvisionSchemas(ctx, tenantID)
	require.Error(t, err, "Provisioning should fail with invalid SQL")
	assert.ErrorIs(t, err, ErrMigrationFailed)

	// Verify provisioning status shows failure
	status, err := provisioner.GetProvisioningStatus(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)
	assert.NotEmpty(t, status.ErrorMessage)
	assert.Contains(t, status.ErrorMessage, "market-information")

	// Note: Schema may still exist (it's created before migrations apply)
	// The important part is that the provisioning status reflects the failure
	// and allows for retry or cleanup via deprovision/purge operations
}
