package mapping_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/mapping"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

func TestRepository_UpdateStatus_InvalidTransition(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnupdstatusinv", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	// DRAFT -> DEPRECATED is not a permitted transition (only DRAFT -> ACTIVE).
	err := repo.UpdateStatus(ctx, def.ID, mapping.StatusDeprecated)
	assert.ErrorIs(t, err, mapping.ErrInvalidStatusTransition)
}

func TestRepository_UpdateStatus_NotFound(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnupdstatusnf", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	err := repo.UpdateStatus(ctx, uuid.New(), mapping.StatusActive)
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_Update_NotFound(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnupdnf", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	def.ID = uuid.New() // never persisted
	err := repo.Update(ctx, def, def.UpdatedAt)
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_Delete_NotFound(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tndelnf", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	err := repo.Delete(ctx, uuid.New())
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_GetLatestActive_NotFound(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnlatestnf", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	// A DRAFT exists but none ACTIVE, so GetLatestActive should return ErrNotFound.
	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	_, err := repo.GetLatestActive(ctx, "test-mapping")
	assert.ErrorIs(t, err, mapping.ErrNotFound)
}

func TestRepository_ListByTenant_TargetServiceFilter(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnlisttgtsvc", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	payment := &mapping.Definition{Name: "pay-map", TargetService: "payment.Service", TargetRPC: "Rpc", Version: 1}
	require.NoError(t, repo.Create(ctx, payment))

	ledger := &mapping.Definition{Name: "led-map", TargetService: "ledger.Service", TargetRPC: "Rpc", Version: 1}
	require.NoError(t, repo.Create(ctx, ledger))

	defs, total, err := repo.ListByTenant(ctx, "", "payment.Service", 10, "")
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, defs, 1)
	assert.Equal(t, "payment.Service", defs[0].TargetService)
}

func TestRepository_ListByTenant_Pagination(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnlistpage", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	for i := 1; i <= 3; i++ {
		d := &mapping.Definition{
			Name:          "page-" + string(rune('A'-1+i)),
			TargetService: "svc",
			TargetRPC:     "Rpc",
			Version:       1,
		}
		require.NoError(t, repo.Create(ctx, d))
	}

	// First page of 2 (ordered by id).
	firstPage, total, err := repo.ListByTenant(ctx, "", "", 2, "")
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, firstPage, 2)

	// Use the last id of the first page as the cursor for the next page.
	lastID := firstPage[len(firstPage)-1].ID.String()
	secondPage, _, err := repo.ListByTenant(ctx, "", "", 2, lastID)
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	assert.Greater(t, secondPage[0].ID.String(), lastID)
}

func TestRepository_ListByTenant_DefaultPageSize(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnlistdefsize", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := newTestDef()
	require.NoError(t, repo.Create(ctx, def))

	// pageSize <= 0 should fall back to the default page size.
	defs, total, err := repo.ListByTenant(ctx, "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, defs, 1)
}

// TestRepository_RoundTrip_AllOptionalFields exercises populateFromScan with every
// optional column populated (external_schema, CEL expressions, batch path, computed
// fields, idempotency) so the conditional branches that copy NULL-able values are covered.
func TestRepository_RoundTrip_AllOptionalFields(t *testing.T) {
	pool := testdb.NewTestPool(t, testdb.WithMigrations("reference-data"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "tnroundtrip", "reference-data")
	defer cleanup()

	repo := mapping.NewPostgresRepository(pool)

	def := &mapping.Definition{
		Name:           "full-mapping",
		TargetService:  "svc",
		TargetRPC:      "Rpc",
		Version:        1,
		ExternalSchema: `{"type":"object"}`,
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
		InboundComputed: []mapping.ComputedField{
			{TargetPath: "derived_in", CELExpression: "1 + 1"},
		},
		OutboundComputed: []mapping.ComputedField{
			{TargetPath: "derived_out", CELExpression: "2 + 2"},
		},
		InboundValidationCEL:  "amount > 0",
		OutboundValidationCEL: "amount < 100",
		IsBatch:               true,
		BatchTargetPath:       "items",
		Idempotency: &mapping.IdempotencyConfig{
			SourceSelector:    "id",
			UseContentHash:    true,
			ContentHashFields: []string{"amount"},
		},
	}
	require.NoError(t, repo.Create(ctx, def))

	got, err := repo.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, `{"type":"object"}`, got.ExternalSchema)
	assert.Equal(t, "amount > 0", got.InboundValidationCEL)
	assert.Equal(t, "amount < 100", got.OutboundValidationCEL)
	assert.True(t, got.IsBatch)
	assert.Equal(t, "items", got.BatchTargetPath)
	require.Len(t, got.InboundComputed, 1)
	assert.Equal(t, "derived_in", got.InboundComputed[0].TargetPath)
	require.Len(t, got.OutboundComputed, 1)
	assert.Equal(t, "derived_out", got.OutboundComputed[0].TargetPath)
	require.NotNil(t, got.Idempotency)
	assert.Equal(t, "id", got.Idempotency.SourceSelector)
	assert.True(t, got.Idempotency.UseContentHash)
	assert.Equal(t, []string{"amount"}, got.Idempotency.ContentHashFields)

	// Round-trip through ListByTenant too, exercising scanRows + populateFromScan.
	defs, _, err := repo.ListByTenant(ctx, "", "", 10, "")
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "full-mapping", defs[0].Name)
	require.NotNil(t, defs[0].Idempotency)
	assert.Equal(t, "id", defs[0].Idempotency.SourceSelector)
}
