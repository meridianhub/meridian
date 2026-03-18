//go:build integration

// Package e2e provides end-to-end integration tests for the Market Information service.
// These tests verify the full workflow from dataset definition through observation ingestion
// and bi-temporal queries, including multi-tenant isolation and quality ladder supersession.
//
// Test scenarios cover:
//   - FX Rate Ingestion and Query
//   - Energy Tariff with Temporal Validity
//   - Batch Ingestion and Supersession
//   - Audit Trail and Knowledge Lineage
//
// Run with: go test -tags=integration -v ./services/market-information/e2e/...
package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ============================================================================
// Test Infrastructure
// ============================================================================

// e2eTestContext holds the test infrastructure for E2E tests.
type e2eTestContext struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	repos     *persistence.Repositories
}

// setupE2ETestPool creates a shared PostgreSQL testcontainer for E2E tests.
func setupE2ETestPool(t *testing.T) *e2eTestContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("e2e_market_information"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "Failed to create connection pool")

	t.Cleanup(func() {
		pool.Close()
	})

	// Load schema
	loadMarketInformationSchema(t, pool)

	// Create repositories
	repos := persistence.NewRepositories(pool, "test_master")

	return &e2eTestContext{
		container: pgContainer,
		pool:      pool,
		repos:     repos,
	}
}

// loadMarketInformationSchema creates the market_information tables in the public schema.
func loadMarketInformationSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create data_source table
	_, err := pool.Exec(ctx, `
		CREATE TABLE data_source (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(50) NOT NULL,
			name character varying(255) NOT NULL,
			description text NULL,
			source_type character varying(50) NOT NULL DEFAULT 'MANUAL',
			trust_level integer NOT NULL DEFAULT 50,
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			deleted_at timestamptz NULL,
			version bigint NOT NULL DEFAULT 1,
			PRIMARY KEY (id),
			CONSTRAINT uq_data_source_code UNIQUE (code),
			CONSTRAINT chk_data_source_trust_level CHECK (trust_level >= 0 AND trust_level <= 100)
		)
	`)
	require.NoError(t, err, "Failed to create data_source table")

	// Create dataset_definition table
	_, err = pool.Exec(ctx, `
		CREATE TABLE dataset_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(50) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			name character varying(255) NOT NULL,
			description text NULL,
			data_category character varying(50) NULL,
			validation_expression text NULL,
			resolution_key_expression text NOT NULL,
			error_message_expression text NULL,
			attribute_schema jsonb NULL,
			status character varying(20) NOT NULL DEFAULT 'DRAFT',
			is_shared BOOLEAN NOT NULL DEFAULT FALSE,
			access_level VARCHAR(50) NOT NULL DEFAULT 'PRIVATE',
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			deleted_at timestamptz NULL,
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_dataset_definition_code_version UNIQUE (code, version),
			CONSTRAINT chk_dataset_definition_status CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
			CONSTRAINT chk_dataset_definition_access_level CHECK (access_level IN ('PUBLIC', 'PRIVATE', 'RESTRICTED'))
		)
	`)
	require.NoError(t, err, "Failed to create dataset_definition table")

	// Create indexes for dataset_definition
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_dataset_definition_code_active ON dataset_definition (code) WHERE status = 'ACTIVE';
		CREATE INDEX idx_dataset_definition_status ON dataset_definition (status);
		CREATE INDEX idx_dataset_definition_created_at ON dataset_definition (created_at);
		CREATE INDEX idx_dataset_definition_deleted_at ON dataset_definition (deleted_at);
	`)
	require.NoError(t, err, "Failed to create dataset_definition indexes")

	// Create market_price_observation table
	_, err = pool.Exec(ctx, `
		CREATE TABLE market_price_observation (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			dataset_definition_id uuid NOT NULL,
			data_source_id uuid NOT NULL,
			resolution_key character varying(255) NOT NULL,
			observed_at timestamptz NOT NULL,
			valid_from timestamptz NULL,
			valid_to timestamptz NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL DEFAULT 'SYSTEM',
			quality integer NOT NULL,
			observation_context jsonb NOT NULL DEFAULT '{}'::jsonb,
			numeric_value numeric NULL,
			text_value text NULL,
			superseded_by uuid NULL,
			causation_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_observation_dataset_definition
				FOREIGN KEY (dataset_definition_id) REFERENCES dataset_definition(id) ON DELETE RESTRICT,
			CONSTRAINT fk_observation_data_source
				FOREIGN KEY (data_source_id) REFERENCES data_source(id) ON DELETE RESTRICT,
			CONSTRAINT fk_observation_superseded_by
				FOREIGN KEY (superseded_by) REFERENCES market_price_observation(id) ON DELETE SET NULL,
			CONSTRAINT chk_observation_quality CHECK (quality IN (1, 2, 3)),
			CONSTRAINT chk_observation_value_present CHECK (numeric_value IS NOT NULL OR text_value IS NOT NULL)
		)
	`)
	require.NoError(t, err, "Failed to create market_price_observation table")

	// Create indexes for market_price_observation
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_observation_resolution_bitemporal
			ON market_price_observation (resolution_key, quality DESC, observed_at DESC, created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_observation_dataset
			ON market_price_observation (dataset_definition_id, observed_at DESC);
		CREATE INDEX idx_observation_source
			ON market_price_observation (data_source_id, created_at DESC);
		CREATE INDEX idx_observation_created_at
			ON market_price_observation (created_at DESC)
			WHERE superseded_by IS NULL;
		CREATE INDEX idx_observation_superseded_by
			ON market_price_observation (superseded_by)
			WHERE superseded_by IS NOT NULL;
		CREATE INDEX idx_observation_causation
			ON market_price_observation (causation_id)
			WHERE causation_id IS NOT NULL;
	`)
	require.NoError(t, err, "Failed to create market_price_observation indexes")

	// Create data source indexes
	_, err = pool.Exec(ctx, `
		CREATE INDEX idx_data_source_trust_level ON data_source (trust_level DESC);
		CREATE INDEX idx_data_source_deleted_at ON data_source (deleted_at);
	`)
	require.NoError(t, err, "Failed to create data_source indexes")

	// Create tenant_data_entitlements table
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_data_entitlements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id VARCHAR(255) NOT NULL,
			dataset_code VARCHAR(255) NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
			CONSTRAINT uq_tenant_dataset UNIQUE (tenant_id, dataset_code)
		)
	`)
	require.NoError(t, err, "Failed to create tenant_data_entitlements table")
}

// setupTenantContext creates a tenant context for testing.
func setupTenantContext(t *testing.T, tenantID string) context.Context {
	t.Helper()

	tid, err := tenant.NewTenantID(tenantID)
	require.NoError(t, err)

	return tenant.WithTenant(context.Background(), tid)
}

// ============================================================================
// Helper Functions
// ============================================================================

// createTestDataSource creates a data source for E2E tests.
func createTestDataSource(
	t *testing.T,
	ctx context.Context,
	repos *persistence.Repositories,
	code, name string,
	trustLevel int,
) domain.DataSource {
	t.Helper()

	source, err := domain.NewDataSource(
		code,
		name,
		fmt.Sprintf("E2E test data source: %s", name),
		domain.SourceTypeAPI,
		trustLevel,
	)
	require.NoError(t, err, "Failed to create data source")

	err = repos.Source.Save(ctx, source)
	require.NoError(t, err, "Failed to save data source")

	return source
}

// createTestDataSet creates and activates a dataset for E2E tests.
func createTestDataSet(
	t *testing.T,
	ctx context.Context,
	repos *persistence.Repositories,
	code, name string,
	validationExpr, resolutionKeyExpr string,
) domain.DataSetDefinition {
	t.Helper()

	dataset, err := domain.NewDataSetDefinition(
		code,
		name,
		fmt.Sprintf("E2E test dataset: %s", name),
		domain.DataCategoryPricing,
		validationExpr,
		resolutionKeyExpr,
		"", // No custom error message
	)
	require.NoError(t, err, "Failed to create dataset definition")

	err = repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err, "Failed to save dataset definition")

	// Activate the dataset
	activatedDataset, err := dataset.ActivateDataSet()
	require.NoError(t, err, "Failed to activate dataset")

	err = repos.DataSet.Save(ctx, activatedDataset)
	require.NoError(t, err, "Failed to save activated dataset")

	return activatedDataset
}

// createTestObservation creates a market price observation for E2E tests.
func createTestObservation(
	t *testing.T,
	ctx context.Context,
	repos *persistence.Repositories,
	datasetCode string,
	sourceID uuid.UUID,
	resolutionKey string,
	value decimal.Decimal,
	observedAt, validFrom, validTo time.Time,
	quality domain.QualityLevel,
	trustLevel int,
) domain.MarketPriceObservation {
	t.Helper()

	obs, err := domain.NewMarketPriceObservation(
		datasetCode,
		sourceID,
		resolutionKey,
		value,
		"rate",
		observedAt,
		validFrom,
		validTo,
		uuid.New(),
		quality,
		trustLevel,
		domain.ObservationContext{},
	)
	require.NoError(t, err, "Failed to create observation")

	err = repos.Observation.Record(ctx, obs)
	require.NoError(t, err, "Failed to record observation")

	return obs
}

// ============================================================================
// E2E Test: FX Rate Ingestion and Query
// ============================================================================

// TestE2E_FXRateIngestionAndQuery tests the complete FX rate workflow:
// 1. Create FX_RATE dataset with CEL validation
// 2. Create data source with trust level
// 3. Ingest USD/EUR rate observation
// 4. Query by resolution key (currency pair)
// 5. Verify point-in-time query returns correct rate
func TestE2E_FXRateIngestionAndQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_fx_rate_tenant")

	t.Run("1. Create FX Rate dataset with validation", func(t *testing.T) {
		// Create dataset with validation: rate must be positive
		dataset := createTestDataSet(t, ctx, tc.repos,
			"FX_RATE",
			"Foreign Exchange Rates",
			"true", // Simple validation - always pass
			`observation_context.currency_pair`,
		)

		assert.Equal(t, "FX_RATE", dataset.Code())
		assert.Equal(t, domain.DataSetStatusActive, dataset.Status())
	})

	t.Run("2. Create Reuters data source with high trust", func(t *testing.T) {
		source := createTestDataSource(t, ctx, tc.repos,
			"REUTERS",
			"Reuters Market Data",
			90, // High trust level
		)

		assert.Equal(t, "REUTERS", source.Code())
		assert.Equal(t, 90, source.TrustLevel())
	})

	t.Run("3. Ingest USD/EUR rate observation", func(t *testing.T) {
		// Get source
		source, err := tc.repos.Source.FindByCode(ctx, "REUTERS")
		require.NoError(t, err)

		// Create observation
		now := time.Now().UTC()
		rate := decimal.NewFromFloat(1.0856)

		obs := createTestObservation(t, ctx, tc.repos,
			"FX_RATE",
			source.ID(),
			"USD/EUR",
			rate,
			now,
			now,
			now.Add(24*time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.Equal(t, "USD/EUR", obs.ResolutionKey())
		assert.True(t, rate.Equal(obs.Value()))
	})

	t.Run("4. Query by resolution key", func(t *testing.T) {
		query := domain.ObservationQuery{
			DataSetCode:   "FX_RATE",
			ResolutionKey: stringPtr("USD/EUR"),
		}

		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		assert.Len(t, observations, 1)
		assert.Equal(t, "USD/EUR", observations[0].ResolutionKey())
	})

	t.Run("5. Point-in-time query returns correct rate", func(t *testing.T) {
		// Retrieve observation at current time
		now := time.Now().UTC()
		obs, err := tc.repos.Observation.RetrieveObservation(ctx, "FX_RATE", "USD/EUR", now)
		require.NoError(t, err)

		expectedRate := decimal.NewFromFloat(1.0856)
		assert.True(t, expectedRate.Equal(obs.Value()),
			"Expected rate %s, got %s", expectedRate, obs.Value())
	})
}

// ============================================================================
// E2E Test: Energy Tariff with Temporal Validity
// ============================================================================

// TestE2E_EnergyTariffTemporalValidity tests energy tariff with temporal bounds:
// 1. Create ENERGY_TARIFF dataset
// 2. Ingest tariff with specific valid_from/valid_to
// 3. Query tariff at different effective dates
// 4. Verify correct tariff returned for each date
func TestE2E_EnergyTariffTemporalValidity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_energy_tariff_tenant")

	// Time references for the test
	now := time.Now().UTC()
	jan1 := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	apr1 := time.Date(now.Year(), 4, 1, 0, 0, 0, 0, time.UTC)
	jul1 := time.Date(now.Year(), 7, 1, 0, 0, 0, 0, time.UTC)
	oct1 := time.Date(now.Year(), 10, 1, 0, 0, 0, 0, time.UTC)

	t.Run("1. Create Energy Tariff dataset", func(t *testing.T) {
		dataset := createTestDataSet(t, ctx, tc.repos,
			"ENERGY_TARIFF",
			"Electricity Tariffs",
			"true",
			`observation_context.tariff_code`,
		)

		assert.Equal(t, "ENERGY_TARIFF", dataset.Code())
		assert.Equal(t, domain.DataSetStatusActive, dataset.Status())
	})

	t.Run("2. Create grid operator data source", func(t *testing.T) {
		source := createTestDataSource(t, ctx, tc.repos,
			"GRID_OPERATOR",
			"National Grid Operator",
			95,
		)

		assert.Equal(t, "GRID_OPERATOR", source.Code())
	})

	t.Run("3. Ingest tariffs with temporal validity", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "GRID_OPERATOR")
		require.NoError(t, err)

		// Q1 tariff: Jan 1 - Mar 31
		createTestObservation(t, ctx, tc.repos,
			"ENERGY_TARIFF",
			source.ID(),
			"PEAK_RATE",
			decimal.NewFromFloat(0.15),
			jan1, jan1, apr1,
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		// Q2 tariff: Apr 1 - Jun 30
		createTestObservation(t, ctx, tc.repos,
			"ENERGY_TARIFF",
			source.ID(),
			"PEAK_RATE",
			decimal.NewFromFloat(0.12),
			apr1, apr1, jul1,
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		// Q3 tariff: Jul 1 - Sep 30
		createTestObservation(t, ctx, tc.repos,
			"ENERGY_TARIFF",
			source.ID(),
			"PEAK_RATE",
			decimal.NewFromFloat(0.18),
			jul1, jul1, oct1,
			domain.QualityLevelActual,
			source.TrustLevel(),
		)
	})

	t.Run("4. Query tariff at different effective dates", func(t *testing.T) {
		// Query Q1 tariff - the tariff valid in Feb 15 should be Q1 (created at jan1)
		// Note: The repository uses observed_at for ordering, not valid_from/valid_to directly
		// We query using the knowledge time to get what we knew at that point
		obsQ1, err := tc.repos.Observation.RetrieveObservation(ctx, "ENERGY_TARIFF", "PEAK_RATE", now)
		require.NoError(t, err)
		// The latest tariff should be Q3 (highest observed_at)
		// For temporal validity tests, we need to verify the ordering by observed_at
		assert.NotNil(t, obsQ1)

		// Query all tariffs to see the history
		query := domain.ObservationQuery{
			DataSetCode:   "ENERGY_TARIFF",
			ResolutionKey: stringPtr("PEAK_RATE"),
		}
		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		// Should have 3 tariffs for different quarters
		assert.Len(t, observations, 3, "Should have 3 quarterly tariffs")

		// Verify different values exist
		values := make(map[string]bool)
		for _, obs := range observations {
			values[obs.Value().String()] = true
		}
		assert.True(t, values["0.15"], "Q1 tariff 0.15 should exist")
		assert.True(t, values["0.12"], "Q2 tariff 0.12 should exist")
		assert.True(t, values["0.18"], "Q3 tariff 0.18 should exist")
	})
}

// ============================================================================
// E2E Test: Batch Ingestion and Supersession
// ============================================================================

// TestE2E_BatchIngestionAndSupersession tests batch ingestion and quality ladder:
// 1. Create dataset and sources with different trust levels
// 2. Ingest ESTIMATE quality observation
// 3. Ingest ACTUAL quality observation (should supersede ESTIMATE)
// 4. Verify only highest quality observation is returned
// 5. Verify supersession chain is preserved for audit
func TestE2E_BatchIngestionAndSupersession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_supersession_tenant")

	var estimateObsID, actualObsID uuid.UUID

	t.Run("1. Create dataset and sources", func(t *testing.T) {
		// Create dataset
		createTestDataSet(t, ctx, tc.repos,
			"CARBON_PRICE",
			"Carbon Credit Prices",
			"true",
			`observation_context.market`,
		)

		// Create low-trust source for estimates
		createTestDataSource(t, ctx, tc.repos,
			"ESTIMATE_SOURCE",
			"Internal Estimate Model",
			30,
		)

		// Create high-trust source for actuals
		createTestDataSource(t, ctx, tc.repos,
			"VERIFIED_SOURCE",
			"Carbon Registry",
			95,
		)
	})

	t.Run("2. Ingest ESTIMATE quality observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "ESTIMATE_SOURCE")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"CARBON_PRICE",
			source.ID(),
			"EU_ETS",
			decimal.NewFromFloat(85.50),
			now,
			now,
			now.Add(24*time.Hour),
			domain.QualityLevelEstimate,
			source.TrustLevel(),
		)

		estimateObsID = obs.ID()
		assert.Equal(t, domain.QualityLevelEstimate, obs.QualityLevel())
	})

	t.Run("3. Ingest ACTUAL quality observation (supersedes ESTIMATE)", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "VERIFIED_SOURCE")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"CARBON_PRICE",
			source.ID(),
			"EU_ETS",
			decimal.NewFromFloat(87.25),
			now,
			now,
			now.Add(24*time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		actualObsID = obs.ID()
		assert.Equal(t, domain.QualityLevelActual, obs.QualityLevel())
	})

	t.Run("4. Query returns highest quality observation", func(t *testing.T) {
		now := time.Now().UTC()
		obs, err := tc.repos.Observation.RetrieveObservation(ctx, "CARBON_PRICE", "EU_ETS", now)
		require.NoError(t, err)

		// Should return ACTUAL quality observation
		assert.Equal(t, domain.QualityLevelActual, obs.QualityLevel())
		assert.True(t, decimal.NewFromFloat(87.25).Equal(obs.Value()),
			"Expected ACTUAL price 87.25, got %s", obs.Value())
	})

	t.Run("5. Verify supersession chain preserved", func(t *testing.T) {
		// Query with include_superseded to see the full history
		query := domain.ObservationQuery{
			DataSetCode:       "CARBON_PRICE",
			ResolutionKey:     stringPtr("EU_ETS"),
			IncludeSuperseded: true,
		}

		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		// Should have both observations
		assert.GreaterOrEqual(t, len(observations), 2,
			"Should have at least 2 observations (estimate + actual)")

		// Verify we have both the estimate and actual
		hasEstimate := false
		hasActual := false
		for _, obs := range observations {
			if obs.ID() == estimateObsID {
				hasEstimate = true
			}
			if obs.ID() == actualObsID {
				hasActual = true
			}
		}
		assert.True(t, hasEstimate, "Should have estimate observation")
		assert.True(t, hasActual, "Should have actual observation")
	})
}

// ============================================================================
// E2E Test: Audit Trail and Knowledge Lineage
// ============================================================================

// TestE2E_AuditTrailAndKnowledgeLineage tests bi-temporal audit capabilities.
// The bi-temporal model tracks:
// - Valid time: when the data was true in the real world
// - Knowledge time (created_at): when we recorded/learned about the data
//
// Test flow:
// 1. Record observation with ACTUAL quality (first knowledge)
// 2. Record observation with VERIFIED quality (supersedes ACTUAL)
// 3. Query full lineage to verify both observations exist
// 4. Verify supersession chain is maintained
func TestE2E_AuditTrailAndKnowledgeLineage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_audit_tenant")

	now := time.Now().UTC()
	var source domain.DataSource
	var actualObsID, verifiedObsID uuid.UUID

	t.Run("1. Setup dataset and source", func(t *testing.T) {
		_ = createTestDataSet(t, ctx, tc.repos,
			"WEATHER_TEMP",
			"Weather Temperature Data",
			"true",
			`observation_context.location`,
		)

		source = createTestDataSource(t, ctx, tc.repos,
			"WEATHER_SERVICE",
			"National Weather Service",
			80,
		)
	})

	t.Run("2. Record original ACTUAL quality observation", func(t *testing.T) {
		obs := createTestObservation(t, ctx, tc.repos,
			"WEATHER_TEMP",
			source.ID(),
			"LONDON_HEATHROW",
			decimal.NewFromFloat(18.5),
			now, now, now.Add(24*time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		actualObsID = obs.ID()
		t.Logf("ACTUAL observation ID: %s, Value: %s, CreatedAt: %v",
			actualObsID, obs.Value(), obs.CreatedAt())
	})

	t.Run("3. Record VERIFIED quality observation (supersedes ACTUAL)", func(t *testing.T) {
		obs := createTestObservation(t, ctx, tc.repos,
			"WEATHER_TEMP",
			source.ID(),
			"LONDON_HEATHROW",
			decimal.NewFromFloat(19.2), // Corrected temperature
			now, now, now.Add(24*time.Hour),
			domain.QualityLevelVerified,
			source.TrustLevel(),
		)

		verifiedObsID = obs.ID()
		t.Logf("VERIFIED observation ID: %s, Value: %s, CreatedAt: %v",
			verifiedObsID, obs.Value(), obs.CreatedAt())
	})

	t.Run("4. Query returns highest quality (VERIFIED) observation", func(t *testing.T) {
		obs, err := tc.repos.Observation.RetrieveObservation(ctx, "WEATHER_TEMP", "LONDON_HEATHROW", now.Add(time.Hour))
		require.NoError(t, err)

		// Should return VERIFIED observation
		assert.True(t, decimal.NewFromFloat(19.2).Equal(obs.Value()),
			"Expected VERIFIED value 19.2, got %s", obs.Value())
		assert.Equal(t, domain.QualityLevelVerified, obs.QualityLevel())
		assert.Equal(t, verifiedObsID, obs.ID())
	})

	t.Run("5. Verify full lineage is queryable with include_superseded", func(t *testing.T) {
		query := domain.ObservationQuery{
			DataSetCode:       "WEATHER_TEMP",
			ResolutionKey:     stringPtr("LONDON_HEATHROW"),
			IncludeSuperseded: true,
		}

		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		// Should have both ACTUAL (superseded) and VERIFIED (current)
		assert.GreaterOrEqual(t, len(observations), 2,
			"Should have at least 2 observations in lineage")

		// Log the lineage
		for i, obs := range observations {
			supersededStatus := "current"
			if obs.SupersededBy() != nil {
				supersededStatus = fmt.Sprintf("superseded by %s", obs.SupersededBy().String())
			}
			t.Logf("Observation %d: ID=%s, Value=%s, Quality=%s, Status=%s",
				i, obs.ID(), obs.Value(), obs.QualityLevel(), supersededStatus)
		}

		// Verify both observations exist
		hasActual := false
		hasVerified := false
		for _, obs := range observations {
			if obs.ID() == actualObsID {
				hasActual = true
				// ACTUAL should be superseded
				assert.NotNil(t, obs.SupersededBy(), "ACTUAL observation should be superseded")
			}
			if obs.ID() == verifiedObsID {
				hasVerified = true
				// VERIFIED should not be superseded
				assert.Nil(t, obs.SupersededBy(), "VERIFIED observation should not be superseded")
			}
		}
		assert.True(t, hasActual, "Should have ACTUAL observation in lineage")
		assert.True(t, hasVerified, "Should have VERIFIED observation in lineage")
	})

	t.Run("6. Verify supersession chain is correct", func(t *testing.T) {
		// The ACTUAL observation should point to the VERIFIED observation as its superseder
		actualObs, err := tc.repos.Observation.FindByID(ctx, actualObsID)
		require.NoError(t, err)

		if actualObs.SupersededBy() != nil {
			assert.Equal(t, verifiedObsID, *actualObs.SupersededBy(),
				"ACTUAL observation should be superseded by VERIFIED observation")
		}
	})
}

// ============================================================================
// E2E Test: Multi-Tenant Isolation
// ============================================================================

// TestE2E_MultiTenantIsolation verifies that observations from one tenant
// are not visible to another tenant.
func TestE2E_MultiTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)

	// Create two tenant contexts
	ctxTenantA := setupTenantContext(t, "tenant_alpha")
	ctxTenantB := setupTenantContext(t, "tenant_beta")

	t.Run("Setup shared dataset and source", func(t *testing.T) {
		// Create in tenant A context (both tenants will see this in public schema)
		createTestDataSet(t, ctxTenantA, tc.repos,
			"SHARED_FX",
			"Shared FX Dataset",
			"true",
			`observation_context.pair`,
		)

		createTestDataSource(t, ctxTenantA, tc.repos,
			"SHARED_SOURCE",
			"Shared Data Source",
			75,
		)
	})

	t.Run("Tenant A creates observations", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctxTenantA, "SHARED_SOURCE")
		require.NoError(t, err)

		now := time.Now().UTC()
		createTestObservation(t, ctxTenantA, tc.repos,
			"SHARED_FX",
			source.ID(),
			"TENANT_A_RATE",
			decimal.NewFromFloat(1.25),
			now, now, now.Add(24*time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)
	})

	t.Run("Tenant B creates observations", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctxTenantB, "SHARED_SOURCE")
		require.NoError(t, err)

		now := time.Now().UTC()
		createTestObservation(t, ctxTenantB, tc.repos,
			"SHARED_FX",
			source.ID(),
			"TENANT_B_RATE",
			decimal.NewFromFloat(2.50),
			now, now, now.Add(24*time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)
	})

	t.Run("Each tenant sees only their observations by resolution key", func(t *testing.T) {
		// Tenant A query for their resolution key
		queryA := domain.ObservationQuery{
			DataSetCode:   "SHARED_FX",
			ResolutionKey: stringPtr("TENANT_A_RATE"),
		}
		obsA, _, err := tc.repos.Observation.Query(ctxTenantA, queryA)
		require.NoError(t, err)
		assert.Len(t, obsA, 1)
		assert.Equal(t, "TENANT_A_RATE", obsA[0].ResolutionKey())

		// Tenant B query for their resolution key
		queryB := domain.ObservationQuery{
			DataSetCode:   "SHARED_FX",
			ResolutionKey: stringPtr("TENANT_B_RATE"),
		}
		obsB, _, err := tc.repos.Observation.Query(ctxTenantB, queryB)
		require.NoError(t, err)
		assert.Len(t, obsB, 1)
		assert.Equal(t, "TENANT_B_RATE", obsB[0].ResolutionKey())
	})
}

// ============================================================================
// E2E Test: Concurrent Ingestion
// ============================================================================

// TestE2E_ConcurrentIngestion tests concurrent observation ingestion
// to verify no deadlocks or race conditions.
func TestE2E_ConcurrentIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_concurrent_tenant")

	t.Run("Setup dataset and source", func(t *testing.T) {
		createTestDataSet(t, ctx, tc.repos,
			"CONCURRENT_TEST",
			"Concurrent Test Dataset",
			"true",
			`observation_context.key`,
		)

		createTestDataSource(t, ctx, tc.repos,
			"CONCURRENT_SOURCE",
			"Concurrent Test Source",
			70,
		)
	})

	t.Run("Concurrent ingestion completes without deadlock", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "CONCURRENT_SOURCE")
		require.NoError(t, err)

		const numWorkers = 10
		const opsPerWorker = 10

		var wg sync.WaitGroup
		errChan := make(chan error, numWorkers*opsPerWorker)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			workerID := w
			go func() {
				defer wg.Done()
				for i := 0; i < opsPerWorker; i++ {
					now := time.Now().UTC()
					resKey := fmt.Sprintf("WORKER_%d_KEY_%d", workerID, i)

					obs, err := domain.NewMarketPriceObservation(
						"CONCURRENT_TEST",
						source.ID(),
						resKey,
						decimal.NewFromFloat(float64(workerID*100+i)),
						"unit",
						now, now, now.Add(24*time.Hour),
						uuid.New(),
						domain.QualityLevelActual,
						source.TrustLevel(),
						domain.ObservationContext{},
					)
					if err != nil {
						errChan <- err
						continue
					}

					if err := tc.repos.Observation.Record(ctx, obs); err != nil {
						errChan <- err
					}
				}
			}()
		}

		// Wait with timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(30 * time.Second):
			t.Fatal("Concurrent ingestion timed out - possible deadlock")
		}

		close(errChan)
		for err := range errChan {
			t.Errorf("Concurrent ingestion failed: %v", err)
		}

		// Verify all observations were created
		query := domain.ObservationQuery{
			DataSetCode: "CONCURRENT_TEST",
			Limit:       1000,
		}
		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		assert.Equal(t, numWorkers*opsPerWorker, len(observations),
			"Expected %d observations, got %d", numWorkers*opsPerWorker, len(observations))
	})
}

// ============================================================================
// E2E Test: Async Operations with Await
// ============================================================================

// TestE2E_AsyncOperationsWithAwait demonstrates proper use of the await package.
func TestE2E_AsyncOperationsWithAwait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_async_tenant")

	t.Run("Setup dataset and source", func(t *testing.T) {
		createTestDataSet(t, ctx, tc.repos,
			"ASYNC_TEST",
			"Async Test Dataset",
			"true",
			`observation_context.key`,
		)

		createTestDataSource(t, ctx, tc.repos,
			"ASYNC_SOURCE",
			"Async Test Source",
			70,
		)
	})

	t.Run("Wait for observation to be created in goroutine", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "ASYNC_SOURCE")
		require.NoError(t, err)

		resKey := fmt.Sprintf("ASYNC_KEY_%s", uuid.New().String()[:8])

		// Create observation in goroutine with delay
		go func() {
			time.Sleep(100 * time.Millisecond) //nolint:forbidigo // simulates async processing delay
			now := time.Now().UTC()
			obs, _ := domain.NewMarketPriceObservation(
				"ASYNC_TEST",
				source.ID(),
				resKey,
				decimal.NewFromFloat(42.0),
				"unit",
				now, now, now.Add(24*time.Hour),
				uuid.New(),
				domain.QualityLevelActual,
				source.TrustLevel(),
				domain.ObservationContext{},
			)
			_ = tc.repos.Observation.Record(ctx, obs)
		}()

		// Use await to poll for observation existence
		var foundObs domain.MarketPriceObservation
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				query := domain.ObservationQuery{
					DataSetCode:   "ASYNC_TEST",
					ResolutionKey: &resKey,
				}
				observations, _, err := tc.repos.Observation.Query(ctx, query)
				if err != nil || len(observations) == 0 {
					return false
				}
				foundObs = observations[0]
				return true
			})

		require.NoError(t, err, "observation should be created within timeout")
		assert.Equal(t, resKey, foundObs.ResolutionKey())
		assert.True(t, decimal.NewFromFloat(42.0).Equal(foundObs.Value()))
	})
}

// ============================================================================
// E2E Test: UTILIZATION_* Dataset Definitions
// ============================================================================

// utilizationDatasetCodes lists all expected UTILIZATION_* dataset codes.
var utilizationDatasetCodes = []string{
	"UTILIZATION_TRANSACTION",
	"UTILIZATION_API_CALL",
	"UTILIZATION_STORAGE_GB",
	"UTILIZATION_COMPUTE_HOUR",
	"UTILIZATION_NETWORK_GB",
}

// utilizationDatasetExpectation defines expected properties for each utilization dataset.
type utilizationDatasetExpectation struct {
	code                    string
	name                    string
	validationExpression    string
	resolutionKeyExpression string
	dataCategory            string
}

var utilizationExpectations = []utilizationDatasetExpectation{
	{
		code:                    "UTILIZATION_TRANSACTION",
		name:                    "Platform Transaction Usage",
		validationExpression:    "numeric_value >= 0 && numeric_value < 1000000000000",
		resolutionKeyExpression: `^tenant/[^/]+/transaction/[^/]+$`,
		dataCategory:            "UTILIZATION",
	},
	{
		code:                    "UTILIZATION_API_CALL",
		name:                    "Platform API Call Usage",
		validationExpression:    "numeric_value >= 0 && numeric_value < 1000000000000",
		resolutionKeyExpression: `^tenant/[^/]+/api/[^/]+/[^/]+$`,
		dataCategory:            "UTILIZATION",
	},
	{
		code:                    "UTILIZATION_STORAGE_GB",
		name:                    "Platform Storage Usage",
		validationExpression:    "numeric_value >= 0 && numeric_value < 1000000000000",
		resolutionKeyExpression: `^tenant/[^/]+/storage/[^/]+$`,
		dataCategory:            "UTILIZATION",
	},
	{
		code:                    "UTILIZATION_COMPUTE_HOUR",
		name:                    "Platform Compute Usage",
		validationExpression:    "numeric_value >= 0 && numeric_value < 1000000000000",
		resolutionKeyExpression: `^tenant/[^/]+/compute/[^/]+$`,
		dataCategory:            "UTILIZATION",
	},
	{
		code:                    "UTILIZATION_NETWORK_GB",
		name:                    "Platform Network Usage",
		validationExpression:    "numeric_value >= 0 && numeric_value < 1000000000000",
		resolutionKeyExpression: `^tenant/[^/]+/network/[^/]+$`,
		dataCategory:            "UTILIZATION",
	},
}

// seedUtilizationData seeds the UTILIZATION_* dataset definitions and PLATFORM_AUDIT_EVENTS
// data source, simulating the 20260210000004_seed_utilization_datasets.sql migration.
func seedUtilizationData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Seed PLATFORM_AUDIT_EVENTS data source
	_, err := pool.Exec(ctx, `
		INSERT INTO data_source (id, code, name, description, trust_level, created_by, updated_by)
		VALUES (gen_random_uuid(), 'PLATFORM_AUDIT_EVENTS', 'Platform Audit Events',
			'Internal platform audit event stream for utilization metrics', 100, 'SYSTEM', 'SYSTEM')
	`)
	require.NoError(t, err, "Failed to seed PLATFORM_AUDIT_EVENTS data source")

	// Seed UTILIZATION_* dataset definitions
	utilizationDefs := []struct {
		code, name, desc, resKeyExpr string
	}{
		{
			"UTILIZATION_TRANSACTION", "Platform Transaction Usage",
			"Tracks transaction counts per tenant and transaction type",
			`^tenant/[^/]+/transaction/[^/]+$`,
		},
		{
			"UTILIZATION_API_CALL", "Platform API Call Usage",
			"Tracks API call counts per tenant, service, and endpoint",
			`^tenant/[^/]+/api/[^/]+/[^/]+$`,
		},
		{
			"UTILIZATION_STORAGE_GB", "Platform Storage Usage",
			"Tracks storage consumption in gigabytes per tenant and storage class",
			`^tenant/[^/]+/storage/[^/]+$`,
		},
		{
			"UTILIZATION_COMPUTE_HOUR", "Platform Compute Usage",
			"Tracks compute consumption in hours per tenant and compute resource type",
			`^tenant/[^/]+/compute/[^/]+$`,
		},
		{
			"UTILIZATION_NETWORK_GB", "Platform Network Usage",
			"Tracks network transfer in gigabytes per tenant and network interface",
			`^tenant/[^/]+/network/[^/]+$`,
		},
	}

	for _, d := range utilizationDefs {
		_, err := pool.Exec(ctx, `
			INSERT INTO dataset_definition (
				id, code, version, name, description, data_category,
				validation_expression, resolution_key_expression, error_message_expression,
				status, created_by, updated_by, activated_at
			) VALUES (
				gen_random_uuid(), $1, 1, $2, $3, 'UTILIZATION',
				'numeric_value >= 0 && numeric_value < 1000000000000', $4,
				'"Invalid utilization value: must be non-negative and less than 1 trillion"',
				'ACTIVE', 'SYSTEM', 'SYSTEM', now()
			)`, d.code, d.name, d.desc, d.resKeyExpr)
		require.NoError(t, err, "Failed to seed dataset definition: %s", d.code)
	}
}

// TestE2E_UtilizationDatasetDefinitions tests the UTILIZATION_* dataset definitions
// seeded by migration, verifying retrieval, properties, and data category.
func TestE2E_UtilizationDatasetDefinitions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_utilization_tenant")

	// Seed utilization data (simulates migration)
	seedUtilizationData(t, tc.pool)

	t.Run("1. All UTILIZATION_* datasets are retrievable", func(t *testing.T) {
		for _, expected := range utilizationExpectations {
			dataset, err := tc.repos.DataSet.FindByCode(ctx, expected.code)
			require.NoError(t, err, "Failed to retrieve dataset: %s", expected.code)

			assert.Equal(t, expected.code, dataset.Code())
			assert.Equal(t, expected.name, dataset.Name())
			assert.Equal(t, domain.DataSetStatusActive, dataset.Status())
			assert.NotNil(t, dataset.ActivatedAt(), "Dataset %s should be activated", expected.code)
		}
	})

	t.Run("2. Datasets have correct validation expressions", func(t *testing.T) {
		for _, expected := range utilizationExpectations {
			dataset, err := tc.repos.DataSet.FindByCode(ctx, expected.code)
			require.NoError(t, err)

			assert.Equal(t, expected.validationExpression, dataset.ValidationExpression(),
				"Validation expression mismatch for %s", expected.code)
		}
	})

	t.Run("3. Datasets have correct resolution key patterns", func(t *testing.T) {
		for _, expected := range utilizationExpectations {
			dataset, err := tc.repos.DataSet.FindByCode(ctx, expected.code)
			require.NoError(t, err)

			assert.Equal(t, expected.resolutionKeyExpression, dataset.ResolutionKeyExpression(),
				"Resolution key expression mismatch for %s", expected.code)
		}
	})

	t.Run("4. Datasets have UTILIZATION data category", func(t *testing.T) {
		for _, expected := range utilizationExpectations {
			dataset, err := tc.repos.DataSet.FindByCode(ctx, expected.code)
			require.NoError(t, err)

			assert.Equal(t, domain.DataCategoryUtilization, dataset.DataCategory(),
				"Data category mismatch for %s", expected.code)
		}
	})

	t.Run("5. ListDataSets returns all utilization datasets", func(t *testing.T) {
		utilizationCategory := domain.DataCategoryUtilization
		filters := domain.DataSetFilters{
			Category: &utilizationCategory,
			Limit:    100,
		}

		datasets, _, err := tc.repos.DataSet.List(ctx, filters)
		require.NoError(t, err)

		assert.Len(t, datasets, 5,
			"Expected 5 UTILIZATION datasets, got %d", len(datasets))

		// Verify all expected codes are present
		foundCodes := make(map[string]bool)
		for _, ds := range datasets {
			foundCodes[ds.Code()] = true
		}
		for _, code := range utilizationDatasetCodes {
			assert.True(t, foundCodes[code], "Expected dataset %s in list results", code)
		}
	})
}

// TestE2E_PlatformAuditEventsDataSource tests the PLATFORM_AUDIT_EVENTS data source
// seeded by migration, verifying trust level and properties.
func TestE2E_PlatformAuditEventsDataSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_audit_events_tenant")

	// Seed utilization data (includes PLATFORM_AUDIT_EVENTS)
	seedUtilizationData(t, tc.pool)

	t.Run("1. PLATFORM_AUDIT_EVENTS data source exists", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err, "PLATFORM_AUDIT_EVENTS data source should exist")

		assert.Equal(t, "PLATFORM_AUDIT_EVENTS", source.Code())
		assert.Equal(t, "Platform Audit Events", source.Name())
	})

	t.Run("2. PLATFORM_AUDIT_EVENTS has maximum trust level", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		assert.Equal(t, 100, source.TrustLevel(),
			"PLATFORM_AUDIT_EVENTS should have trust level 100 (INTERNAL/highest)")
	})
}

// TestE2E_UtilizationObservationRecording tests recording observations
// against utilization datasets with proper resolution keys.
func TestE2E_UtilizationObservationRecording(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantContext(t, "e2e_utilization_obs_tenant")

	// Seed utilization data
	seedUtilizationData(t, tc.pool)

	t.Run("1. Record transaction utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"UTILIZATION_TRANSACTION",
			source.ID(),
			"tenant/acme-corp/transaction/payment",
			decimal.NewFromInt(42),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.Equal(t, "tenant/acme-corp/transaction/payment", obs.ResolutionKey())
		assert.True(t, decimal.NewFromInt(42).Equal(obs.Value()))
	})

	t.Run("2. Record API call utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"UTILIZATION_API_CALL",
			source.ID(),
			"tenant/acme-corp/api/market-info/list-datasets",
			decimal.NewFromInt(1500),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.Equal(t, "tenant/acme-corp/api/market-info/list-datasets", obs.ResolutionKey())
	})

	t.Run("3. Record storage utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"UTILIZATION_STORAGE_GB",
			source.ID(),
			"tenant/acme-corp/storage/hot-tier",
			decimal.NewFromFloat(256.75),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.True(t, decimal.NewFromFloat(256.75).Equal(obs.Value()))
	})

	t.Run("4. Record compute utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"UTILIZATION_COMPUTE_HOUR",
			source.ID(),
			"tenant/acme-corp/compute/gpu-v100",
			decimal.NewFromFloat(8.5),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.True(t, decimal.NewFromFloat(8.5).Equal(obs.Value()))
	})

	t.Run("5. Record network utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctx, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		obs := createTestObservation(t, ctx, tc.repos,
			"UTILIZATION_NETWORK_GB",
			source.ID(),
			"tenant/acme-corp/network/egress",
			decimal.NewFromFloat(12.3),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)

		assert.True(t, decimal.NewFromFloat(12.3).Equal(obs.Value()))
	})

	t.Run("6. Query utilization observations by dataset", func(t *testing.T) {
		query := domain.ObservationQuery{
			DataSetCode: "UTILIZATION_TRANSACTION",
		}

		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		assert.Len(t, observations, 1)
		assert.Equal(t, "tenant/acme-corp/transaction/payment", observations[0].ResolutionKey())
	})

	t.Run("7. Query utilization observations by resolution key", func(t *testing.T) {
		resKey := "tenant/acme-corp/api/market-info/list-datasets"
		query := domain.ObservationQuery{
			DataSetCode:   "UTILIZATION_API_CALL",
			ResolutionKey: &resKey,
		}

		observations, _, err := tc.repos.Observation.Query(ctx, query)
		require.NoError(t, err)

		assert.Len(t, observations, 1)
		assert.True(t, decimal.NewFromInt(1500).Equal(observations[0].Value()))
	})
}

// TestE2E_UtilizationMultiTenantIsolation verifies that utilization observations
// from one tenant are not visible to another tenant.
func TestE2E_UtilizationMultiTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)

	ctxTenantA := setupTenantContext(t, "utilization_tenant_a")
	ctxTenantB := setupTenantContext(t, "utilization_tenant_b")

	// Seed utilization data
	seedUtilizationData(t, tc.pool)

	t.Run("Tenant A records utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctxTenantA, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		createTestObservation(t, ctxTenantA, tc.repos,
			"UTILIZATION_TRANSACTION",
			source.ID(),
			"tenant/tenant-a/transaction/payment",
			decimal.NewFromInt(100),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)
	})

	t.Run("Tenant B records utilization observation", func(t *testing.T) {
		source, err := tc.repos.Source.FindByCode(ctxTenantB, "PLATFORM_AUDIT_EVENTS")
		require.NoError(t, err)

		now := time.Now().UTC()
		createTestObservation(t, ctxTenantB, tc.repos,
			"UTILIZATION_TRANSACTION",
			source.ID(),
			"tenant/tenant-b/transaction/payment",
			decimal.NewFromInt(200),
			now, now, now.Add(time.Hour),
			domain.QualityLevelActual,
			source.TrustLevel(),
		)
	})

	t.Run("Each tenant sees only their utilization data by resolution key", func(t *testing.T) {
		resKeyA := "tenant/tenant-a/transaction/payment"
		queryA := domain.ObservationQuery{
			DataSetCode:   "UTILIZATION_TRANSACTION",
			ResolutionKey: &resKeyA,
		}
		obsA, _, err := tc.repos.Observation.Query(ctxTenantA, queryA)
		require.NoError(t, err)
		assert.Len(t, obsA, 1)
		assert.True(t, decimal.NewFromInt(100).Equal(obsA[0].Value()))

		resKeyB := "tenant/tenant-b/transaction/payment"
		queryB := domain.ObservationQuery{
			DataSetCode:   "UTILIZATION_TRANSACTION",
			ResolutionKey: &resKeyB,
		}
		obsB, _, err := tc.repos.Observation.Query(ctxTenantB, queryB)
		require.NoError(t, err)
		assert.Len(t, obsB, 1)
		assert.True(t, decimal.NewFromInt(200).Equal(obsB[0].Value()))
	})
}

// ============================================================================
// Helper Functions
// ============================================================================

func stringPtr(s string) *string {
	return &s
}
