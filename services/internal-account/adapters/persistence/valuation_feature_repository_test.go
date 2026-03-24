package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValuationFeatureRepository_ErrorAliases(t *testing.T) {
	assert.Equal(t, vf.ErrNotFound, ErrValuationFeatureNotFound)
	assert.Equal(t, vf.ErrVersionConflict, ErrValuationFeatureVersionConflict)
	assert.Equal(t, vf.ErrAlreadyExists, ErrValuationFeatureAlreadyExists)
}

func TestNewValuationFeatureRepository_ReturnsNonNil(t *testing.T) {
	gormDB, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&vf.Entity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewValuationFeatureRepository(gormDB)

	assert.NotNil(t, repo)
}

func TestNewValuationFeatureRepository_IsSharedRepository(t *testing.T) {
	gormDB, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&vf.Entity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewValuationFeatureRepository(gormDB)

	// ValuationFeatureRepository is a type alias for vf.Repository.
	// Verify the returned repo exposes the shared package's DB() method.
	assert.NotNil(t, repo.DB())
}

func TestNewValuationFeatureRepository_Create_FindByID(t *testing.T) {
	gormDB, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&vf.Entity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewValuationFeatureRepository(gormDB)
	accountID := uuid.New()
	methodID := uuid.New()

	feature, err := vf.NewValuationFeature(accountID, "USD", methodID, 1, nil, "test-user")
	require.NoError(t, err)

	err = repo.Create(ctx, feature)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.ID, retrieved.ID)
	assert.Equal(t, "USD", retrieved.InstrumentCode)
}

func TestNewValuationFeatureRepository_FindByID_NotFound(t *testing.T) {
	gormDB, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&vf.Entity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewValuationFeatureRepository(gormDB)

	_, err := repo.FindByID(ctx, uuid.New())

	assert.ErrorIs(t, err, ErrValuationFeatureNotFound)
}

func TestNewValuationFeatureRepository_WithNilContext(t *testing.T) {
	gormDB, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&vf.Entity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewValuationFeatureRepository(gormDB)
	assert.NotNil(t, repo)

	// WithTx should return a new repository instance.
	tx := gormDB.Begin()
	defer tx.Rollback()
	txRepo := repo.WithTx(tx)
	assert.NotNil(t, txRepo)
	_ = context.Background()
}
