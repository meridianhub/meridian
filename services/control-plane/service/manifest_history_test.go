package service

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// ManifestHistoryServiceConfig field tests
// ---------------------------------------------------------------------------

func TestManifestHistoryServiceConfig_Defaults(t *testing.T) {
	cfg := ManifestHistoryServiceConfig{}
	assert.Nil(t, cfg.DB)
	assert.Nil(t, cfg.Logger)
	assert.Nil(t, cfg.ExportCollectors)
	assert.Nil(t, cfg.Applier)
}

func TestErrDBRequired_Message(t *testing.T) {
	assert.EqualError(t, ErrDBRequired, "manifest history service: database connection is required")
}

// ---------------------------------------------------------------------------
// RegisterManifestHistoryService - configuration combinations
// ---------------------------------------------------------------------------

func TestRegisterManifestHistoryService_NilDBError(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{})
	require.ErrorIs(t, err, ErrDBRequired)
}

func TestRegisterManifestHistoryService_WithApplier(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{
		DB:      db,
		Applier: &stubApplier{},
	})
	require.NoError(t, err)
}

func TestRegisterManifestHistoryService_WithExportCollectorsAndApplier(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{
		DB:               db,
		ExportCollectors: &manifest.ExportCollectors{},
		Applier:          &stubApplier{},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// SeedManifestVersion - additional edge cases
// ---------------------------------------------------------------------------

func TestSeedManifestVersion_NilManifest(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tc := testdb.SetupTenantSchema(t, db, "test_seed_nil")
	defer tc.Cleanup()
	testdb.CreateTable(t, tc.DB, tc.Tenant, manifestVersionsDDL)

	// nil manifest - should return an error wrapping the nil-manifest sentinel.
	_, err := SeedManifestVersion(tc.Ctx, tc.DB, nil, "system:bootstrap")
	require.Error(t, err)
}

func TestSeedManifestVersion_NilDBWithManifest(t *testing.T) {
	mf := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "Test", Industry: "platform"},
	}
	_, err := SeedManifestVersion(context.Background(), nil, mf, "system:bootstrap")
	require.ErrorIs(t, err, ErrDBRequired)
}

func TestSeedManifestVersion_SecondSeedReturnsFalse(t *testing.T) {
	// Equivalent to the idempotency test already in bootstrap_test.go but expressed
	// as an explicit second-seed assertion for clarity.
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tc := testdb.SetupTenantSchema(t, db, "test_seed_idem")
	defer tc.Cleanup()
	testdb.CreateTable(t, tc.DB, tc.Tenant, manifestVersionsDDL)

	mf := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "Test", Industry: "platform"},
	}

	seeded, err := SeedManifestVersion(tc.Ctx, tc.DB, mf, "system:bootstrap")
	require.NoError(t, err)
	require.True(t, seeded)

	// Third call should also return false (idempotent).
	seeded, err = SeedManifestVersion(tc.Ctx, tc.DB, mf, "system:bootstrap")
	require.NoError(t, err)
	assert.False(t, seeded, "repeated seed calls should be no-ops")
}

// ---------------------------------------------------------------------------
// Stub types (local to manifest_history_test.go)
// ---------------------------------------------------------------------------

// stubApplier satisfies manifest.Applier.
type stubApplier struct{}

func (s *stubApplier) ApplyManifest(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	return &controlplanev1.ApplyManifestResponse{}, nil
}
