package valuationfeature

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDBForSeeder reuses setupTestDB defined in repository_test.go.

func TestProductTypeSeeder_SeedFromProductType_ActiveTemplates(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	seeder := NewProductTypeSeeder(repo)

	accountID := uuid.New()
	methodID1 := uuid.New()
	methodID2 := uuid.New()

	productType := &accounttype.Definition{
		ID:   uuid.New(),
		Code: "TEST_TYPE",
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "USD",
				ValuationMethodID:      methodID1,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusActive,
				Parameters:             map[string]any{"source": "ECB"},
			},
			{
				ID:                     uuid.New(),
				InputInstrument:        "EUR",
				ValuationMethodID:      methodID2,
				ValuationMethodVersion: 2,
				Status:                 accounttype.StatusActive,
				Parameters:             nil,
			},
		},
	}

	now := time.Now().UTC()
	err := seeder.SeedFromProductType(ctx, accountID, productType, now)
	require.NoError(t, err)

	active := LifecycleStatusActive
	features, err := repo.FindByAccountID(ctx, accountID, &active)
	require.NoError(t, err)
	assert.Len(t, features, 2, "should seed 2 ACTIVE features from 2 ACTIVE templates")

	byCode := make(map[string]*ValuationFeature, len(features))
	for _, f := range features {
		byCode[f.InstrumentCode] = f
	}

	usd := byCode["USD"]
	require.NotNil(t, usd)
	assert.Equal(t, methodID1, usd.ValuationMethodID)
	assert.Equal(t, 1, usd.ValuationMethodVersion)
	assert.Equal(t, LifecycleStatusActive, usd.LifecycleStatus)
	assert.Equal(t, "ECB", usd.Parameters["source"])

	eur := byCode["EUR"]
	require.NotNil(t, eur)
	assert.Equal(t, methodID2, eur.ValuationMethodID)
	assert.Equal(t, 2, eur.ValuationMethodVersion)
	assert.Equal(t, LifecycleStatusActive, eur.LifecycleStatus)
}

func TestProductTypeSeeder_SeedFromProductType_SkipsNonActiveTemplates(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	seeder := NewProductTypeSeeder(repo)

	accountID := uuid.New()
	methodID := uuid.New()

	productType := &accounttype.Definition{
		ID:   uuid.New(),
		Code: "TEST_TYPE",
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "USD",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusActive,
			},
			{
				ID:                     uuid.New(),
				InputInstrument:        "EUR",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusDeprecated, // should be skipped
			},
			{
				ID:                     uuid.New(),
				InputInstrument:        "GBP",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusDraft, // should be skipped
			},
		},
	}

	now := time.Now().UTC()
	err := seeder.SeedFromProductType(ctx, accountID, productType, now)
	require.NoError(t, err)

	features, err := repo.FindByAccountID(ctx, accountID, nil)
	require.NoError(t, err)
	assert.Len(t, features, 1, "only ACTIVE templates should be seeded")
	assert.Equal(t, "USD", features[0].InstrumentCode)
}

func TestProductTypeSeeder_SeedFromProductType_Idempotent(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	seeder := NewProductTypeSeeder(repo)

	accountID := uuid.New()
	methodID := uuid.New()

	productType := &accounttype.Definition{
		ID:   uuid.New(),
		Code: "TEST_TYPE",
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "USD",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusActive,
			},
		},
	}

	now := time.Now().UTC()

	// First seed
	err := seeder.SeedFromProductType(ctx, accountID, productType, now)
	require.NoError(t, err)

	// Second seed - must not fail and must not create duplicates
	err = seeder.SeedFromProductType(ctx, accountID, productType, now)
	require.NoError(t, err, "second seed must be idempotent")

	active := LifecycleStatusActive
	features, err := repo.FindByAccountID(ctx, accountID, &active)
	require.NoError(t, err)
	assert.Len(t, features, 1, "no duplicates after double seed")
}

func TestProductTypeSeeder_SeedFromProductType_EmptyTemplates(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	seeder := NewProductTypeSeeder(repo)

	accountID := uuid.New()
	productType := &accounttype.Definition{
		ID:               uuid.New(),
		Code:             "EMPTY_TYPE",
		ValuationMethods: []accounttype.ValuationMethodTemplate{},
	}

	now := time.Now().UTC()
	err := seeder.SeedFromProductType(ctx, accountID, productType, now)
	require.NoError(t, err)

	features, err := repo.FindByAccountID(ctx, accountID, nil)
	require.NoError(t, err)
	assert.Empty(t, features)
}

func TestRepository_UpsertFeature_CreatesFeature(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID := uuid.New()
	now := time.Now().UTC()
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	feature := &ValuationFeature{
		ID:                     uuid.New(),
		AccountID:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodID:      methodID,
		ValuationMethodVersion: 1,
		LifecycleStatus:        LifecycleStatusActive,
		ValidFrom:              now,
		ValidTo:                maxTime,
		CreatedAt:              now,
		CreatedBy:              "system",
		UpdatedAt:              now,
		UpdatedBy:              "system",
		Version:                1,
	}

	err := repo.UpsertFeature(ctx, feature)
	require.NoError(t, err)

	retrieved, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", now)
	require.NoError(t, err)
	assert.Equal(t, methodID, retrieved.ValuationMethodID)
	assert.Equal(t, LifecycleStatusActive, retrieved.LifecycleStatus)
}

func TestRepository_UpsertFeature_DoesNothingOnConflict(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	accountID := uuid.New()
	methodID1 := uuid.New()
	methodID2 := uuid.New()
	now := time.Now().UTC()
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	first := &ValuationFeature{
		ID:                     uuid.New(),
		AccountID:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodID:      methodID1,
		ValuationMethodVersion: 1,
		LifecycleStatus:        LifecycleStatusActive,
		ValidFrom:              now,
		ValidTo:                maxTime,
		CreatedAt:              now,
		CreatedBy:              "system",
		UpdatedAt:              now,
		UpdatedBy:              "system",
		Version:                1,
	}
	require.NoError(t, repo.UpsertFeature(ctx, first))

	// Second upsert with different method ID for the same (account, instrument)
	second := &ValuationFeature{
		ID:                     uuid.New(),
		AccountID:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodID:      methodID2,
		ValuationMethodVersion: 2,
		LifecycleStatus:        LifecycleStatusActive,
		ValidFrom:              now,
		ValidTo:                maxTime,
		CreatedAt:              now,
		CreatedBy:              "system",
		UpdatedAt:              now,
		UpdatedBy:              "system",
		Version:                1,
	}
	err := repo.UpsertFeature(ctx, second)
	require.NoError(t, err, "upsert on conflict must not return an error")

	// The original feature must remain unchanged
	retrieved, err := repo.FindByAccountIDAndInstrument(ctx, accountID, "USD", now)
	require.NoError(t, err)
	assert.Equal(t, first.ID, retrieved.ID, "original feature ID must be preserved")
	assert.Equal(t, methodID1, retrieved.ValuationMethodID, "original method must not be overwritten")
}

func TestProductTypeSeeder_SeedFromProductType_MultipleTenants(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a second tenant schema and migrate tables using AutoMigrate
	// (must match the GORM-created schema from the first tenant to avoid type mismatches)
	tid2 := tenant.TenantID("test_tenant_2")
	schemaName2 := tid2.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName2)).Error
	require.NoError(t, err)
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName2)).Error
	require.NoError(t, err)
	err = db.AutoMigrate(&Entity{})
	require.NoError(t, err)
	err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_vf_t2_account_instrument_active
		ON valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`).Error
	require.NoError(t, err)
	// Restore search_path to the primary tenant
	primarySchema := tenant.TenantID(testTenantID).SchemaName()
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", primarySchema)).Error
	require.NoError(t, err)

	ctx2 := tenant.WithTenant(context.Background(), tid2)

	repo := NewRepository(db)
	seeder := NewProductTypeSeeder(repo)

	accountID1 := uuid.New()
	accountID2 := uuid.New()
	methodID := uuid.New()
	now := time.Now().UTC()

	productType := &accounttype.Definition{
		ID:   uuid.New(),
		Code: "TEST_TYPE",
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "USD",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusActive,
			},
		},
	}

	// Seed for tenant 1
	err = seeder.SeedFromProductType(ctx, accountID1, productType, now)
	require.NoError(t, err)

	// Seed for tenant 2
	err = seeder.SeedFromProductType(ctx2, accountID2, productType, now)
	require.NoError(t, err)

	// Tenant 1 sees its own feature
	active := LifecycleStatusActive
	features1, err := repo.FindByAccountID(ctx, accountID1, &active)
	require.NoError(t, err)
	assert.Len(t, features1, 1)

	// Tenant 2 sees its own feature
	features2, err := repo.FindByAccountID(ctx2, accountID2, &active)
	require.NoError(t, err)
	assert.Len(t, features2, 1)
}
