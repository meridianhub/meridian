package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceRepository_Save_NewSource(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create a new data source
	source, err := domain.NewDataSource(
		"TEST_SOURCE",
		"Test Source",
		"A test data source",
		domain.SourceTypeAPI,
		85,
	)
	require.NoError(t, err)

	// Save it
	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)

	// Retrieve by code and verify
	retrieved, err := tc.Repos.Source.FindByCode(ctx, "TEST_SOURCE")
	require.NoError(t, err)

	assert.Equal(t, "TEST_SOURCE", retrieved.Code())
	assert.Equal(t, "Test Source", retrieved.Name())
	assert.Equal(t, "A test data source", retrieved.Description())
	assert.Equal(t, 85, retrieved.TrustLevel())
}

func TestSourceRepository_Save_UpdateExisting(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create and save initial source
	source, err := domain.NewDataSource(
		"UPDATE_SOURCE",
		"Original Name",
		"Original description",
		domain.SourceTypeManual,
		50,
	)
	require.NoError(t, err)

	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)

	// Create updated source with same code but different attributes
	updatedSource, err := domain.NewDataSource(
		"UPDATE_SOURCE",
		"Updated Name",
		"Updated description",
		domain.SourceTypeManual,
		75,
	)
	require.NoError(t, err)

	// Save should update (upsert behavior)
	err = tc.Repos.Source.Save(ctx, updatedSource)
	require.NoError(t, err)

	// Verify the update
	retrieved, err := tc.Repos.Source.FindByCode(ctx, "UPDATE_SOURCE")
	require.NoError(t, err)

	assert.Equal(t, "Updated Name", retrieved.Name())
	assert.Equal(t, "Updated description", retrieved.Description())
	assert.Equal(t, 75, retrieved.TrustLevel())
}

func TestSourceRepository_FindByID(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create and save a source
	source, err := domain.NewDataSource(
		"FIND_BY_ID_SOURCE",
		"Find By ID Source",
		"Testing FindByID",
		domain.SourceTypeScheduled,
		60,
	)
	require.NoError(t, err)

	err = tc.Repos.Source.Save(ctx, source)
	require.NoError(t, err)

	// Get the ID by finding by code first
	byCode, err := tc.Repos.Source.FindByCode(ctx, "FIND_BY_ID_SOURCE")
	require.NoError(t, err)

	// Now find by ID
	byID, err := tc.Repos.Source.FindByID(ctx, byCode.ID())
	require.NoError(t, err)

	assert.Equal(t, byCode.Code(), byID.Code())
	assert.Equal(t, byCode.Name(), byID.Name())
}

func TestSourceRepository_FindByID_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repos.Source.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
}

func TestSourceRepository_FindByCode_NotFound(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	_, err := tc.Repos.Source.FindByCode(ctx, "NON_EXISTENT_SOURCE")
	assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
}

func TestSourceRepository_List(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create multiple sources with different trust levels
	sources := []struct {
		code       string
		trustLevel int
	}{
		{"HIGH_TRUST", 90},
		{"MEDIUM_TRUST", 60},
		{"LOW_TRUST", 30},
	}

	for _, s := range sources {
		source, err := domain.NewDataSource(
			s.code,
			s.code,
			"Description",
			domain.SourceTypeAPI,
			s.trustLevel,
		)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)
	}

	// List all sources
	all, err := tc.Repos.Source.List(ctx, false)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Verify ordering by trust level (descending)
	assert.Equal(t, 90, all[0].TrustLevel())
	assert.Equal(t, 60, all[1].TrustLevel())
	assert.Equal(t, 30, all[2].TrustLevel())
}

func TestSourceRepository_TrustLevel_Validation(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Trust level 0 should be valid
	source0, err := domain.NewDataSource(
		"TRUST_ZERO",
		"Zero Trust",
		"Minimal trust",
		domain.SourceTypeAPI,
		0,
	)
	require.NoError(t, err)
	err = tc.Repos.Source.Save(ctx, source0)
	require.NoError(t, err)

	// Trust level 100 should be valid
	source100, err := domain.NewDataSource(
		"TRUST_MAX",
		"Max Trust",
		"Maximum trust",
		domain.SourceTypeAPI,
		100,
	)
	require.NoError(t, err)
	err = tc.Repos.Source.Save(ctx, source100)
	require.NoError(t, err)

	// Verify they were saved correctly
	retrieved0, err := tc.Repos.Source.FindByCode(ctx, "TRUST_ZERO")
	require.NoError(t, err)
	assert.Equal(t, 0, retrieved0.TrustLevel())

	retrieved100, err := tc.Repos.Source.FindByCode(ctx, "TRUST_MAX")
	require.NoError(t, err)
	assert.Equal(t, 100, retrieved100.TrustLevel())
}
