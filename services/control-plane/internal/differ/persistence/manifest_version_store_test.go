package persistence

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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

	assert.Equal(t, "1.0", result.Version)
	assert.Equal(t, "test-user", result.AppliedBy)
	assert.Equal(t, "1.0", result.Manifest.Version)
	assert.Equal(t, "Test Manifest", result.Manifest.Metadata.Name)
	assert.NotEmpty(t, result.ID)
	assert.False(t, result.AppliedAt.IsZero())
}

func TestSave_MultipleVersions(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Save first version
	err := store.Save(ctx, testManifest(), "user-a")
	require.NoError(t, err)

	// Save second version with a different version string
	m2 := testManifest()
	m2.Version = "2.0"
	m2.Metadata.Name = "Updated Manifest"
	err = store.Save(ctx, m2, "user-b")
	require.NoError(t, err)

	// GetLatestApplied should return version 2.0 (most recent by applied_at)
	result, err := store.GetLatestApplied(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "2.0", result.Version)
	assert.Equal(t, "user-b", result.AppliedBy)
	assert.Equal(t, "Updated Manifest", result.Manifest.Metadata.Name)
}

func TestMultiTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))

	// Set up two independent tenant schemas
	ctxA, cleanupA := testdb.SetupTenantSchemaForPgx(t, pool, "tenant-alpha", "control-plane")
	t.Cleanup(cleanupA)
	ctxB, cleanupB := testdb.SetupTenantSchemaForPgx(t, pool, "tenant-beta", "control-plane")
	t.Cleanup(cleanupB)

	store := NewPostgresManifestVersionStore(pool)

	// Save a manifest for tenant A
	manifestA := testManifest()
	manifestA.Metadata.Name = "Alpha Manifest"
	err := store.Save(ctxA, manifestA, "admin-a")
	require.NoError(t, err)

	// Save a manifest for tenant B
	manifestB := testManifest()
	manifestB.Metadata.Name = "Beta Manifest"
	err = store.Save(ctxB, manifestB, "admin-b")
	require.NoError(t, err)

	// Both tenants should see version "1.0" (independent stores)
	resultA, err := store.GetLatestApplied(ctxA)
	require.NoError(t, err)
	require.NotNil(t, resultA)
	assert.Equal(t, "1.0", resultA.Version)
	assert.Equal(t, "admin-a", resultA.AppliedBy)
	assert.Equal(t, "Alpha Manifest", resultA.Manifest.Metadata.Name)

	resultB, err := store.GetLatestApplied(ctxB)
	require.NoError(t, err)
	require.NotNil(t, resultB)
	assert.Equal(t, "1.0", resultB.Version)
	assert.Equal(t, "admin-b", resultB.AppliedBy)
	assert.Equal(t, "Beta Manifest", resultB.Manifest.Metadata.Name)
}

func TestGetLatestApplied_WithoutTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	store := NewPostgresManifestVersionStore(pool)

	// Use plain context without tenant - should return error
	ctx := context.Background()

	err := store.Save(ctx, testManifest(), "no-tenant-user")
	require.Error(t, err)
	assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)

	_, err = store.GetLatestApplied(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
}
