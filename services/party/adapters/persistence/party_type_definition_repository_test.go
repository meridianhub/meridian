package persistence

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testPartyTypeSchema = `{"type":"object","properties":{"annual_income":{"type":"string"}}}`

func setupPartyTypeTestDB(t *testing.T) (*gorm.DB, interface{ String() string }, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&PartyTypeDefinitionEntity{}})

	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_type_definition (
		id               UUID        NOT NULL DEFAULT gen_random_uuid(),
		tenant_id        VARCHAR(100) NOT NULL,
		party_type       VARCHAR(100) NOT NULL,
		attribute_schema TEXT         NOT NULL,
		validation_cel   TEXT         NOT NULL DEFAULT '',
		eligibility_cel  TEXT         NOT NULL DEFAULT '',
		error_message_cel TEXT        NOT NULL DEFAULT '',
		version          BIGINT       NOT NULL DEFAULT 1,
		created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
		updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
		PRIMARY KEY (id),
		UNIQUE (tenant_id, party_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	return db, tid, cleanup
}

func TestPartyTypeDefinitionRepository_Create(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	entity := &PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		ValidationCEL:   "",
		EligibilityCEL:  "",
		ErrorMessageCEL: "",
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	err := repo.Create(ctx, entity)
	require.NoError(t, err)
}

func TestPartyTypeDefinitionRepository_Create_DuplicateReturnsError(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	entity1 := &PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tid.String(),
		PartyType:       "ORGANIZATION",
		AttributeSchema: testPartyTypeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.Create(ctx, entity1))

	entity2 := &PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tid.String(),
		PartyType:       "ORGANIZATION",
		AttributeSchema: testPartyTypeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	err := repo.Create(ctx, entity2)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeDefinitionExists))
}

func TestPartyTypeDefinitionRepository_GetByID(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	id := uuid.New()
	entity := &PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		ValidationCEL:   "true",
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.Create(ctx, entity))

	retrieved, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, retrieved.ID)
	assert.Equal(t, "PERSON", retrieved.PartyType)
	assert.Equal(t, testPartyTypeSchema, retrieved.AttributeSchema)
	assert.Equal(t, "true", retrieved.ValidationCEL)
	assert.Equal(t, int64(1), retrieved.Version)
}

func TestPartyTypeDefinitionRepository_GetByID_NotFound(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	_, err := repo.GetByID(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeDefinitionNotFound))
}

func TestPartyTypeDefinitionRepository_GetByTenantAndType(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	entity := &PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tid.String(),
		PartyType:       "CORPORATE",
		AttributeSchema: testPartyTypeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.Create(ctx, entity))

	retrieved, err := repo.GetByTenantAndType(ctx, tid.String(), "CORPORATE")
	require.NoError(t, err)
	assert.Equal(t, entity.ID, retrieved.ID)
	assert.Equal(t, "CORPORATE", retrieved.PartyType)
}

func TestPartyTypeDefinitionRepository_GetByTenantAndType_NotFound(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	_, err := repo.GetByTenantAndType(ctx, tid.String(), "NONEXISTENT")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeDefinitionNotFound))
}

func TestPartyTypeDefinitionRepository_ListByTenant(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	for _, pt := range []string{"PERSON", "ORGANIZATION", "TRUST"} {
		e := &PartyTypeDefinitionEntity{
			ID:              uuid.New(),
			TenantID:        tid.String(),
			PartyType:       pt,
			AttributeSchema: testPartyTypeSchema,
			Version:         1,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		require.NoError(t, repo.Create(ctx, e))
	}

	results, err := repo.ListByTenant(ctx, tid.String(), "")
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestPartyTypeDefinitionRepository_ListByTenant_FilterByType(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	for _, pt := range []string{"PERSON", "ORGANIZATION"} {
		e := &PartyTypeDefinitionEntity{
			ID:              uuid.New(),
			TenantID:        tid.String(),
			PartyType:       pt,
			AttributeSchema: testPartyTypeSchema,
			Version:         1,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		require.NoError(t, repo.Create(ctx, e))
	}

	results, err := repo.ListByTenant(ctx, tid.String(), "PERSON")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "PERSON", results[0].PartyType)
}

func TestPartyTypeDefinitionRepository_ListByTenant_Empty(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	results, err := repo.ListByTenant(ctx, tid.String(), "")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestPartyTypeDefinitionRepository_Update(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	id := uuid.New()
	entity := &PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.Create(ctx, entity))

	// Update: increment version, change schema
	entity.AttributeSchema = `{"type":"object","properties":{"income":{"type":"number"}}}`
	entity.ValidationCEL = "true"
	entity.Version = 2

	err := repo.Update(ctx, entity)
	require.NoError(t, err)

	// Verify update persisted
	updated, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	assert.Equal(t, "true", updated.ValidationCEL)
	assert.Contains(t, updated.AttributeSchema, "income")
}

func TestPartyTypeDefinitionRepository_Update_VersionConflict(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	id := uuid.New()
	entity := &PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.Create(ctx, entity))

	// Try to update with wrong version (version=1, but expecting DB to have version=0 for update to succeed)
	// entity.Version=1 means expectedDBVersion=0, but DB has version=1 → conflict
	stale := &PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		Version:         1, // target=1, so expectedDB=0, but DB has 1 → conflict
		UpdatedAt:       time.Now(),
	}
	err := repo.Update(ctx, stale)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeVersionConflict))
}

func TestPartyTypeDefinitionRepository_Update_NotFound(t *testing.T) {
	db, tid, cleanup := setupPartyTypeTestDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID(tid.String()))
	repo := NewPartyTypeDefinitionRepository(db)

	entity := &PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tid.String(),
		PartyType:       "PERSON",
		AttributeSchema: testPartyTypeSchema,
		Version:         2, // targets DB version=1, but record doesn't exist
		UpdatedAt:       time.Now(),
	}
	err := repo.Update(ctx, entity)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeDefinitionNotFound))
}
