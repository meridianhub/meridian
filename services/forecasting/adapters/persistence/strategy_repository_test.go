package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/forecasting/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestStrategy(t *testing.T, tenantID, name string) domain.ForecastingStrategy {
	t.Helper()
	s, err := domain.NewForecastingStrategy(
		tenantID,
		name,
		"Test strategy for integration tests",
		`result = [42.0] * 24`,
		24,
		1,
		"0 16 * * *",
		[]string{"SPOT_PRICE", "WEATHER_FORECAST"},
		"FORWARD_CURVE_ELEC",
		"GB/SOUTH",
	)
	require.NoError(t, err)
	return s
}

func TestStrategyRepository_Save_NewStrategy(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	strategy := createTestStrategy(t, "tenant-1", "Day-Ahead Price Forecast")

	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := tc.Repo.FindByID(ctx, strategy.ID())
	require.NoError(t, err)

	assert.Equal(t, strategy.ID(), retrieved.ID())
	assert.Equal(t, "tenant-1", retrieved.TenantID())
	assert.Equal(t, "Day-Ahead Price Forecast", retrieved.Name())
	assert.Equal(t, "Test strategy for integration tests", retrieved.Description())
	assert.Equal(t, `result = [42.0] * 24`, retrieved.StarlarkCode())
	assert.Equal(t, 24, retrieved.HorizonHours())
	assert.Equal(t, 1, retrieved.GranularityHours())
	assert.Equal(t, "0 16 * * *", retrieved.Schedule())
	assert.Equal(t, []string{"SPOT_PRICE", "WEATHER_FORECAST"}, retrieved.InputDatasetCodes())
	assert.Equal(t, "FORWARD_CURVE_ELEC", retrieved.OutputDatasetCode())
	assert.Equal(t, "GB/SOUTH", retrieved.ReferenceDataResolutionKey())
	assert.Equal(t, domain.StrategyStatusDraft, retrieved.Status())
	assert.Equal(t, int64(1), retrieved.Version())
}

func TestStrategyRepository_Save_UpdateWithOptimisticLocking(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	strategy := createTestStrategy(t, "tenant-1", "Updatable Strategy")

	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	// Update the strategy (domain layer increments version)
	updated, err := strategy.UpdateDescription("Updated description")
	require.NoError(t, err)

	err = tc.Repo.Save(ctx, updated)
	require.NoError(t, err)

	// Verify the update
	retrieved, err := tc.Repo.FindByID(ctx, strategy.ID())
	require.NoError(t, err)

	assert.Equal(t, "Updated description", retrieved.Description())
	assert.Equal(t, int64(2), retrieved.Version())
}

func TestStrategyRepository_Save_OptimisticLockingConflict(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	strategy := createTestStrategy(t, "tenant-1", "Conflict Strategy")

	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	// Simulate concurrent update: update with correct version
	updated1, err := strategy.UpdateDescription("First update")
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, updated1)
	require.NoError(t, err)

	// Second update from stale version (version 1 expected, but db has version 2)
	updated2, err := strategy.UpdateDescription("Second update - stale")
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, updated2)
	assert.ErrorIs(t, err, domain.ErrVersionMismatch)
}

func TestStrategyRepository_Save_DuplicateActiveStrategy(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create and activate first strategy
	strategy1 := createTestStrategy(t, "tenant-1", "Same Name")
	err := tc.Repo.Save(ctx, strategy1)
	require.NoError(t, err)

	activated1, err := strategy1.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated1)
	require.NoError(t, err)

	// Create and activate second strategy with same name for same tenant
	strategy2 := createTestStrategy(t, "tenant-1", "Same Name")
	err = tc.Repo.Save(ctx, strategy2)
	require.NoError(t, err)

	activated2, err := strategy2.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated2)
	assert.ErrorIs(t, err, domain.ErrDuplicateActiveStrategy)
}

func TestStrategyRepository_Save_DuplicateNameDifferentTenant(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create and activate strategy for tenant-1
	s1 := createTestStrategy(t, "tenant-1", "Same Name")
	err := tc.Repo.Save(ctx, s1)
	require.NoError(t, err)
	activated1, err := s1.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated1)
	require.NoError(t, err)

	// Create and activate strategy for tenant-2 (should succeed - different tenant)
	s2 := createTestStrategy(t, "tenant-2", "Same Name")
	err = tc.Repo.Save(ctx, s2)
	require.NoError(t, err)
	activated2, err := s2.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated2)
	require.NoError(t, err)
}

func TestStrategyRepository_Save_DuplicateNameBothDraft(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Two DRAFT strategies with same name for same tenant - should succeed
	// because the unique constraint only applies to ACTIVE status
	s1 := createTestStrategy(t, "tenant-1", "Draft Name")
	err := tc.Repo.Save(ctx, s1)
	require.NoError(t, err)

	s2 := createTestStrategy(t, "tenant-1", "Draft Name")
	err = tc.Repo.Save(ctx, s2)
	require.NoError(t, err)
}

func TestStrategyRepository_FindByID_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrStrategyNotFound)
}

func TestStrategyRepository_FindByTenantAndName(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	strategy := createTestStrategy(t, "tenant-1", "Findable Strategy")
	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	activated, err := strategy.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated)
	require.NoError(t, err)

	// Find by tenant and name
	found, err := tc.Repo.FindByTenantAndName(ctx, "tenant-1", "Findable Strategy")
	require.NoError(t, err)
	assert.Equal(t, strategy.ID(), found.ID())
	assert.Equal(t, domain.StrategyStatusActive, found.Status())
}

func TestStrategyRepository_FindByTenantAndName_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repo.FindByTenantAndName(ctx, "tenant-1", "Nonexistent")
	assert.ErrorIs(t, err, domain.ErrStrategyNotFound)
}

func TestStrategyRepository_FindByTenantAndName_OnlyFindsActive(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a DRAFT strategy
	strategy := createTestStrategy(t, "tenant-1", "Draft Only")
	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	// Should not find DRAFT strategies
	_, err = tc.Repo.FindByTenantAndName(ctx, "tenant-1", "Draft Only")
	assert.ErrorIs(t, err, domain.ErrStrategyNotFound)
}

func TestStrategyRepository_ListByTenant(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create 3 strategies for tenant-1
	for i, name := range []string{"Strategy A", "Strategy B", "Strategy C"} {
		s := createTestStrategy(t, "tenant-1", name)
		err := tc.Repo.Save(ctx, s)
		require.NoError(t, err, "Failed to save strategy %d", i)
	}

	// Create 1 strategy for tenant-2
	s := createTestStrategy(t, "tenant-2", "Other Tenant")
	err := tc.Repo.Save(ctx, s)
	require.NoError(t, err)

	// List tenant-1 strategies
	strategies, nextToken, err := tc.Repo.ListByTenant(ctx, "tenant-1", domain.StrategyFilters{})
	require.NoError(t, err)
	assert.Len(t, strategies, 3)
	assert.Empty(t, nextToken)

	// Verify all belong to tenant-1
	for _, strategy := range strategies {
		assert.Equal(t, "tenant-1", strategy.TenantID())
	}
}

func TestStrategyRepository_ListByTenant_WithStatusFilter(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a DRAFT strategy
	draft := createTestStrategy(t, "tenant-1", "Draft Strategy")
	err := tc.Repo.Save(ctx, draft)
	require.NoError(t, err)

	// Create and activate another strategy
	active := createTestStrategy(t, "tenant-1", "Active Strategy")
	err = tc.Repo.Save(ctx, active)
	require.NoError(t, err)
	activated, err := active.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated)
	require.NoError(t, err)

	// Filter by ACTIVE status
	activeStatus := domain.StrategyStatusActive
	strategies, _, err := tc.Repo.ListByTenant(ctx, "tenant-1", domain.StrategyFilters{
		Status: &activeStatus,
	})
	require.NoError(t, err)
	assert.Len(t, strategies, 1)
	assert.Equal(t, "Active Strategy", strategies[0].Name())
}

func TestStrategyRepository_ListByTenant_Pagination(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create 5 strategies
	for i := 0; i < 5; i++ {
		s := createTestStrategy(t, "tenant-1", "Strategy "+string(rune('A'+i)))
		err := tc.Repo.Save(ctx, s)
		require.NoError(t, err)
	}

	// Get first page (2 items)
	page1, nextToken, err := tc.Repo.ListByTenant(ctx, "tenant-1", domain.StrategyFilters{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, page1, 2)
	assert.NotEmpty(t, nextToken)

	// Get second page
	page2, nextToken2, err := tc.Repo.ListByTenant(ctx, "tenant-1", domain.StrategyFilters{
		Limit:     2,
		PageToken: nextToken,
	})
	require.NoError(t, err)
	assert.Len(t, page2, 2)
	assert.NotEmpty(t, nextToken2)

	// Get third page (1 remaining)
	page3, nextToken3, err := tc.Repo.ListByTenant(ctx, "tenant-1", domain.StrategyFilters{
		Limit:     2,
		PageToken: nextToken2,
	})
	require.NoError(t, err)
	assert.Len(t, page3, 1)
	assert.Empty(t, nextToken3)

	// Verify no duplicates across pages
	allIDs := make(map[uuid.UUID]bool)
	for _, s := range page1 {
		allIDs[s.ID()] = true
	}
	for _, s := range page2 {
		assert.False(t, allIDs[s.ID()], "Duplicate found in page 2")
		allIDs[s.ID()] = true
	}
	for _, s := range page3 {
		assert.False(t, allIDs[s.ID()], "Duplicate found in page 3")
		allIDs[s.ID()] = true
	}
	assert.Len(t, allIDs, 5)
}

func TestStrategyRepository_ListByTenant_EmptyResult(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	strategies, nextToken, err := tc.Repo.ListByTenant(ctx, "nonexistent-tenant", domain.StrategyFilters{})
	require.NoError(t, err)
	assert.Empty(t, strategies)
	assert.Empty(t, nextToken)
}

func TestStrategyRepository_Save_NullableFields(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create strategy with empty optional fields
	s, err := domain.NewForecastingStrategy(
		"tenant-1",
		"Minimal Strategy",
		"", // empty description
		`result = [1.0]`,
		1,
		1,
		"0 0 * * *",
		[]string{"INPUT"},
		"OUTPUT",
		"", // empty reference key
	)
	require.NoError(t, err)

	err = tc.Repo.Save(ctx, s)
	require.NoError(t, err)

	retrieved, err := tc.Repo.FindByID(ctx, s.ID())
	require.NoError(t, err)

	assert.Equal(t, "", retrieved.Description())
	assert.Equal(t, "", retrieved.ReferenceDataResolutionKey())
}

func TestStrategyRepository_LifecycleTransitions(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create DRAFT
	strategy := createTestStrategy(t, "tenant-1", "Lifecycle Strategy")
	err := tc.Repo.Save(ctx, strategy)
	require.NoError(t, err)

	retrieved, err := tc.Repo.FindByID(ctx, strategy.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyStatusDraft, retrieved.Status())

	// Activate
	activated, err := strategy.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated)
	require.NoError(t, err)

	retrieved, err = tc.Repo.FindByID(ctx, strategy.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyStatusActive, retrieved.Status())
	assert.Equal(t, int64(2), retrieved.Version())

	// Deprecate
	deprecated, err := activated.Deprecate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, deprecated)
	require.NoError(t, err)

	retrieved, err = tc.Repo.FindByID(ctx, strategy.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyStatusDeprecated, retrieved.Status())
	assert.Equal(t, int64(3), retrieved.Version())
}

func TestStrategyRepository_ListAllActive(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a DRAFT strategy (should not appear)
	draft := createTestStrategy(t, "tenant-1", "Draft Strategy")
	err := tc.Repo.Save(ctx, draft)
	require.NoError(t, err)

	// Create and activate two strategies across different tenants
	s1 := createTestStrategy(t, "tenant-1", "Active Strategy 1")
	err = tc.Repo.Save(ctx, s1)
	require.NoError(t, err)
	activated1, err := s1.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated1)
	require.NoError(t, err)

	s2 := createTestStrategy(t, "tenant-2", "Active Strategy 2")
	err = tc.Repo.Save(ctx, s2)
	require.NoError(t, err)
	activated2, err := s2.Activate()
	require.NoError(t, err)
	err = tc.Repo.Save(ctx, activated2)
	require.NoError(t, err)

	// ListAllActive should return only the two active strategies
	results, err := tc.Repo.ListAllActive(ctx)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Verify all returned strategies are ACTIVE
	for _, s := range results {
		assert.Equal(t, domain.StrategyStatusActive, s.Status())
	}
}

func TestStrategyRepository_ListAllActive_Empty(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	results, err := tc.Repo.ListAllActive(ctx)
	require.NoError(t, err)
	assert.Empty(t, results)
}
