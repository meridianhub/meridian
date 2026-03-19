package valuationfeature

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	gormDB, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&Entity{}),
		testdb.WithTenant(testTenantID),
	)

	// Create partial unique index for active features (not expressible via GORM tags)
	err := gormDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_valuation_feature_account_instrument_active
		ON valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`).Error
	require.NoError(t, err)

	return gormDB, ctx, cleanup
}

func TestRepository_Create(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()
	parameters := map[string]interface{}{
		"source": "ECB",
		"lag":    "1D",
	}

	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, parameters, "creator")
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
	assert.Equal(t, LifecycleStatusInitiated, retrieved.LifecycleStatus)
	assert.Equal(t, "creator", retrieved.CreatedBy)
	assert.Equal(t, parameters, retrieved.Parameters)
}

func TestRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRepository_FindByAccountIDAndInstrument(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate a feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
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
	assert.Equal(t, LifecycleStatusActive, retrieved.LifecycleStatus)
}

func TestRepository_FindByAccountIDAndInstrument_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()

	knowledgeAt := time.Now()
	_, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRepository_FindByAccountIDAndInstrument_BiTemporal(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate a feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
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
	assert.ErrorIs(t, err, ErrNotFound)

	// Query after validity range - should not find
	knowledgeAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", knowledgeAt)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRepository_FindByAccountID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create multiple features
	feature1, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature1))

	feature2, err := NewValuationFeature(accountID, "EUR", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature2.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature2))

	// Create feature for different account
	otherAccountID := uuid.New()
	feature3, err := NewValuationFeature(otherAccountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature3))

	// Find all features for account
	features, err := repo.FindByAccountID(ctx, accountID, nil)
	require.NoError(t, err)
	assert.Len(t, features, 2)

	// Find only active features for account
	activeStatus := LifecycleStatusActive
	features, err = repo.FindByAccountID(ctx, accountID, &activeStatus)
	require.NoError(t, err)
	assert.Len(t, features, 1)
	assert.Equal(t, "EUR", features[0].InstrumentCode)
}

func TestRepository_FindByMethodID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID1 := uuid.New()
	methodID2 := uuid.New()

	// Create active feature with methodID1
	feature1, err := NewValuationFeature(accountID, "USD", methodID1, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature1.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature1))

	// Create inactive feature with methodID1 (should not be returned)
	feature2, err := NewValuationFeature(accountID, "EUR", methodID1, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature2))

	// Create active feature with methodID2
	feature3, err := NewValuationFeature(accountID, "GBP", methodID2, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature3.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature3))

	// Find active features for methodID1
	features, err := repo.FindByMethodID(ctx, methodID1)
	require.NoError(t, err)
	assert.Len(t, features, 1)
	assert.Equal(t, "USD", features[0].InstrumentCode)
}

func TestRepository_Update(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
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
	assert.Equal(t, LifecycleStatusActive, retrieved.LifecycleStatus)
	assert.Equal(t, "activator", retrieved.UpdatedBy)
}

func TestRepository_Update_OptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
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
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestRepository_FindByIDForUpdate(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))

	// Retrieve with pessimistic lock
	locked, err := repo.FindByIDForUpdate(ctx, feature.ID)
	require.NoError(t, err)
	assert.Equal(t, feature.ID, locked.ID)
}

func TestRepository_FindByIDForUpdate_ConcurrentBlocking(t *testing.T) {
	rawDB, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(rawDB)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create feature
	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, feature))

	// Goroutine 1: hold FOR UPDATE lock, update version while holding it
	lockAcquired := make(chan struct{})
	canRelease := make(chan struct{})
	g1Done := make(chan struct{})

	go func() {
		defer close(g1Done)
		txRepo := NewRepository(rawDB)
		_ = db.WithGormTenantTransaction(ctx, rawDB, func(tx *gorm.DB) error {
			withinTx := txRepo.WithTx(tx)
			// Acquire pessimistic lock within this transaction
			var entity Entity
			result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", feature.ID).
				First(&entity)
			require.NoError(t, result.Error)

			// Signal that lock is held
			close(lockAcquired)

			// Hold the lock until main goroutine says release
			<-canRelease

			// Update version while we hold the lock
			locked, err := toDomain(&entity)
			require.NoError(t, err)
			require.NoError(t, locked.Activate("user1"))
			require.NoError(t, withinTx.Update(ctx, locked))
			return nil
		})
	}()

	// Wait for goroutine 1 to acquire the lock
	<-lockAcquired

	// Goroutine 2: attempt to get lock - should block until g1 releases
	g2Done := make(chan int)
	go func() {
		f, err := repo.FindByIDForUpdate(ctx, feature.ID)
		require.NoError(t, err)
		g2Done <- f.Version
	}()

	// Verify g2 is still blocked (hasn't returned yet)
	select {
	case <-g2Done:
		t.Fatal("goroutine 2 acquired the lock while goroutine 1 still held it")
	case <-time.After(200 * time.Millisecond):
		// Expected: g2 is blocked
	}

	// Release the lock from g1
	close(canRelease)
	<-g1Done

	// g2 should now complete and see the updated version
	select {
	case version := <-g2Done:
		assert.Equal(t, 2, version, "goroutine 2 should see version 2 after g1 committed")
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine 2 did not complete after lock was released")
	}
}

func TestRepository_UniqueConstraint(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()

	// Create and activate first feature
	feature1, err := NewValuationFeature(accountID, "USD", methodID, 1, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature1.Activate("activator"))
	require.NoError(t, repo.Create(ctx, feature1))

	// Create and activate second feature with same account and instrument - should fail unique constraint
	feature2, err := NewValuationFeature(accountID, "USD", methodID, 2, nil, "creator")
	require.NoError(t, err)
	require.NoError(t, feature2.Activate("activator"))
	err = repo.Create(ctx, feature2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idx_valuation_feature_account_instrument_active")
}

func TestRepository_ParametersMarshaling(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
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

	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, parameters, "creator")
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
