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
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query all observations for the dataset
	results, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode: "FX_RATE_TEST",
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Results should be ordered by observed_at descending
	assert.True(t, results[0].ObservedAt().After(results[1].ObservedAt()))
	assert.True(t, results[1].ObservedAt().After(results[2].ObservedAt()))
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
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query by specific resolution key
	resKey := "EUR/USD"
	results, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
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
		)
		require.NoError(t, err)
		err = tc.Repos.Observation.Record(ctx, obs)
		require.NoError(t, err)
	}

	// Query by VERIFIED quality only
	verifiedQuality := domain.QualityLevelVerified
	results, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
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
	)
	require.NoError(t, err)
	err = tc.Repos.Observation.Record(ctx, actual)
	require.NoError(t, err)

	// Query without superseded (default) - should only get ACTUAL
	resKey := "EUR/GBP"
	nonSuperseded, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
		DataSetCode:       "FX_RATE_TEST",
		ResolutionKey:     &resKey,
		IncludeSuperseded: false,
	})
	require.NoError(t, err)
	assert.Len(t, nonSuperseded, 1)
	assert.Equal(t, domain.QualityLevelActual, nonSuperseded[0].QualityLevel())

	// Query with superseded - should get both
	withSuperseded, err := tc.Repos.Observation.Query(ctx, domain.ObservationQuery{
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
	)
	require.NoError(t, err)

	err = tc.Repos.Observation.Record(ctx, obs)
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound)
}
