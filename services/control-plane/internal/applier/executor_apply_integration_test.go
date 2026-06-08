package applier

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertPlatformSaga inserts an ACTIVE apply_manifest row into
// public.platform_saga_definition so resolveSagaScript can find a script.
func insertPlatformSaga(t *testing.T, pool *pgxpool.Pool, version, script string) {
	t.Helper()
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO public.platform_saga_definition (id, name, version, script, status)
		 VALUES (gen_random_uuid(), 'apply_manifest', $1, $2, 'ACTIVE')`,
		version, script,
	)
	require.NoError(t, err)
}

// newExecutorWithRunner builds a ManifestExecutor wired to a real saga runner
// using the provided handler dependencies and a DB-backed pool with the
// control-plane migrations applied. It exercises NewManifestExecutorFromDeps.
func newExecutorWithRunner(t *testing.T, pool *pgxpool.Pool, deps *HandlerDependencies) *ManifestExecutor {
	t.Helper()
	executor, err := NewManifestExecutorFromDeps(ManifestExecutorDepsConfig{
		Pool: pool,
		Deps: deps,
	})
	require.NoError(t, err)
	require.NotNil(t, executor)
	return executor
}

// TestManifestExecutor_Apply_Success drives the full Apply path against a real
// database: prepareApplyJob -> resolveSagaScript -> pinSagaDefinition ->
// executeSagaAndFinalize, ending with the job marked APPLIED.
func TestManifestExecutor_Apply_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	// Use the embedded saga script as the platform default so the runner can
	// execute the registered handlers end to end.
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	insertPlatformSaga(t, pool, version, script)

	deps := &HandlerDependencies{
		ReferenceData:      &noopReferenceData{},
		InternalAccount:    &noopInternalAccount{},
		MarketInformation:  &noopMarketInformation{},
		Party:              &noopParty{},
		OperationalGateway: &noopOperationalGateway{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	input := &ApplyManifestInput{
		ManifestVersion: "7",
		TenantID:        "org_success",
		Instruments: []InstrumentInput{
			{Code: "GBP", DisplayName: "British Pound", Dimension: "CURRENCY", DecimalPlaces: 2},
		},
	}

	result, err := executor.Apply(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "applied", result.Status)
	assert.Equal(t, "7", result.Version)
	assert.NotEqual(t, result.JobID, result.SagaExecutionID)
	assert.Empty(t, result.Error)

	// Job tracking row must be marked APPLIED.
	jobRepo := NewApplyJobRepository(pool)
	job, err := jobRepo.GetByID(context.Background(), result.JobID)
	require.NoError(t, err)
	assert.Equal(t, ApplyJobStatusApplied, job.Status)
	require.NotNil(t, job.SagaExecutionID)
	assert.Equal(t, result.SagaExecutionID, *job.SagaExecutionID)

	// The resolved script must have been pinned into saga_definitions.
	pinned, err := executor.sagaDefRepo.FindOrCreate(context.Background(), "apply_manifest", version, script, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, pinned.ID)
}

// TestManifestExecutor_Apply_SagaNotFound covers the resolveSagaScript
// ErrSagaNotFound branch: no platform saga row exists, so prepareApplyJob fails
// and the job is marked FAILED.
func TestManifestExecutor_Apply_SagaNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	deps := &HandlerDependencies{
		ReferenceData:   &noopReferenceData{},
		InternalAccount: &noopInternalAccount{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	input := &ApplyManifestInput{
		ManifestVersion: "3",
		TenantID:        "org_missing",
	}

	result, err := executor.Apply(context.Background(), input)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrSagaNotFound)

	// The tracking job should have been created then marked FAILED.
	jobRepo := NewApplyJobRepository(pool)
	job, err := jobRepo.GetByManifestVersion(context.Background(), 3)
	require.NoError(t, err)
	assert.Equal(t, ApplyJobStatusFailed, job.Status)
	assert.Contains(t, job.Error, "apply_manifest saga definition not found")
}

// TestManifestExecutor_Apply_SagaFails covers the executeSagaAndFinalize
// failure branch: a handler returns an error, the saga reports !Success, and
// the result carries status "failed" wrapping ErrSagaFailed.
func TestManifestExecutor_Apply_SagaFails(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	insertPlatformSaga(t, pool, version, script)

	// A reference-data handler that fails on register_instrument forces the
	// saga to fail during Phase 10.
	failingRefData := &mockReferenceData{
		registerInstrumentFn: func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
			return nil, errBoom
		},
	}
	deps := &HandlerDependencies{
		ReferenceData:   failingRefData,
		InternalAccount: &noopInternalAccount{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	input := &ApplyManifestInput{
		ManifestVersion: "9",
		TenantID:        "org_fail",
		Instruments: []InstrumentInput{
			{Code: "GBP", DisplayName: "British Pound", Dimension: "CURRENCY", DecimalPlaces: 2},
		},
	}

	result, err := executor.Apply(context.Background(), input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSagaFailed)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.NotEmpty(t, result.Error)

	// Job must be marked FAILED with the saga error captured.
	jobRepo := NewApplyJobRepository(pool)
	job, err := jobRepo.GetByID(context.Background(), result.JobID)
	require.NoError(t, err)
	assert.Equal(t, ApplyJobStatusFailed, job.Status)
	assert.NotEmpty(t, job.Error)
}

// TestNewManifestExecutorFromDeps_BuildsRunner verifies the happy path of the
// wiring factory: a runnable executor is returned with a non-nil runner and
// repositories.
func TestNewManifestExecutorFromDeps_BuildsRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	deps := &HandlerDependencies{
		ReferenceData:   &noopReferenceData{},
		InternalAccount: &noopInternalAccount{},
	}
	executor, err := NewManifestExecutorFromDeps(ManifestExecutorDepsConfig{
		Pool: pool,
		Deps: deps,
	})
	require.NoError(t, err)
	require.NotNil(t, executor)
	assert.NotNil(t, executor.runner)
	assert.NotNil(t, executor.jobRepo)
	assert.NotNil(t, executor.sagaDefRepo)
	assert.NotNil(t, executor.pool)

	// Sanity: building the service modules from the same registry should succeed,
	// proving the deps registered cleanly.
	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterManifestHandlers(registry, deps))
	_, err = schema.BuildServiceModules(registry)
	require.NoError(t, err)
}
