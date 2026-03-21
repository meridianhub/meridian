package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
