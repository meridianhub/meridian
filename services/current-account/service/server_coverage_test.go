package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// sagaAssetRepoRoot returns the repository root so SAGA_ASSET_DIR can resolve
// the canonical deposit/withdrawal saga scripts under
// services/reference-data/saga/defaults/. Mirrors the runtime.Caller pattern
// used by the orchestrator test helpers.
func sagaAssetRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to resolve current file path")
	serviceDir := filepath.Dir(filename)
	return filepath.Join(serviceDir, "..", "..", "..")
}

// =============================================================================
// NewService constructor
// =============================================================================

func TestNewService_NilRepo(t *testing.T) {
	_, err := NewService(nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

func TestNewService_ValidRepo(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc, err := NewService(repo, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// =============================================================================
// NewServiceWithIdempotency constructor
// =============================================================================

func TestNewServiceWithIdempotency_NilRepo(t *testing.T) {
	_, err := NewServiceWithIdempotency(nil, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

func TestNewServiceWithIdempotency_ValidRepo(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	mockIdemp := &mockIdempotencyService{}
	svc, err := NewServiceWithIdempotency(repo, nil, mockIdemp)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// =============================================================================
// NewServiceWithValuationFeatures constructor
// =============================================================================

func TestNewServiceWithValuationFeatures_ValidRepo(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)
	svc, err := NewServiceWithValuationFeatures(repo, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// =============================================================================
// loadSagaAsset - fallback to executable directory
// =============================================================================

func TestLoadSagaAsset_FallbackToExecutable(t *testing.T) {
	// Clear the env var to force executable fallback
	t.Setenv("SAGA_ASSET_DIR", "")

	// The file won't exist relative to the test executable, so we expect an error
	_, err := loadSagaAsset("nonexistent/path/script.star")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read saga asset")
}

func TestLoadSagaAsset_EnvVarSet_FileExists(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test.star")
	require.NoError(t, os.WriteFile(scriptPath, []byte("print('hello')"), 0o644))

	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	content, err := loadSagaAsset("test.star")
	require.NoError(t, err)
	assert.Equal(t, "print('hello')", content)
}

func TestLoadSagaAsset_EnvVarSet_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAGA_ASSET_DIR", tmpDir)

	_, err := loadSagaAsset("missing.star")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read saga asset")
}

// =============================================================================
// NewServiceWithExistingClients - success path
//
// Exercises the full constructor wiring: buildSagaRunner (loads the canonical
// deposit/withdrawal saga scripts, registers handlers, builds service modules,
// creates the runtime + runner) and buildOrchestrators (deposit + withdrawal).
// SAGA_ASSET_DIR points at the repo root so loadSagaAsset resolves the real
// scripts under services/reference-data/saga/defaults/.
// =============================================================================

func TestNewServiceWithExistingClients_Success(t *testing.T) {
	t.Setenv("SAGA_ASSET_DIR", sagaAssetRepoRoot(t))

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)

	svc, err := NewServiceWithExistingClients(
		repo,
		nil, // lienRepo
		nil, // withdrawalRepo
		nil, // outboxRepo
		db,
		&mockPositionKeepingClient{},
		&mockFinancialAccountingClient{},
		nil, // partyClient
		nil, // accountConfig
		newMockIdempotencyService(),
		testLogger(),
		nil, // tracer
		nil, // accountResolver
		nil, // fungibilityValidator
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Constructor wired both orchestrators and the outbox publisher.
	assert.NotNil(t, svc.depositOrchestrator)
	assert.NotNil(t, svc.withdrawalOrchestrator)
	assert.NotNil(t, svc.outboxPublisher)
	assert.Same(t, repo, svc.repo)
}

// TestNewServiceWithExistingClients_NilLoggerDefaults verifies the constructor
// substitutes a default JSON logger when none is provided.
func TestNewServiceWithExistingClients_NilLoggerDefaults(t *testing.T) {
	t.Setenv("SAGA_ASSET_DIR", sagaAssetRepoRoot(t))

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)

	svc, err := NewServiceWithExistingClients(
		repo,
		nil, nil, nil,
		db,
		&mockPositionKeepingClient{},
		&mockFinancialAccountingClient{},
		nil, nil,
		newMockIdempotencyService(),
		nil, // logger -> constructor defaults it
		nil, nil, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
}

// TestNewServiceWithExistingClients_WithNotificationSagaHandler verifies that the
// WithNotificationSagaHandler option is consumed during saga runner construction
// (the constructor pre-scans options to wire a real notification.send handler).
func TestNewServiceWithExistingClients_WithNotificationSagaHandler(t *testing.T) {
	t.Setenv("SAGA_ASSET_DIR", sagaAssetRepoRoot(t))

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)

	notifyHandler := saga.Handler(func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"status": "sent"}, nil
	})

	svc, err := NewServiceWithExistingClients(
		repo,
		nil, nil, nil,
		db,
		&mockPositionKeepingClient{},
		&mockFinancialAccountingClient{},
		nil, nil,
		newMockIdempotencyService(),
		testLogger(),
		nil, nil, nil,
		WithNotificationSagaHandler(notifyHandler),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	// The option also lands on the service struct via the post-construction apply loop.
	assert.NotNil(t, svc.notificationHandler)
}

// TestNewServiceWithExistingClients_MissingSagaAsset verifies the constructor
// surfaces buildSagaRunner errors when the saga scripts cannot be resolved.
func TestNewServiceWithExistingClients_MissingSagaAsset(t *testing.T) {
	t.Setenv("SAGA_ASSET_DIR", t.TempDir()) // empty dir -> deposit script not found

	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := newTestRepo(db)

	_, err := NewServiceWithExistingClients(
		repo,
		nil, nil, nil,
		db,
		&mockPositionKeepingClient{},
		&mockFinancialAccountingClient{},
		nil, nil,
		newMockIdempotencyService(),
		testLogger(),
		nil, nil, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load deposit saga script")
}
