package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDataSetAndSource creates a test dataset and data source for observation tests.
func setupTestDataSetAndSource(t *testing.T, tc *testhelpers.TestContainer) (domain.DataSetDefinition, domain.DataSource) {
	t.Helper()
	ctx := context.Background()

	// Create and activate a dataset
	dataset, err := domain.NewDataSetDefinition(
		"FX_RATE_TEST",
		"FX Rate Test",
		"Test dataset for observations",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Retrieve to get the generated ID
	dataset, err = tc.Repos.DataSet.FindByCode(ctx, "FX_RATE_TEST")
	require.NoError(t, err)

	// Activate the dataset
	activatedDataset, err := dataset.ActivateDataSet()
	require.NoError(t, err)
	err = tc.Repos.DataSet.Save(ctx, activatedDataset)
	require.NoError(t, err)

	// Create a data source
	source, err := domain.NewDataSource(
		"ECB_TEST",
		"ECB Test",
		"European Central Bank test feed",
		domain.SourceTypeAPI,
		90,
	)
	require.NoError(t, err)

	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)

	source, err = tc.Repos.Source.FindByCode(ctx, "ECB_TEST")
	require.NoError(t, err)

	return activatedDataset, source
}

func TestObservationRepository_Record_NewObservation(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	// Create an observation
	now := time.Now()
	obs, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.0850),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)

	// Record it
	err = tc.Repos.Observation.Record(ctx, obs)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := tc.Repos.Observation.FindByID(ctx, obs.ID())
	require.NoError(t, err)

	assert.Equal(t, "FX_RATE_TEST", retrieved.DataSetCode())
	assert.Equal(t, "EUR/USD", retrieved.ResolutionKey())
	assert.Equal(t, domain.QualityLevelActual, retrieved.QualityLevel())
	assert.True(t, decimal.NewFromFloat(1.0850).Equal(retrieved.Value()))
}

func TestObservationRepository_Record_SupersessionByQuality(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// First, record an ESTIMATE
	estimate, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.0800),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelEstimate,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, estimate)
	require.NoError(t, err)

	// Verify estimate is retrievable via GetLatest
	latest, err := tc.Repos.Observation.GetLatest(ctx, "FX_RATE_TEST", "EUR/USD")
	require.NoError(t, err)
	assert.Equal(t, domain.QualityLevelEstimate, latest.QualityLevel())

	// Record an ACTUAL for the same resolution key
	actual, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.0850),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, actual)
	require.NoError(t, err)

	// Now GetLatest should return the ACTUAL (higher quality supersedes)
	latest, err = tc.Repos.Observation.GetLatest(ctx, "FX_RATE_TEST", "EUR/USD")
	require.NoError(t, err)
	assert.Equal(t, domain.QualityLevelActual, latest.QualityLevel())
	assert.True(t, decimal.NewFromFloat(1.0850).Equal(latest.Value()))

	// The estimate should now be superseded
	oldEstimate, err := tc.Repos.Observation.FindByID(ctx, estimate.ID())
	require.NoError(t, err)
	assert.NotNil(t, oldEstimate.SupersededBy())
	assert.Equal(t, actual.ID(), *oldEstimate.SupersededBy())
}

func TestObservationRepository_FindByID_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repos.Observation.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrObservationNotFound)
}

func TestObservationRepository_Query_ByDataSetCode(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Create multiple observations
	for i := 0; i < 3; i++ {
		obs, err := domain.NewMarketPriceObservation(
			"FX_RATE_TEST",
			source.ID(),
			"EUR/USD/"+string(rune('0'+i)),
			decimal.NewFromFloat(1.08+float64(i)*0.001),
			"USD",
			now.Add(time.Duration(i)*time.Hour),
			now,
			now.Add(24*time.Hour),
			uuid.New(),
			domain.QualityLevelActual,
			source.TrustLevel(),
			domain.ObservationContext{},
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query all observations for the dataset
	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode: "FX_RATE_TEST",
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Results should be ordered by created_at descending (for cursor pagination consistency)
	// Note: we can't guarantee observed_at ordering in pagination mode
}

func TestObservationRepository_Query_ByResolutionKey(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Create observations for different resolution keys
	keys := []string{"EUR/USD", "GBP/USD", "EUR/USD"}
	for i, key := range keys {
		obs, err := domain.NewMarketPriceObservation(
			"FX_RATE_TEST",
			source.ID(),
			key,
			decimal.NewFromFloat(1.08+float64(i)*0.001),
			"USD",
			now.Add(time.Duration(i)*time.Minute),
			now,
			now.Add(24*time.Hour),
			uuid.New(),
			domain.QualityLevelActual,
			source.TrustLevel(),
			domain.ObservationContext{},
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query by specific resolution key
	resKey := "EUR/USD"
	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:   "FX_RATE_TEST",
		ResolutionKey: &resKey,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	for _, r := range results {
		assert.Equal(t, "EUR/USD", r.ResolutionKey())
	}
}

func TestObservationRepository_Query_ByQualityLevel(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Create observations with different quality levels
	qualities := []domain.QualityLevel{
		domain.QualityLevelEstimate,
		domain.QualityLevelActual,
		domain.QualityLevelVerified,
	}

	for i, q := range qualities {
		obs, err := domain.NewMarketPriceObservation(
			"FX_RATE_TEST",
			source.ID(),
			"KEY_"+string(rune('0'+i)),
			decimal.NewFromFloat(1.08+float64(i)*0.001),
			"USD",
			now,
			now,
			now.Add(24*time.Hour),
			uuid.New(),
			q,
			source.TrustLevel(),
			domain.ObservationContext{},
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query by VERIFIED quality only
	verifiedQuality := domain.QualityLevelVerified
	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:  "FX_RATE_TEST",
		QualityLevel: &verifiedQuality,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, domain.QualityLevelVerified, results[0].QualityLevel())
}

func TestObservationRepository_Query_IncludeSuperseded(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Create an ESTIMATE
	estimate, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"EUR/GBP",
		decimal.NewFromFloat(0.8500),
		"GBP",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelEstimate,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, estimate)
	require.NoError(t, err)

	// Supersede with ACTUAL
	actual, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"EUR/GBP",
		decimal.NewFromFloat(0.8550),
		"GBP",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, actual)
	require.NoError(t, err)

	// Query without superseded (default) - should only get ACTUAL
	resKey := "EUR/GBP"
	nonSuperseded, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:       "FX_RATE_TEST",
		ResolutionKey:     &resKey,
		IncludeSuperseded: false,
	})
	require.NoError(t, err)
	assert.Len(t, nonSuperseded, 1)
	assert.Equal(t, domain.QualityLevelActual, nonSuperseded[0].QualityLevel())

	// Query with superseded - should get both
	withSuperseded, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:       "FX_RATE_TEST",
		ResolutionKey:     &resKey,
		IncludeSuperseded: true,
	})
	require.NoError(t, err)
	assert.Len(t, withSuperseded, 2)
}

func TestObservationRepository_GetLatest_QualityLadder(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Create VERIFIED observation first (oldest)
	verified, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"QUALITY_TEST",
		decimal.NewFromFloat(1.0900),
		"USD",
		now.Add(-2*time.Hour),
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelVerified,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, verified)
	require.NoError(t, err)

	// Create ACTUAL observation (newer)
	actual, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"QUALITY_TEST",
		decimal.NewFromFloat(1.0850),
		"USD",
		now.Add(-1*time.Hour),
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, actual)
	require.NoError(t, err)

	// Create ESTIMATE observation (newest)
	estimate, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"QUALITY_TEST",
		decimal.NewFromFloat(1.0800),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelEstimate,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, estimate)
	require.NoError(t, err)

	// GetLatest should return VERIFIED (highest quality) despite being oldest
	latest, err := tc.Repos.Observation.GetLatest(ctx, "FX_RATE_TEST", "QUALITY_TEST")
	require.NoError(t, err)
	assert.Equal(t, domain.QualityLevelVerified, latest.QualityLevel())
	assert.True(t, decimal.NewFromFloat(1.0900).Equal(latest.Value()))
}

func TestObservationRepository_RetrieveObservation_KnowledgeBaseTime(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	// We need to control created_at times, so we'll insert directly
	// For this test, let's use the repository and check time-based queries

	now := time.Now()

	// Create an observation
	obs, err := domain.NewMarketPriceObservation(
		"FX_RATE_TEST",
		source.ID(),
		"BITEMPORAL_TEST",
		decimal.NewFromFloat(1.0850),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, obs)
	require.NoError(t, err)

	// Query with knowledge base time in the future - should find the observation
	futureTime := now.Add(1 * time.Hour)
	result, err := tc.Repos.Observation.RetrieveObservation(ctx, "FX_RATE_TEST", "BITEMPORAL_TEST", futureTime)
	require.NoError(t, err)
	assert.Equal(t, "BITEMPORAL_TEST", result.ResolutionKey())

	// Query with knowledge base time before the observation was created - should not find it
	pastTime := now.Add(-1 * time.Hour)
	_, err = tc.Repos.Observation.RetrieveObservation(ctx, "FX_RATE_TEST", "BITEMPORAL_TEST", pastTime)
	assert.ErrorIs(t, err, domain.ErrObservationNotFound)
}

func TestObservationRepository_GetLatest_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	setupTestDataSetAndSource(t, tc)

	_, err := tc.Repos.Observation.GetLatest(ctx, "FX_RATE_TEST", "NON_EXISTENT_KEY")
	assert.ErrorIs(t, err, domain.ErrObservationNotFound)
}

func TestObservationRepository_Record_InvalidDataSetCode(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Try to record observation with non-existent dataset code
	obs, err := domain.NewMarketPriceObservation(
		"NON_EXISTENT_DATASET",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.0850),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)

	err = tc.Repos.Observation.Record(ctx, obs)
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound)
}

// setupSharedDataSetAndSource creates a shared dataset with specified access level.
func setupSharedDataSetAndSource(t *testing.T, tc *testhelpers.TestContainer, code string, accessLevel domain.DataAccessLevel) (domain.DataSetDefinition, domain.DataSource) {
	t.Helper()
	ctx := context.Background()

	// Create and activate a shared dataset
	dataset, err := domain.NewDataSetDefinition(
		code,
		"Shared "+code,
		"Shared test dataset for multi-tenant observations",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	// Build with shared flags - copy all fields manually
	builder := domain.NewDataSetDefinitionBuilder().
		WithID(dataset.ID()).
		WithCode(dataset.Code()).
		WithVersion(dataset.Version()).
		WithName(dataset.Name()).
		WithDescription(dataset.Description()).
		WithDataCategory(dataset.DataCategory()).
		WithValidationExpression(dataset.ValidationExpression()).
		WithResolutionKeyExpression(dataset.ResolutionKeyExpression()).
		WithErrorMessageExpression(dataset.ErrorMessageExpression()).
		WithStatus(dataset.Status()).
		WithIsShared(true).
		WithAccessLevel(accessLevel).
		WithCreatedAt(dataset.CreatedAt()).
		WithUpdatedAt(dataset.UpdatedAt())
	sharedDataset := builder.Build()

	err = tc.Repos.DataSet.Save(ctx, sharedDataset)
	require.NoError(t, err)

	// Retrieve to get the generated ID
	sharedDataset, err = tc.Repos.DataSet.FindByCode(ctx, code)
	require.NoError(t, err)

	// Activate the dataset
	activatedDataset, err := sharedDataset.ActivateDataSet()
	require.NoError(t, err)
	err = tc.Repos.DataSet.Save(ctx, activatedDataset)
	require.NoError(t, err)

	// Create a data source
	source, err := domain.NewDataSource(
		"ECB_SHARED_TEST",
		"ECB Shared Test",
		"European Central Bank shared test feed",
		domain.SourceTypeAPI,
		90,
	)
	require.NoError(t, err)

	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)

	source, err = tc.Repos.Source.FindByCode(ctx, "ECB_SHARED_TEST")
	require.NoError(t, err)

	return activatedDataset, source
}

func TestObservationRepository_HierarchicalLookup_TenantOverride(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "SHARED_FX_RATE", domain.AccessLevelPublic)

	now := time.Now()

	// Record observation in master tenant (test_master)
	masterObs, err := domain.NewMarketPriceObservation(
		"SHARED_FX_RATE",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.0850),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_a")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Record tenant-specific observation with different value
	tenantObs, err := domain.NewMarketPriceObservation(
		"SHARED_FX_RATE",
		source.ID(),
		"EUR/USD",
		decimal.NewFromFloat(1.1000), // Different value
		"USD",
		now.Add(1*time.Minute),
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(tenantCtx, tenantObs)
	require.NoError(t, err)

	// Query from tenant context - should get tenant-specific observation
	result, err := tc.Repos.Observation.GetLatest(tenantCtx, "SHARED_FX_RATE", "EUR/USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(1.1000).Equal(result.Value()),
		"Expected tenant override value, got master value")
	assert.Equal(t, tenantObs.ID(), result.ID(),
		"Should return tenant observation, not master")
}

func TestObservationRepository_HierarchicalLookup_MasterFallback(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "SHARED_FX_RATE_FALLBACK", domain.AccessLevelPublic)

	now := time.Now()

	// Record observation in master tenant only
	masterObs, err := domain.NewMarketPriceObservation(
		"SHARED_FX_RATE_FALLBACK",
		source.ID(),
		"GBP/USD",
		decimal.NewFromFloat(1.2500),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_b")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Query from tenant context - should fall back to master
	result, err := tc.Repos.Observation.GetLatest(tenantCtx, "SHARED_FX_RATE_FALLBACK", "GBP/USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(1.2500).Equal(result.Value()),
		"Should fall back to master tenant data")
	assert.Equal(t, masterObs.ID(), result.ID(),
		"Should return master observation via fallback")
}

func TestObservationRepository_HierarchicalLookup_PrivateDatasetNoFallback(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a PRIVATE (non-shared) dataset
	dataset, err := domain.NewDataSetDefinition(
		"PRIVATE_FX_RATE",
		"Private FX Rate",
		"Private test dataset - no sharing",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)
	// Default is private, but be explicit
	builder := domain.NewDataSetDefinitionBuilder().
		WithID(dataset.ID()).
		WithCode(dataset.Code()).
		WithVersion(dataset.Version()).
		WithName(dataset.Name()).
		WithDescription(dataset.Description()).
		WithDataCategory(dataset.DataCategory()).
		WithValidationExpression(dataset.ValidationExpression()).
		WithResolutionKeyExpression(dataset.ResolutionKeyExpression()).
		WithErrorMessageExpression(dataset.ErrorMessageExpression()).
		WithStatus(dataset.Status()).
		WithIsShared(false).
		WithAccessLevel(domain.AccessLevelPrivate).
		WithCreatedAt(dataset.CreatedAt()).
		WithUpdatedAt(dataset.UpdatedAt())
	privateDataset := builder.Build()

	err = tc.Repos.DataSet.Save(ctx, privateDataset)
	require.NoError(t, err)

	privateDataset, err = tc.Repos.DataSet.FindByCode(ctx, "PRIVATE_FX_RATE")
	require.NoError(t, err)

	activatedDataset, err := privateDataset.ActivateDataSet()
	require.NoError(t, err)
	err = tc.Repos.DataSet.Save(ctx, activatedDataset)
	require.NoError(t, err)

	// Create data source
	source, err := domain.NewDataSource(
		"PRIVATE_SOURCE",
		"Private Source",
		"Private data source",
		domain.SourceTypeAPI,
		90,
	)
	require.NoError(t, err)
	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)
	source, err = tc.Repos.Source.FindByCode(ctx, "PRIVATE_SOURCE")
	require.NoError(t, err)

	now := time.Now()

	// Record observation in master tenant
	masterObs, err := domain.NewMarketPriceObservation(
		"PRIVATE_FX_RATE",
		source.ID(),
		"CHF/USD",
		decimal.NewFromFloat(1.0900),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_c")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Query from tenant context - should NOT fall back to master for private dataset
	// Private datasets don't get copied to tenant schemas, so we get ErrDataSetNotFound
	_, err = tc.Repos.Observation.GetLatest(tenantCtx, "PRIVATE_FX_RATE", "CHF/USD")
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound,
		"Private datasets should not be accessible from tenant schemas")
}

func TestObservationRepository_HierarchicalLookup_RestrictedAccessDenied(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "RESTRICTED_FX_RATE", domain.AccessLevelRestricted)

	now := time.Now()

	// Record observation in master tenant
	masterObs, err := domain.NewMarketPriceObservation(
		"RESTRICTED_FX_RATE",
		source.ID(),
		"JPY/USD",
		decimal.NewFromFloat(0.0085),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context WITHOUT entitlement
	tenantID, err := tc.CreateTenantSchema("tenant_d")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Query from tenant context - should be denied access
	_, err = tc.Repos.Observation.GetLatest(tenantCtx, "RESTRICTED_FX_RATE", "JPY/USD")
	assert.ErrorIs(t, err, domain.ErrAccessDenied,
		"RESTRICTED dataset should deny access without entitlement")
}

func TestObservationRepository_HierarchicalLookup_RestrictedAccessGranted(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "RESTRICTED_FX_RATE_GRANTED", domain.AccessLevelRestricted)

	now := time.Now()

	// Record observation in master tenant
	masterObs, err := domain.NewMarketPriceObservation(
		"RESTRICTED_FX_RATE_GRANTED",
		source.ID(),
		"CAD/USD",
		decimal.NewFromFloat(0.7200),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_e")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Grant entitlement to tenant
	err = tc.GrantTenantEntitlement(ctx, tenantID, "RESTRICTED_FX_RATE_GRANTED", nil)
	require.NoError(t, err)

	// Query from tenant context - should succeed with entitlement
	result, err := tc.Repos.Observation.GetLatest(tenantCtx, "RESTRICTED_FX_RATE_GRANTED", "CAD/USD")
	require.NoError(t, err, "RESTRICTED dataset should allow access with valid entitlement")
	assert.True(t, decimal.NewFromFloat(0.7200).Equal(result.Value()),
		"Should access master data with valid entitlement")
	assert.Equal(t, masterObs.ID(), result.ID())
}

func TestObservationRepository_HierarchicalLookup_RestrictedAccessExpired(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "RESTRICTED_FX_RATE_EXPIRED", domain.AccessLevelRestricted)

	now := time.Now()

	// Record observation in master tenant
	masterObs, err := domain.NewMarketPriceObservation(
		"RESTRICTED_FX_RATE_EXPIRED",
		source.ID(),
		"AUD/USD",
		decimal.NewFromFloat(0.6500),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_f")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Grant entitlement with past expiration date
	expiresAt := now.Add(-24 * time.Hour) // Already expired
	err = tc.GrantTenantEntitlement(ctx, tenantID, "RESTRICTED_FX_RATE_EXPIRED", &expiresAt)
	require.NoError(t, err)

	// Query from tenant context - should be denied due to expired entitlement
	_, err = tc.Repos.Observation.GetLatest(tenantCtx, "RESTRICTED_FX_RATE_EXPIRED", "AUD/USD")
	assert.ErrorIs(t, err, domain.ErrAccessDenied,
		"RESTRICTED dataset should deny access with expired entitlement")
}

func TestObservationRepository_HierarchicalLookup_RestrictedAccessInactive(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupSharedDataSetAndSource(t, tc, "RESTRICTED_FX_RATE_INACTIVE", domain.AccessLevelRestricted)

	now := time.Now()

	// Record observation in master tenant
	masterObs, err := domain.NewMarketPriceObservation(
		"RESTRICTED_FX_RATE_INACTIVE",
		source.ID(),
		"NZD/USD",
		decimal.NewFromFloat(0.6000),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		source.TrustLevel(),
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, masterObs)
	require.NoError(t, err)

	// Create tenant context
	tenantID, err := tc.CreateTenantSchema("tenant_g")
	require.NoError(t, err)
	tenantCtx := tc.WithTenant(ctx, tenantID)

	// Grant inactive entitlement
	err = tc.GrantTenantEntitlement(ctx, tenantID, "RESTRICTED_FX_RATE_INACTIVE", nil)
	require.NoError(t, err)

	// Revoke entitlement (set is_active = false)
	err = tc.RevokeTenantEntitlement(ctx, tenantID, "RESTRICTED_FX_RATE_INACTIVE")
	require.NoError(t, err)

	// Query from tenant context - should be denied due to inactive entitlement
	_, err = tc.Repos.Observation.GetLatest(tenantCtx, "RESTRICTED_FX_RATE_INACTIVE", "NZD/USD")
	assert.ErrorIs(t, err, domain.ErrAccessDenied,
		"RESTRICTED dataset should deny access with inactive entitlement")
}

func TestObservationRepository_CountByDataset_Basic(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Insert 5 observations
	for i := 0; i < 5; i++ {
		obs, err := domain.NewMarketPriceObservation(
			"FX_RATE_TEST",
			source.ID(),
			"EUR/USD/"+string(rune('0'+i)),
			decimal.NewFromFloat(1.08+float64(i)*0.001),
			"USD",
			now.Add(time.Duration(i)*time.Minute),
			now,
			now.Add(24*time.Hour),
			uuid.New(),
			domain.QualityLevelActual,
			source.TrustLevel(),
			domain.ObservationContext{},
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Count should be 5
	count, err := tc.Repos.Observation.CountByDataset(ctx, "FX_RATE_TEST", false)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
}

func TestObservationRepository_CountByDataset_ExcludesSuperseded(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	now := time.Now()

	// Insert 5 observations for the same resolution key
	// They will progressively supersede each other based on quality
	for i := 0; i < 5; i++ {
		quality := domain.QualityLevelEstimate
		if i >= 3 {
			quality = domain.QualityLevelActual
		}
		if i >= 4 {
			quality = domain.QualityLevelVerified
		}
		obs, err := domain.NewMarketPriceObservation(
			"FX_RATE_TEST",
			source.ID(),
			"EUR/USD",
			decimal.NewFromFloat(1.08+float64(i)*0.001),
			"USD",
			now.Add(time.Duration(i)*time.Minute),
			now,
			now.Add(24*time.Hour),
			uuid.New(),
			quality,
			source.TrustLevel(),
			domain.ObservationContext{},
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Count without superseded - should exclude the ones that were superseded
	countWithoutSuperseded, err := tc.Repos.Observation.CountByDataset(ctx, "FX_RATE_TEST", false)
	require.NoError(t, err)

	// Count with superseded - should include all 5
	countWithSuperseded, err := tc.Repos.Observation.CountByDataset(ctx, "FX_RATE_TEST", true)
	require.NoError(t, err)

	assert.Equal(t, int64(5), countWithSuperseded)
	assert.Less(t, countWithoutSuperseded, countWithSuperseded,
		"Non-superseded count should be less than total count")
}

func TestObservationRepository_CountByDataset_EmptyDataset(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	// Setup creates dataset but we don't insert any observations
	setupTestDataSetAndSource(t, tc)

	// Count for empty dataset should be 0
	count, err := tc.Repos.Observation.CountByDataset(ctx, "FX_RATE_TEST", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestObservationRepository_CountByDataset_NonExistentDataset(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Count for non-existent dataset should return ErrDataSetNotFound
	_, err := tc.Repos.Observation.CountByDataset(ctx, "NON_EXISTENT_DATASET", false)
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound)
}

// recordObservationWithTimes records an observation with caller-controlled bi-temporal
// times using the builder, bypassing NewMarketPriceObservation's time.Now() created_at.
// This enables deterministic period-scoping, as-of, and ordering assertions.
func recordObservationWithTimes(
	t *testing.T,
	tc *testhelpers.TestContainer,
	sourceID uuid.UUID,
	resolutionKey string,
	quality domain.QualityLevel,
	value decimal.Decimal,
	observedAt, validFrom, validTo, createdAt time.Time,
) domain.MarketPriceObservation {
	t.Helper()
	obs := domain.NewMarketPriceObservationBuilder().
		WithID(uuid.New()).
		WithDataSetCode("FX_RATE_TEST").
		WithSourceID(sourceID).
		WithResolutionKey(resolutionKey).
		WithValue(value).
		WithObservedAt(observedAt).
		WithValidFrom(validFrom).
		WithValidTo(validTo).
		WithCreatedAt(createdAt).
		WithQualityLevel(quality).
		WithCausationID(uuid.New()).
		Build()
	require.NoError(t, tc.Repos.Observation.Record(context.Background(), obs))
	return obs
}

// TestObservationRepository_Record_SupersessionScopedToPeriod verifies DAT-2: a
// higher-quality observation supersedes only same-period lower-quality observations,
// not observations for other periods in the same series (same resolution key).
func TestObservationRepository_Record_SupersessionScopedToPeriod(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	base := time.Now().Add(-72 * time.Hour)
	day := 24 * time.Hour
	price := decimal.NewFromFloat(1.10)

	// Three ESTIMATE observations for the same resolution key, distinct periods.
	p1 := recordObservationWithTimes(t, tc, source.ID(), "EUR/USD", domain.QualityLevelEstimate, price, base, base, base.Add(day), base)
	p2 := recordObservationWithTimes(t, tc, source.ID(), "EUR/USD", domain.QualityLevelEstimate, price, base.Add(day), base.Add(day), base.Add(2*day), base.Add(time.Minute))
	p3 := recordObservationWithTimes(t, tc, source.ID(), "EUR/USD", domain.QualityLevelEstimate, price, base.Add(2*day), base.Add(2*day), base.Add(3*day), base.Add(2*time.Minute))

	// A higher-quality ACTUAL for period 1 only.
	recordObservationWithTimes(t, tc, source.ID(), "EUR/USD", domain.QualityLevelActual, price, base, base, base.Add(day), base.Add(time.Hour))

	// Only the period-1 estimate must be superseded.
	got1, err := tc.Repos.Observation.FindByID(ctx, p1.ID())
	require.NoError(t, err)
	assert.NotNil(t, got1.SupersededBy(), "period-1 estimate must be superseded")

	got2, err := tc.Repos.Observation.FindByID(ctx, p2.ID())
	require.NoError(t, err)
	assert.Nil(t, got2.SupersededBy(), "period-2 estimate must not leak supersession")

	got3, err := tc.Repos.Observation.FindByID(ctx, p3.ID())
	require.NoError(t, err)
	assert.Nil(t, got3.SupersededBy(), "period-3 estimate must not leak supersession")

	// Non-superseded query returns period-1 ACTUAL plus both untouched estimates.
	resKey := "EUR/USD"
	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:   "FX_RATE_TEST",
		ResolutionKey: &resKey,
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestObservationRepository_RetrieveObservation_AsOfSupersessionState verifies DAT-3:
// bi-temporal as-of queries reconstruct supersession state at the query time, using
// superseded_at rather than the present superseded_by IS NULL state.
func TestObservationRepository_RetrieveObservation_AsOfSupersessionState(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	base := time.Now().Add(-72 * time.Hour)
	t1 := base                    // A becomes known
	t2 := base.Add(2 * time.Hour) // B supersedes A
	valA := decimal.NewFromFloat(1.05)
	valB := decimal.NewFromFloat(1.09)

	// A (ESTIMATE) known at t1; B (ACTUAL) supersedes it at t2 (B.created_at).
	recordObservationWithTimes(t, tc, source.ID(), "AS_OF", domain.QualityLevelEstimate, valA, t1, t1, t1.Add(24*time.Hour), t1)
	recordObservationWithTimes(t, tc, source.ID(), "AS_OF", domain.QualityLevelActual, valB, t1, t1, t1.Add(24*time.Hour), t2)

	// As-of between t1 and t2: A was still authoritative.
	asOfMid := base.Add(1 * time.Hour)
	got, err := tc.Repos.Observation.RetrieveObservation(ctx, "FX_RATE_TEST", "AS_OF", asOfMid)
	require.NoError(t, err)
	assert.True(t, valA.Equal(got.Value()), "as-of before supersession must return A")

	// As-of after t2: B is authoritative.
	asOfLater := base.Add(3 * time.Hour)
	got, err = tc.Repos.Observation.RetrieveObservation(ctx, "FX_RATE_TEST", "AS_OF", asOfLater)
	require.NoError(t, err)
	assert.True(t, valB.Equal(got.Value()), "as-of after supersession must return B")

	// Present time: B is the current state.
	got, err = tc.Repos.Observation.GetLatest(ctx, "FX_RATE_TEST", "AS_OF")
	require.NoError(t, err)
	assert.True(t, valB.Equal(got.Value()), "present query must return B")
	assert.Nil(t, got.SupersededAt(), "current authoritative observation is not superseded")
}

// TestObservationRepository_Query_ObservedFromInclusive verifies LOW-5: ObservedFrom
// is an inclusive lower bound, so an observation at exactly the boundary is returned.
func TestObservationRepository_Query_ObservedFromInclusive(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	// ECB-style midnight boundary timestamp.
	boundary := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	recordObservationWithTimes(t, tc, source.ID(), "EUR/USD", domain.QualityLevelActual, decimal.NewFromFloat(1.07), boundary, boundary, boundary.Add(24*time.Hour), boundary)

	from := boundary
	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:   "FX_RATE_TEST",
		ObservedAfter: &from,
	})
	require.NoError(t, err)
	assert.Len(t, results, 1, "observation at exactly ObservedFrom must be included")
}

// TestObservationRepository_Query_OrderedByValidFrom verifies LOW-6: results are ordered
// by valid_from DESC (business time), not created_at, as the forecasting MDS adapter relies on.
func TestObservationRepository_Query_OrderedByValidFrom(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	_, source := setupTestDataSetAndSource(t, tc)

	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	hour := time.Hour
	price := decimal.NewFromFloat(1.20)

	// created_at order is the reverse of valid_from order (out-of-order/backfilled reads).
	vfLatest := base.Add(2 * hour)
	vfMid := base.Add(1 * hour)
	vfEarliest := base
	recordObservationWithTimes(t, tc, source.ID(), "K_LATEST", domain.QualityLevelActual, price, vfLatest, vfLatest, vfLatest.Add(hour), base)                     // created first
	recordObservationWithTimes(t, tc, source.ID(), "K_MID", domain.QualityLevelActual, price, vfMid, vfMid, vfMid.Add(hour), base.Add(hour))                       // created second
	recordObservationWithTimes(t, tc, source.ID(), "K_EARLIEST", domain.QualityLevelActual, price, vfEarliest, vfEarliest, vfEarliest.Add(hour), base.Add(2*hour)) // created last

	results, _, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode: "FX_RATE_TEST",
	})
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Ordered by valid_from DESC regardless of created_at.
	assert.Equal(t, vfLatest.Unix(), results[0].ValidFrom().Unix())
	assert.Equal(t, vfMid.Unix(), results[1].ValidFrom().Unix())
	assert.Equal(t, vfEarliest.Unix(), results[2].ValidFrom().Unix())
}
