package valuation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/valuation"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMethodRepo(t *testing.T) (*valuation.PostgresMethodRepository, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.NewTestPool(t)
	repo := valuation.NewPostgresMethodRepository(pool)
	return repo, pool
}

func setupMethodTenantCtx(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func newTestMethod(name, input, output string) *valuation.Method {
	return &valuation.Method{
		Name:             name,
		Version:          1,
		InputInstrument:  input,
		OutputInstrument: output,
		LogicScript:      "def evaluate(amount, rate, context):\n    return amount\n",
		RequiredPolicies: []string{},
		Description:      "Test method: " + name,
	}
}

func seedSystemMethod(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name, input, output string) uuid.UUID {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO valuation_method (
			id, name, version, input_instrument, output_instrument,
			logic_script, logic_hash, required_policies, lifecycle_status,
			is_system, created_at, activated_at, valid_from
		) VALUES (
			$1, $2, 1, $3, $4,
			'def evaluate(a,r,c): return a', 'abc123', '{}', 'ACTIVE',
			true, NOW(), NOW(), NOW()
		)`, id, name, input, output)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return id
}

func TestMethodCreate(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx := setupMethodTenantCtx(t, pool, "method-create")

	t.Run("creates method successfully", func(t *testing.T) {
		m := newTestMethod("TEST_CREATE", "USD", "GBP")
		err := repo.Create(ctx, m)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, m.ID)
		assert.Equal(t, valuation.StatusInitiated, m.LifecycleStatus)
		assert.NotEmpty(t, m.LogicHash)
	})

	t.Run("rejects system method creation", func(t *testing.T) {
		m := newTestMethod("SYS_CREATE", "USD", "GBP")
		m.IsSystem = true
		err := repo.Create(ctx, m)
		require.ErrorIs(t, err, valuation.ErrSystemReadOnly)
	})

	t.Run("rejects duplicate name+version", func(t *testing.T) {
		m1 := newTestMethod("DUPE_METHOD", "USD", "GBP")
		require.NoError(t, repo.Create(ctx, m1))

		m2 := newTestMethod("DUPE_METHOD", "USD", "GBP")
		err := repo.Create(ctx, m2)
		require.ErrorIs(t, err, valuation.ErrAlreadyExists)
	})
}

func TestMethodGetByID(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx := setupMethodTenantCtx(t, pool, "method-getbyid")

	m := newTestMethod("GET_BY_ID", "KWH", "GBP")
	require.NoError(t, repo.Create(ctx, m))

	t.Run("retrieves existing method", func(t *testing.T) {
		result, err := repo.GetByID(ctx, m.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, "GET_BY_ID", result.Name)
		assert.Equal(t, "KWH", result.InputInstrument)
		assert.Equal(t, "GBP", result.OutputInstrument)
		assert.Equal(t, valuation.StatusInitiated, result.LifecycleStatus)
	})

	t.Run("returns ErrNotFound for missing method", func(t *testing.T) {
		_, err := repo.GetByID(ctx, uuid.New(), nil)
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})

	t.Run("bi-temporal query at knowledge time", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		_, err := repo.GetByID(ctx, m.ID, &past)
		// Method was just created, so querying before creation should not find it
		require.ErrorIs(t, err, valuation.ErrNotFound)

		future := time.Now().Add(1 * time.Hour)
		result, err := repo.GetByID(ctx, m.ID, &future)
		require.NoError(t, err)
		assert.Equal(t, "GET_BY_ID", result.Name)
	})
}

func TestMethodResolve(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx := setupMethodTenantCtx(t, pool, "method-resolve")

	t.Run("resolves tenant method over system", func(t *testing.T) {
		seedSystemMethod(t, pool, ctx, "SYS_RESOLVE", "USD", "GBP")

		m := newTestMethod("TENANT_RESOLVE", "USD", "GBP")
		require.NoError(t, repo.Create(ctx, m))
		require.NoError(t, repo.Activate(ctx, m.ID))

		result, err := repo.Resolve(ctx, "USD", "GBP")
		require.NoError(t, err)
		assert.False(t, result.IsSystem)
		assert.Equal(t, "TENANT_RESOLVE", result.Name)
	})

	t.Run("falls back to system method", func(t *testing.T) {
		seedSystemMethod(t, pool, ctx, "SYS_FALLBACK", "EUR", "USD")

		result, err := repo.Resolve(ctx, "EUR", "USD")
		require.NoError(t, err)
		assert.True(t, result.IsSystem)
		assert.Equal(t, "SYS_FALLBACK", result.Name)
	})

	t.Run("returns ErrNotFound when no match", func(t *testing.T) {
		_, err := repo.Resolve(ctx, "MISSING", "NOWHERE")
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})
}

func TestMethodLifecycle(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx := setupMethodTenantCtx(t, pool, "method-lifecycle")

	t.Run("INITIATED to ACTIVE succeeds", func(t *testing.T) {
		m := newTestMethod("LIFECYCLE_ACTIVATE", "USD", "EUR")
		require.NoError(t, repo.Create(ctx, m))

		err := repo.Activate(ctx, m.ID)
		require.NoError(t, err)

		result, err := repo.GetByID(ctx, m.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, valuation.StatusActive, result.LifecycleStatus)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("ACTIVE to DEPRECATED succeeds", func(t *testing.T) {
		m := newTestMethod("LIFECYCLE_DEPRECATE", "GBP", "EUR")
		require.NoError(t, repo.Create(ctx, m))
		require.NoError(t, repo.Activate(ctx, m.ID))

		err := repo.Deprecate(ctx, m.ID)
		require.NoError(t, err)

		result, err := repo.GetByID(ctx, m.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, valuation.StatusDeprecated, result.LifecycleStatus)
		assert.NotNil(t, result.DeprecatedAt)
		assert.NotNil(t, result.ValidTo)
	})

	t.Run("cannot activate already active method", func(t *testing.T) {
		m := newTestMethod("LIFECYCLE_REACTIVATE", "KWH", "GBP")
		require.NoError(t, repo.Create(ctx, m))
		require.NoError(t, repo.Activate(ctx, m.ID))

		err := repo.Activate(ctx, m.ID)
		require.ErrorIs(t, err, valuation.ErrNotInitiated)
	})

	t.Run("cannot deprecate initiated method", func(t *testing.T) {
		m := newTestMethod("LIFECYCLE_SKIP", "KWH", "EUR")
		require.NoError(t, repo.Create(ctx, m))

		err := repo.Deprecate(ctx, m.ID)
		require.ErrorIs(t, err, valuation.ErrNotActive)
	})

	t.Run("cannot activate system method", func(t *testing.T) {
		id := seedSystemMethod(t, pool, ctx, "SYS_ACTIVATE", "TONNE_CO2E", "GBP")
		err := repo.Activate(ctx, id)
		require.ErrorIs(t, err, valuation.ErrSystemReadOnly)
	})
}

func TestMethodTenantIsolation(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx1 := setupMethodTenantCtx(t, pool, "method-iso-1")
	ctx2 := setupMethodTenantCtx(t, pool, "method-iso-2")

	m := newTestMethod("ISOLATED_METHOD", "USD", "GBP")
	require.NoError(t, repo.Create(ctx1, m))

	t.Run("tenant 1 can see its method", func(t *testing.T) {
		result, err := repo.GetByID(ctx1, m.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, "ISOLATED_METHOD", result.Name)
	})

	t.Run("tenant 2 cannot see tenant 1 method", func(t *testing.T) {
		_, err := repo.GetByID(ctx2, m.ID, nil)
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})
}

func TestMethodActivateRequiredPolicies(t *testing.T) {
	repo, pool := setupMethodRepo(t)
	ctx := setupMethodTenantCtx(t, pool, "method-req-policies")

	// Create and activate a policy first
	policyRepo := setupPolicyRepo(t, pool)
	p := newTestPolicy("REQUIRED_POLICY")
	require.NoError(t, policyRepo.Create(ctx, p))
	require.NoError(t, policyRepo.Activate(ctx, p.ID))

	t.Run("activate succeeds when required policies are active", func(t *testing.T) {
		m := newTestMethod("WITH_POLICY", "USD", "GBP")
		m.RequiredPolicies = []string{"REQUIRED_POLICY"}
		require.NoError(t, repo.Create(ctx, m))

		err := repo.Activate(ctx, m.ID)
		require.NoError(t, err)
	})

	t.Run("activate fails when required policy missing", func(t *testing.T) {
		m := newTestMethod("MISSING_POLICY", "USD", "EUR")
		m.RequiredPolicies = []string{"NONEXISTENT_POLICY"}
		require.NoError(t, repo.Create(ctx, m))

		err := repo.Activate(ctx, m.ID)
		require.ErrorIs(t, err, valuation.ErrRequiredPolicyMissing)
	})
}
