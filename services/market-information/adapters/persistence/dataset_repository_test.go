package persistence_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataSetRepository_Save_NewDataSet(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a new dataset
	dataset, err := domain.NewDataSetDefinition(
		"TEST_DATASET",
		"Test Dataset",
		"A test dataset for unit tests",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"Invalid value",
	)
	require.NoError(t, err)

	// Save it
	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := tc.Repos.DataSet.FindByCode(ctx, "TEST_DATASET")
	require.NoError(t, err)

	assert.Equal(t, "TEST_DATASET", retrieved.Code())
	assert.Equal(t, "Test Dataset", retrieved.Name())
	assert.Equal(t, "A test dataset for unit tests", retrieved.Description())
	assert.Equal(t, domain.DataCategoryPricing, retrieved.DataCategory())
	assert.Equal(t, domain.DataSetStatusDraft, retrieved.Status())
	assert.Equal(t, 1, retrieved.Version())
}

func TestDataSetRepository_Save_DuplicateCode_IsIdempotent(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create first dataset
	dataset1, err := domain.NewDataSetDefinition(
		"DUPLICATE_CODE",
		"First Dataset",
		"First description",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset1)
	require.NoError(t, err)

	// Create second dataset with same code - should be idempotent (ON CONFLICT DO NOTHING)
	dataset2, err := domain.NewDataSetDefinition(
		"DUPLICATE_CODE",
		"Second Dataset",
		"Second description",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset2)
	require.NoError(t, err, "duplicate dataset save should be idempotent")
}

func TestDataSetRepository_Save_UpdateWithOptimisticLocking(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create and save initial dataset
	dataset, err := domain.NewDataSetDefinition(
		"UPDATE_TEST",
		"Original Name",
		"Original description",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Retrieve it
	retrieved, err := tc.Repos.DataSet.FindByCode(ctx, "UPDATE_TEST")
	require.NoError(t, err)

	// Update description (increments version)
	updated, err := retrieved.UpdateDescription("Updated description")
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, updated)
	require.NoError(t, err)

	// Verify the update
	final, err := tc.Repos.DataSet.FindByCode(ctx, "UPDATE_TEST")
	require.NoError(t, err)

	assert.Equal(t, "Updated description", final.Description())
	assert.Equal(t, 2, final.Version())
}

func TestDataSetRepository_FindByCode_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repos.DataSet.FindByCode(ctx, "NON_EXISTENT")
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound)
}

func TestDataSetRepository_FindByCodeAndVersion(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create dataset
	dataset, err := domain.NewDataSetDefinition(
		"VERSION_TEST",
		"Version Test",
		"Testing versioned retrieval",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Find by code and version
	retrieved, err := tc.Repos.DataSet.FindByCodeAndVersion(ctx, "VERSION_TEST", 1)
	require.NoError(t, err)

	assert.Equal(t, "VERSION_TEST", retrieved.Code())
	assert.Equal(t, 1, retrieved.Version())

	// Wrong version should fail
	_, err = tc.Repos.DataSet.FindByCodeAndVersion(ctx, "VERSION_TEST", 99)
	assert.ErrorIs(t, err, domain.ErrDataSetNotFound)
}

func TestDataSetRepository_List_WithFilters(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create multiple datasets with different categories and statuses
	datasets := []struct {
		code     string
		category domain.DataCategory
	}{
		{"PRICING_1", domain.DataCategoryPricing},
		{"PRICING_2", domain.DataCategoryPricing},
		{"CONTEXTUAL_1", domain.DataCategoryContextual},
	}

	for _, d := range datasets {
		dataset, err := domain.NewDataSetDefinition(
			d.code,
			d.code,
			"Description",
			d.category,
			"value > 0",
			"observation_context.key",
			"",
		)
		require.NoError(t, err)
		err = tc.Repos.DataSet.Save(ctx, dataset)
		require.NoError(t, err)
	}

	// List all
	all, _, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Filter by category
	pricingCategory := domain.DataCategoryPricing
	pricingOnly, _, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{
		Category: &pricingCategory,
	})
	require.NoError(t, err)
	assert.Len(t, pricingOnly, 2)

	// Filter by status
	draftStatus := domain.DataSetStatusDraft
	draftsOnly, _, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{
		Status: &draftStatus,
	})
	require.NoError(t, err)
	assert.Len(t, draftsOnly, 3)
}

func TestDataSetRepository_List_Pagination(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create 5 datasets - cursor pagination uses timestamp+UUID ordering as tie-breaker
	for i := 0; i < 5; i++ {
		dataset, err := domain.NewDataSetDefinition(
			"PAGINATION_"+string(rune('A'+i)),
			"Dataset "+string(rune('A'+i)),
			"Description",
			domain.DataCategoryPricing,
			"value > 0",
			"observation_context.key",
			"",
		)
		require.NoError(t, err)
		err = tc.Repos.DataSet.Save(ctx, dataset)
		require.NoError(t, err)
	}

	// Get first page
	page1, token1, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, page1, 2)
	assert.NotEmpty(t, token1, "Should return next page token when more results exist")

	// Get second page using cursor
	page2, token2, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{
		Limit:     2,
		PageToken: token1,
	})
	require.NoError(t, err)
	assert.Len(t, page2, 2)
	assert.NotEmpty(t, token2)

	// Get third page using cursor
	page3, token3, err := tc.Repos.DataSet.List(ctx, domain.DataSetFilters{
		Limit:     2,
		PageToken: token2,
	})
	require.NoError(t, err)
	assert.Len(t, page3, 1)
	assert.Empty(t, token3, "Last page should have empty token")

	// Verify no duplicates across pages
	allIDs := make(map[string]bool)
	for _, ds := range page1 {
		assert.False(t, allIDs[ds.ID().String()], "Duplicate ID in page1")
		allIDs[ds.ID().String()] = true
	}
	for _, ds := range page2 {
		assert.False(t, allIDs[ds.ID().String()], "Duplicate ID in page2")
		allIDs[ds.ID().String()] = true
	}
	for _, ds := range page3 {
		assert.False(t, allIDs[ds.ID().String()], "Duplicate ID in page3")
		allIDs[ds.ID().String()] = true
	}
	assert.Len(t, allIDs, 5, "Should see all 5 datasets exactly once")
}

func TestDataSetRepository_ExistsByCode(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Should not exist initially
	exists, err := tc.Repos.DataSet.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.False(t, exists)

	// Create dataset
	dataset, err := domain.NewDataSetDefinition(
		"EXISTS_TEST",
		"Exists Test",
		"Testing existence check",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)
	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Should exist now
	exists, err = tc.Repos.DataSet.ExistsByCode(ctx, "EXISTS_TEST")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestDataSetRepository_Lifecycle_DraftToActive(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create dataset in DRAFT status
	dataset, err := domain.NewDataSetDefinition(
		"LIFECYCLE_TEST",
		"Lifecycle Test",
		"Testing lifecycle transitions",
		domain.DataCategoryPricing,
		"value > 0",
		"observation_context.key",
		"",
	)
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, dataset)
	require.NoError(t, err)

	// Retrieve and verify DRAFT status
	retrieved, err := tc.Repos.DataSet.FindByCode(ctx, "LIFECYCLE_TEST")
	require.NoError(t, err)
	assert.Equal(t, domain.DataSetStatusDraft, retrieved.Status())
	assert.Nil(t, retrieved.ActivatedAt())

	// Activate
	activated, err := retrieved.ActivateDataSet()
	require.NoError(t, err)

	err = tc.Repos.DataSet.Save(ctx, activated)
	require.NoError(t, err)

	// Verify ACTIVE status and activated_at timestamp
	final, err := tc.Repos.DataSet.FindByCode(ctx, "LIFECYCLE_TEST")
	require.NoError(t, err)
	assert.Equal(t, domain.DataSetStatusActive, final.Status())
	assert.NotNil(t, final.ActivatedAt())
}
