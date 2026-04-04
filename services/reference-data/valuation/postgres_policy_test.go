package valuation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/valuation"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPolicyRepo(t *testing.T, pool *pgxpool.Pool) *valuation.PostgresPolicyRepository {
	t.Helper()
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)
	return valuation.NewPostgresPolicyRepository(pool, compiler)
}

func setupPolicyTenantCtx(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func newTestPolicy(name string) *valuation.Policy {
	return &valuation.Policy{
		Name:          name,
		Version:       1,
		CelExpression: "amount",
		OutputType:    "string",
		EstimatedCost: 1,
		Description:   "Test policy: " + name,
	}
}

func seedSystemPolicy(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name, celExpr string) uuid.UUID {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO valuation_policy (
			id, name, version, cel_expression, cel_hash,
			estimated_cost, lifecycle_status,
			is_system, created_at, activated_at, valid_from
		) VALUES (
			$1, $2, 1, $3, 'hash123',
			1, 'ACTIVE',
			true, NOW(), NOW(), NOW()
		)`, id, name, celExpr)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return id
}

func TestPolicyCreate(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx := setupPolicyTenantCtx(t, pool, "policy-create")

	t.Run("creates policy successfully", func(t *testing.T) {
		p := newTestPolicy("TEST_CREATE")
		err := repo.Create(ctx, p)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, p.ID)
		assert.Equal(t, valuation.StatusInitiated, p.LifecycleStatus)
		assert.NotEmpty(t, p.CelHash)
	})

	t.Run("rejects system policy creation", func(t *testing.T) {
		p := newTestPolicy("SYS_CREATE")
		p.IsSystem = true
		err := repo.Create(ctx, p)
		require.ErrorIs(t, err, valuation.ErrSystemReadOnly)
	})

	t.Run("rejects invalid CEL expression", func(t *testing.T) {
		p := newTestPolicy("BAD_CEL")
		p.CelExpression = "{{{{invalid"
		err := repo.Create(ctx, p)
		require.ErrorIs(t, err, valuation.ErrInvalidCEL)
	})

	t.Run("rejects duplicate name+version", func(t *testing.T) {
		p1 := newTestPolicy("DUPE_POLICY")
		require.NoError(t, repo.Create(ctx, p1))

		p2 := newTestPolicy("DUPE_POLICY")
		err := repo.Create(ctx, p2)
		require.ErrorIs(t, err, valuation.ErrAlreadyExists)
	})

	t.Run("estimates cost automatically", func(t *testing.T) {
		p := newTestPolicy("AUTO_COST")
		p.EstimatedCost = 0 // Will be auto-estimated
		p.CelExpression = "parse_int(amount) > 0 && parse_int(amount) < 1000000"
		err := repo.Create(ctx, p)
		require.NoError(t, err)
		assert.Greater(t, p.EstimatedCost, 0)
	})
}

func TestPolicyGetByName(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx := setupPolicyTenantCtx(t, pool, "policy-getbyname")

	p := newTestPolicy("GET_BY_NAME")
	require.NoError(t, repo.Create(ctx, p))

	t.Run("retrieves existing policy", func(t *testing.T) {
		result, err := repo.GetByName(ctx, "GET_BY_NAME", nil)
		require.NoError(t, err)
		assert.Equal(t, "GET_BY_NAME", result.Name)
		assert.Equal(t, "amount", result.CelExpression)
		assert.Equal(t, valuation.StatusInitiated, result.LifecycleStatus)
	})

	t.Run("returns ErrNotFound for missing policy", func(t *testing.T) {
		_, err := repo.GetByName(ctx, "NONEXISTENT", nil)
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})

	t.Run("bi-temporal query at knowledge time", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		_, err := repo.GetByName(ctx, "GET_BY_NAME", &past)
		require.ErrorIs(t, err, valuation.ErrNotFound)

		future := time.Now().Add(1 * time.Hour)
		result, err := repo.GetByName(ctx, "GET_BY_NAME", &future)
		require.NoError(t, err)
		assert.Equal(t, "GET_BY_NAME", result.Name)
	})
}

func TestPolicyResolve(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx := setupPolicyTenantCtx(t, pool, "policy-resolve")

	t.Run("resolves tenant policy over system", func(t *testing.T) {
		seedSystemPolicy(t, pool, ctx, "SYS_RESOLVE", "amount")

		p := newTestPolicy("SYS_RESOLVE")
		p.Version = 2 // Different version to avoid conflict with system
		require.NoError(t, repo.Create(ctx, p))
		require.NoError(t, repo.Activate(ctx, p.ID))

		result, err := repo.Resolve(ctx, "SYS_RESOLVE")
		require.NoError(t, err)
		assert.False(t, result.IsSystem)
	})

	t.Run("falls back to system policy", func(t *testing.T) {
		seedSystemPolicy(t, pool, ctx, "SYS_FALLBACK_P", "amount")

		result, err := repo.Resolve(ctx, "SYS_FALLBACK_P")
		require.NoError(t, err)
		assert.True(t, result.IsSystem)
	})

	t.Run("returns ErrNotFound when no match", func(t *testing.T) {
		_, err := repo.Resolve(ctx, "MISSING_POLICY")
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})
}

func TestPolicyLifecycle(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx := setupPolicyTenantCtx(t, pool, "policy-lifecycle")

	t.Run("INITIATED to ACTIVE succeeds", func(t *testing.T) {
		p := newTestPolicy("LIFECYCLE_ACTIVATE")
		require.NoError(t, repo.Create(ctx, p))

		err := repo.Activate(ctx, p.ID)
		require.NoError(t, err)

		result, err := repo.GetByName(ctx, "LIFECYCLE_ACTIVATE", nil)
		require.NoError(t, err)
		assert.Equal(t, valuation.StatusActive, result.LifecycleStatus)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("ACTIVE to DEPRECATED succeeds", func(t *testing.T) {
		p := newTestPolicy("LIFECYCLE_DEPRECATE")
		require.NoError(t, repo.Create(ctx, p))
		require.NoError(t, repo.Activate(ctx, p.ID))

		err := repo.Deprecate(ctx, p.ID)
		require.NoError(t, err)

		result, err := repo.GetByName(ctx, "LIFECYCLE_DEPRECATE", nil)
		require.NoError(t, err)
		assert.Equal(t, valuation.StatusDeprecated, result.LifecycleStatus)
		assert.NotNil(t, result.DeprecatedAt)
		assert.NotNil(t, result.ValidTo)
	})

	t.Run("cannot activate already active policy", func(t *testing.T) {
		p := newTestPolicy("LIFECYCLE_REACTIVATE")
		require.NoError(t, repo.Create(ctx, p))
		require.NoError(t, repo.Activate(ctx, p.ID))

		err := repo.Activate(ctx, p.ID)
		require.ErrorIs(t, err, valuation.ErrNotInitiated)
	})

	t.Run("cannot deprecate initiated policy", func(t *testing.T) {
		p := newTestPolicy("LIFECYCLE_SKIP")
		require.NoError(t, repo.Create(ctx, p))

		err := repo.Deprecate(ctx, p.ID)
		require.ErrorIs(t, err, valuation.ErrNotActive)
	})

	t.Run("cannot activate system policy", func(t *testing.T) {
		id := seedSystemPolicy(t, pool, ctx, "SYS_ACTIVATE_P", "amount")
		err := repo.Activate(ctx, id)
		require.ErrorIs(t, err, valuation.ErrSystemReadOnly)
	})
}

func TestPolicyDryRun(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx := setupPolicyTenantCtx(t, pool, "policy-dryrun")

	t.Run("dry run returns success with valid expression", func(t *testing.T) {
		p := newTestPolicy("DRYRUN_VALID")
		p.CelExpression = "parse_int(amount) > 0"
		require.NoError(t, repo.Create(ctx, p))
		require.NoError(t, repo.Activate(ctx, p.ID))

		result, err := repo.DryRun(ctx, "DRYRUN_VALID", map[string]string{"amount": "100"})
		require.NoError(t, err)
		assert.True(t, result.Success)
		assert.Equal(t, 1, result.EstimatedCost)
	})

	t.Run("dry run returns not found for missing policy", func(t *testing.T) {
		_, err := repo.DryRun(ctx, "NONEXISTENT_DRYRUN", nil)
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})
}

func TestPolicyTenantIsolation(t *testing.T) {
	pool := testdb.NewTestPool(t)
	repo := setupPolicyRepo(t, pool)
	ctx1 := setupPolicyTenantCtx(t, pool, "policy-iso-1")
	ctx2 := setupPolicyTenantCtx(t, pool, "policy-iso-2")

	p := newTestPolicy("ISOLATED_POLICY")
	require.NoError(t, repo.Create(ctx1, p))

	t.Run("tenant 1 can see its policy", func(t *testing.T) {
		result, err := repo.GetByName(ctx1, "ISOLATED_POLICY", nil)
		require.NoError(t, err)
		assert.Equal(t, "ISOLATED_POLICY", result.Name)
	})

	t.Run("tenant 2 cannot see tenant 1 policy", func(t *testing.T) {
		_, err := repo.GetByName(ctx2, "ISOLATED_POLICY", nil)
		require.ErrorIs(t, err, valuation.ErrNotFound)
	})
}
