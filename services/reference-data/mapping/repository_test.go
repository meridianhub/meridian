package mapping_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/mapping"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

func newTestDef() *mapping.Definition {
	return &mapping.Definition{
		Name:          "test-mapping",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRPC:     "InitiatePaymentOrder",
		Version:       1,
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}
}

func TestRepository_CreateAndGetByID(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant01", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))
	assert.NotEqual(t, uuid.Nil, def.ID)
	assert.Equal(t, mapping.StatusDraft, def.Status)

	got, err := repo.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, got.ID)
	assert.Equal(t, "test-mapping", got.Name)
	assert.Equal(t, "InitiatePaymentOrder", got.TargetRPC)
	assert.Len(t, got.Fields, 1)
}

func TestRepository_Create_Duplicate_ReturnsAlreadyExists(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant02", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	def2 := newTestDef()
	def2.ID = uuid.Nil
	err := repo.Create(ctx, def2)
	assert.ErrorIs(t, err, mapping.ErrAlreadyExists)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant03", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	_, err := repo.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_Update(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant04", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	def.Name = "updated-mapping"
	def.ExternalSchema = `{"type":"object"}`
	err := repo.Update(ctx, def, def.UpdatedAt)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated-mapping", got.Name)
	assert.Equal(t, `{"type":"object"}`, got.ExternalSchema)
}

func TestRepository_Update_NotDraft_Fails(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant05", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))
	require.NoError(t, repo.UpdateStatus(ctx, def.ID, mapping.StatusActive))

	def.Name = "changed"
	err := repo.Update(ctx, def, def.UpdatedAt)
	assert.ErrorIs(t, err, mapping.ErrNotDraft)
}

func TestRepository_UpdateStatus(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant06", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	require.NoError(t, repo.UpdateStatus(ctx, def.ID, mapping.StatusActive))

	got, err := repo.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, mapping.StatusActive, got.Status)
}

func TestRepository_ListByTenant(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant07", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	for i := 1; i <= 3; i++ {
		d := &mapping.Definition{
			Name:          "mapping-" + string(rune('A'-1+i)),
			TargetService: "svc",
			TargetRPC:     "Rpc",
			Version:       1,
		}
		require.NoError(t, repo.Create(ctx, d))
	}

	defs, total, err := repo.ListByTenant(ctx, "", "", 10, "")
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, defs, 3)
}

func TestRepository_ListByTenant_StatusFilter(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant08", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))
	require.NoError(t, repo.UpdateStatus(ctx, def.ID, mapping.StatusActive))

	def2 := &mapping.Definition{Name: "draft-one", TargetService: "svc", TargetRPC: "Rpc", Version: 1}
	require.NoError(t, repo.Create(ctx, def2))

	activeDefs, _, err := repo.ListByTenant(ctx, mapping.StatusActive, "", 10, "")
	require.NoError(t, err)
	assert.Len(t, activeDefs, 1)

	draftDefs, _, err := repo.ListByTenant(ctx, mapping.StatusDraft, "", 10, "")
	require.NoError(t, err)
	assert.Len(t, draftDefs, 1)
}

func TestRepository_Delete(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant09", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	require.NoError(t, repo.Delete(ctx, def.ID))

	_, err := repo.GetByID(ctx, def.ID)
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_Delete_Active_Fails(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant10", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))
	require.NoError(t, repo.UpdateStatus(ctx, def.ID, mapping.StatusActive))

	err := repo.Delete(ctx, def.ID)
	assert.ErrorIs(t, err, mapping.ErrNotActive)
}

func TestRepository_GetLatestActive(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant11", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))
	require.NoError(t, repo.UpdateStatus(ctx, def.ID, mapping.StatusActive))

	got, err := repo.GetLatestActive(ctx, "test-mapping")
	require.NoError(t, err)
	assert.Equal(t, def.ID, got.ID)
	assert.Equal(t, mapping.StatusActive, got.Status)
}

func TestRepository_GetByNameAndVersion(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant13", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	t.Run("returns existing definition", func(t *testing.T) {
		def := newTestDef()
		require.NoError(t, repo.Create(ctx, def))

		got, err := repo.GetByNameAndVersion(ctx, "test-mapping", 1)
		require.NoError(t, err)
		assert.Equal(t, def.ID, got.ID)
		assert.Equal(t, "test-mapping", got.Name)
		assert.Equal(t, 1, got.Version)
	})

	t.Run("returns ErrNotFound for non-existent name", func(t *testing.T) {
		_, err := repo.GetByNameAndVersion(ctx, "does-not-exist", 1)
		assert.ErrorIs(t, err, mapping.ErrNotFound)
	})

	t.Run("returns ErrNotFound for non-existent version", func(t *testing.T) {
		_, err := repo.GetByNameAndVersion(ctx, "test-mapping", 999)
		assert.ErrorIs(t, err, mapping.ErrNotFound)
	})
}

func TestRepository_OptimisticLock(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "testtenant12", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	// First update succeeds
	originalUpdatedAt := def.UpdatedAt
	def.Name = "first-update"
	require.NoError(t, repo.Update(ctx, def, originalUpdatedAt))

	// Second update with stale updated_at fails
	def.Name = "second-update"
	err := repo.Update(ctx, def, originalUpdatedAt) // still using old timestamp
	assert.ErrorIs(t, err, mapping.ErrOptimisticLock)
}
