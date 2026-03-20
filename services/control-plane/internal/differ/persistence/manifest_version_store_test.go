package persistence

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func setupTestStore(t *testing.T) (*PostgresManifestVersionStore, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, "test-tenant", "control-plane")
	t.Cleanup(cleanup)

	store := NewPostgresManifestVersionStore(pool)
	return store, ctx
}

func testManifest() *controlplanev1.Manifest {
	seedData, _ := structpb.NewStruct(map[string]interface{}{})
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Manifest",
			Industry:    "testing",
			Description: "A test manifest",
		},
		SeedData: seedData,
	}
}

func TestNewPostgresManifestVersionStore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := testdb.NewTestPool(t)
	store := NewPostgresManifestVersionStore(pool)
	assert.NotNil(t, store)
}

func TestGetLatestApplied_Empty(t *testing.T) {
	store, ctx := setupTestStore(t)

	result, err := store.GetLatestApplied(ctx)
	require.NoError(t, err)
	assert.Nil(t, result, "expected nil when no manifests have been applied")
}

func TestSave_And_GetLatestApplied(t *testing.T) {
	store, ctx := setupTestStore(t)

	manifest := testManifest()
	err := store.Save(ctx, manifest, "test-user")
	require.NoError(t, err)

	result, err := store.GetLatestApplied(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, result.Version)
	assert.Equal(t, "test-user", result.AppliedBy)
	assert.Equal(t, "1.0", result.Manifest.Version)
	assert.Equal(t, "Test Manifest", result.Manifest.Metadata.Name)
	assert.NotEmpty(t, result.ID)
	assert.False(t, result.AppliedAt.IsZero())
}

func TestSave_IncrementsVersion(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Save first version
	err := store.Save(ctx, testManifest(), "user-a")
	require.NoError(t, err)

	// Save second version
	m2 := testManifest()
	m2.Metadata.Name = "Updated Manifest"
	err = store.Save(ctx, m2, "user-b")
	require.NoError(t, err)

	// GetLatestApplied should return version 2
	result, err := store.GetLatestApplied(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 2, result.Version)
	assert.Equal(t, "user-b", result.AppliedBy)
	assert.Equal(t, "Updated Manifest", result.Manifest.Metadata.Name)
}

func TestGetLatestApplied_WithoutTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	store := NewPostgresManifestVersionStore(pool)

	// Use plain context without tenant - should still work (no search_path set)
	ctx := context.Background()

	// Save and retrieve without tenant context
	err := store.Save(ctx, testManifest(), "no-tenant-user")
	require.NoError(t, err)

	result, err := store.GetLatestApplied(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Version)
}
