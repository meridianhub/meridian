package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupValuationFeatureTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&ValuationFeatureEntity{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the valuation_features table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.valuation_features (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		instrument_code VARCHAR(32) NOT NULL,
		valuation_method_id UUID NOT NULL,
		valuation_method_version INT NOT NULL,
		parameters JSONB,
		lifecycle_status VARCHAR(16) NOT NULL,
		valid_from TIMESTAMP WITH TIME ZONE NOT NULL,
		valid_to TIMESTAMP WITH TIME ZONE NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(100) NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_by VARCHAR(100) NOT NULL,
		version INT NOT NULL DEFAULT 1,
		CONSTRAINT chk_valuation_feature_lifecycle_status CHECK (lifecycle_status IN ('INITIATED','ACTIVE','TERMINATED'))
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create unique index for active features
	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX idx_valuation_feature_account_instrument_active
		ON %q.valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestValuationFeatureRepository_Create(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()
	parameters := map[string]interface{}{
		"source": "ECB",
		"lag":    "1D",
	}

	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, parameters, "creator")
	require.NoError(t, err)

	err = repo.Create(ctx, feature)
	require.NoError(t, err)

	// Verify feature was saved
	retrieved, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)

	assert.Equal(t, feature.ID, retrieved.ID)
	assert.Equal(t, accountID, retrieved.AccountID)
	assert.Equal(t, "USD", retrieved.InstrumentCode)
	assert.Equal(t, methodID, retrieved.ValuationMethodID)
	assert.Equal(t, 1, retrieved.ValuationMethodVersion)
	assert.Equal(t, domain.ValuationFeatureLifecycleStatusInitiated, retrieved.LifecycleStatus)
	assert.Equal(t, "creator", retrieved.CreatedBy)
	assert.Equal(t, parameters, retrieved.Parameters)
}

func TestValuationFeatureRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrValuationFeatureNotFound)
}

func TestValuationFeatureRepository_FindByAccountIDAndInstrument(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate a feature
	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature))

	// Find the feature
	knowledgeAt := time.Now()
	retrieved, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	require.NoError(t, err)

	assert.Equal(t, feature.ID, retrieved.ID)
	assert.Equal(t, accountID, retrieved.AccountID)
	assert.Equal(t, "USD", retrieved.InstrumentCode)
	assert.Equal(t, domain.ValuationFeatureLifecycleStatusActive, retrieved.LifecycleStatus)
}

func TestValuationFeatureRepository_FindByAccountIDAndInstrument_NotFound(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()

	knowledgeAt := time.Now()
	_, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	assert.ErrorIs(t, err, ErrValuationFeatureNotFound)
}

func TestValuationFeatureRepository_FindByAccountIDAndInstrument_BiTemporal(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate a feature
	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature.Activate("activator"))

	// Set specific validity range
	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	feature.ValidFrom = validFrom
	feature.ValidTo = validTo

	require.NoError(t, repo.Create(ctx, feature))

	// Query within validity range - should find
	knowledgeAt := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	retrieved, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	require.NoError(t, err)
	assert.Equal(t, feature.ID, retrieved.ID)

	// Query before validity range - should not find
	knowledgeAt = time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	_, err = repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	assert.ErrorIs(t, err, ErrValuationFeatureNotFound)

	// Query after validity range - should not find
	knowledgeAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	assert.ErrorIs(t, err, ErrValuationFeatureNotFound)
}

func TestValuationFeatureRepository_FindByAccountID(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create multiple features
	feature1, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature1))

	feature2, err := domain.NewValuationFeature(accountID, "EUR", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature2.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature2))

	// Create feature for different account
	otherAccountID := uuid.New()
	feature3, err := domain.NewValuationFeature(otherAccountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature3))

	// Find all features for account
	features, err := repo.FindByAccountID(ctx, accountID, nil)
	require.NoError(t, err)
	assert.Len(t, features, 2)

	// Find only active features for account
	activeStatus := domain.ValuationFeatureLifecycleStatusActive
	features, err = repo.FindByAccountID(ctx, accountID, &activeStatus)
	require.NoError(t, err)
	assert.Len(t, features, 1)
	assert.Equal(t, "EUR", features[0].InstrumentCode)
}

func TestValuationFeatureRepository_FindByMethodID(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID1 := uuid.New()
	methodID2 := uuid.New()

	// Create active feature with methodID1
	feature1, err := domain.NewValuationFeature(accountID, "USD", methodID1, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature1.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature1))

	// Create inactive feature with methodID1 (should not be returned)
	feature2, err := domain.NewValuationFeature(accountID, "EUR", methodID1, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature2))

	// Create active feature with methodID2
	feature3, err := domain.NewValuationFeature(accountID, "GBP", methodID2, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature3.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature3))

	// Find active features for methodID1
	features, err := repo.FindByMethodID(ctx, methodID1)
	require.NoError(t, err)
	assert.Len(t, features, 1)
	assert.Equal(t, "USD", features[0].InstrumentCode)
}

func TestValuationFeatureRepository_Update(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))
	initialVersion := feature.Version

	// Activate feature
	require.NoError(t, feature.Activate("activator"))
	err = repo.Update(ctx, feature)
	require.NoError(t, err)

	// Verify version was incremented
	assert.Equal(t, initialVersion+1, feature.Version)

	// Retrieve and verify update
	retrieved, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ValuationFeatureLifecycleStatusActive, retrieved.LifecycleStatus)
	assert.Equal(t, "activator", retrieved.UpdatedBy)
}

func TestValuationFeatureRepository_Update_OptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))

	// Retrieve twice to simulate concurrent access
	feature1, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)
	feature2, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, feature1.Activate("user1"))
	err = repo.Update(ctx, feature1)
	require.NoError(t, err)

	// Second update fails due to version conflict
	require.NoError(t, feature2.Activate("user2"))
	err = repo.Update(ctx, feature2)
	assert.ErrorIs(t, err, ErrValuationFeatureVersionConflict)
}

func TestValuationFeatureRepository_FindByIDForUpdate(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))

	// Retrieve with pessimistic lock
	locked, err := repo.FindByIDForUpdate(ctx, feature.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.ID, locked.ID)
}

func TestValuationFeatureRepository_UniqueConstraint(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate first feature
	feature1, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature1.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature1))

	// Create and activate second feature with same account and instrument - should fail unique constraint
	feature2, err := domain.NewValuationFeature(accountID, "USD", methodID, 2, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature2.Activate("activator"))
	err = repo.Create(ctx, feature2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idx_valuation_feature_account_instrument_active")
}

func TestValuationFeatureRepository_ParametersMarshaling(t *testing.T) {
	db, ctx, cleanup := setupValuationFeatureTestDB(t)
	defer cleanup()

	repo := NewValuationFeatureRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature with complex parameters
	parameters := map[string]interface{}{
		"source":          "ECB",
		"lag":             "1D",
		"fallback_source": "Bloomberg",
		"precision":       6,
		"rounding":        "half_up",
	}

	feature, err := domain.NewValuationFeature(accountID, "USD", methodID, 1, parameters, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))

	// Retrieve and verify parameters are unmarshaled correctly
	retrieved, err := repo.FindByID(ctx, feature.ID)
	require.NoError(t, err)
	assert.Equal(t, "ECB", retrieved.Parameters["source"])
	assert.Equal(t, "1D", retrieved.Parameters["lag"])
	assert.Equal(t, "Bloomberg", retrieved.Parameters["fallback_source"])
	// JSON unmarshaling converts numbers to float64
	assert.Equal(t, float64(6), retrieved.Parameters["precision"])
	assert.Equal(t, "half_up", retrieved.Parameters["rounding"])
}
