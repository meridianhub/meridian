package accounttype_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markSystem flips is_system=true for a definition directly in the tenant schema,
// since CreateDraft always seeds non-system definitions.
func markSystem(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string, version int) {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	tag, err := tx.Exec(ctx,
		`UPDATE account_type_definitions SET is_system = true WHERE code = $1 AND version = $2`,
		code, version)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
	require.NoError(t, tx.Commit(ctx))
}

// NOTE on checkSagaExists coverage: that branch is intentionally not exercised here.
// platform_saga_definition is created only in the shared "public" schema by the
// reference-data migrations, but the registry queries it under a tenant-only
// search_path (SET LOCAL search_path TO "<tenant_schema>"), which excludes public.
// In this tenant-schema test harness the table is therefore unreachable from the
// registry's transaction, so any activation with a DefaultSagaPrefix fails with
// "relation does not exist" rather than ErrSagaNotFound. Covering checkSagaExists
// would require a production change (placing the table in the tenant schema or
// adding public to the search_path), which is out of scope for test-only work.

func TestPostgresAccountTypeRegistry_UpdateImmutableFields(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-immutable")

	t.Run("returns ErrFieldImmutable for Code change", func(t *testing.T) {
		def := newTestDefinition("IMM_CODE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &accounttype.Definition{Code: "DIFFERENT_CODE"}
		err := reg.UpdateDefinition(ctx, "IMM_CODE", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrFieldImmutable)
		assert.Contains(t, err.Error(), "Code")
	})

	t.Run("returns ErrFieldImmutable for IsSystem change", func(t *testing.T) {
		def := newTestDefinition("IMM_SYS", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &accounttype.Definition{IsSystem: true}
		err := reg.UpdateDefinition(ctx, "IMM_SYS", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrFieldImmutable)
		assert.Contains(t, err.Error(), "IsSystem")
	})

	t.Run("matching Code and BehaviorClass are allowed", func(t *testing.T) {
		def := newTestDefinition("IMM_SAME", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		// Supplying the same Code and BehaviorClass must not trip the immutability check.
		updates := &accounttype.Definition{
			Code:          "IMM_SAME",
			BehaviorClass: accounttype.BehaviorClassCustomer,
			DisplayName:   "Renamed",
		}
		require.NoError(t, reg.UpdateDefinition(ctx, "IMM_SAME", 1, updates))

		result, err := reg.GetDefinition(ctx, "IMM_SAME", 1)
		require.NoError(t, err)
		assert.Equal(t, "Renamed", result.DisplayName)
	})
}

func TestPostgresAccountTypeRegistry_SystemReadOnly(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-systemro")

	seedInstrument(t, pool, ctx, "GBP")

	t.Run("UpdateDefinition rejects system account type", func(t *testing.T) {
		def := newTestDefinition("SYS_UPDATE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		markSystem(t, pool, ctx, "SYS_UPDATE", 1)

		updates := &accounttype.Definition{DisplayName: "Nope"}
		err := reg.UpdateDefinition(ctx, "SYS_UPDATE", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
	})

	t.Run("DeprecateAccountType rejects system account type", func(t *testing.T) {
		def := newTestDefinition("SYS_DEPR", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "SYS_DEPR", 1))
		markSystem(t, pool, ctx, "SYS_DEPR", 1)

		err := reg.DeprecateAccountType(ctx, "SYS_DEPR", 1, nil)
		require.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
	})
}

func TestPostgresAccountTypeRegistry_DeprecateSuccessorWriteOnce(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-succwo")

	seedInstrument(t, pool, ctx, "GBP")

	old := newTestDefinition("WO2_OLD", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, old))
	require.NoError(t, reg.ActivateAccountType(ctx, "WO2_OLD", 1))

	succ := newTestDefinition("WO2_SUCC", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, succ))
	require.NoError(t, reg.ActivateAccountType(ctx, "WO2_SUCC", 1))

	// First deprecation sets the successor.
	require.NoError(t, reg.DeprecateAccountType(ctx, "WO2_OLD", 1, &succ.ID))

	// Re-issuing the same successor on an already-deprecated definition hits the
	// ErrNotActive guard before the write-once check (status is DEPRECATED).
	err := reg.DeprecateAccountType(ctx, "WO2_OLD", 1, &succ.ID)
	require.ErrorIs(t, err, accounttype.ErrNotActive)
}

func TestPostgresAccountTypeRegistry_ActivateNonDraftNonDeprecated(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-activatenf")

	// Activating a definition that does not exist returns ErrNotFound from the
	// in-transaction lookup.
	err := reg.ActivateAccountType(ctx, "NEVER_CREATED", 1)
	require.ErrorIs(t, err, accounttype.ErrNotFound)
}
