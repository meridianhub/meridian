package manifest_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

const manifestVersionsDDL = `CREATE TABLE IF NOT EXISTS %s.manifest_versions (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	version VARCHAR(50) NOT NULL,
	manifest_json JSONB NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	applied_by VARCHAR(255) NOT NULL,
	apply_status VARCHAR(20) NOT NULL DEFAULT 'APPLIED',
	apply_job_id UUID,
	diff_summary TEXT,
	relationship_graph JSONB,
	sequence_number BIGINT NOT NULL DEFAULT 0,
	checksum VARCHAR(64),
	source VARCHAR(20),
	resource_path VARCHAR(255),
	phase_status JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT valid_apply_status CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK', 'PARTIAL'))
)`

func setupTestRepo(t *testing.T) (*manifest.Repository, *testdb.TenantTestContext) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	tc := testdb.SetupTenantSchema(t, db, "test_tenant")
	t.Cleanup(tc.Cleanup)

	testdb.CreateTable(t, tc.DB, tc.Tenant, manifestVersionsDDL)

	repo, err := manifest.NewRepository(tc.DB)
	require.NoError(t, err)

	return repo, tc
}

func newTestEntity(version, appliedBy string, status manifest.ApplyStatus) *manifest.VersionEntity {
	m := testManifestProto(version)
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, _ := marshaler.Marshal(m)

	now := time.Now().UTC()
	return &manifest.VersionEntity{
		ID:           uuid.New(),
		Version:      version,
		ManifestJSON: string(jsonBytes),
		AppliedAt:    now,
		AppliedBy:    appliedBy,
		ApplyStatus:  status,
		CreatedAt:    now,
	}
}

func testManifestProto(version string) *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: version,
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Test Manifest",
			Industry: "energy",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound Sterling",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
	}
}

func TestNewRepository_NilDB(t *testing.T) {
	_, err := manifest.NewRepository(nil)
	assert.ErrorIs(t, err, manifest.ErrNilDatabase)
}

func TestRepository_Store(t *testing.T) {
	repo, tc := setupTestRepo(t)

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)
}

func TestRepository_GetLatestApplied(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store two versions with different timestamps
	entity1 := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity1.AppliedAt = time.Now().UTC().Add(-1 * time.Hour)
	err := repo.Store(tc.Ctx, entity1, 0)
	require.NoError(t, err)

	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity2.AppliedAt = time.Now().UTC()
	err = repo.Store(tc.Ctx, entity2, 0)
	require.NoError(t, err)

	// Should return the latest (v2.0)
	latest, err := repo.GetLatestApplied(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, "2.0", latest.Version)
}

func TestRepository_GetLatestApplied_IgnoresFailed(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store applied version
	entity1 := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity1.AppliedAt = time.Now().UTC().Add(-1 * time.Hour)
	err := repo.Store(tc.Ctx, entity1, 0)
	require.NoError(t, err)

	// Store failed version (more recent)
	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusFailed)
	entity2.AppliedAt = time.Now().UTC()
	err = repo.Store(tc.Ctx, entity2, 0)
	require.NoError(t, err)

	// Should return v1.0 (latest APPLIED, not the FAILED v2.0)
	latest, err := repo.GetLatestApplied(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, "1.0", latest.Version)
}

func TestRepository_GetLatestApplied_NotFound(t *testing.T) {
	repo, tc := setupTestRepo(t)

	_, err := repo.GetLatestApplied(tc.Ctx)
	assert.ErrorIs(t, err, manifest.ErrVersionNotFound)
}

func TestRepository_GetByVersion(t *testing.T) {
	repo, tc := setupTestRepo(t)

	entity := newTestEntity("1.5", "admin@meridian.io", manifest.ApplyStatusApplied)
	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	found, err := repo.GetByVersion(tc.Ctx, "1.5")
	require.NoError(t, err)
	assert.Equal(t, "1.5", found.Version)
	assert.Equal(t, "admin@meridian.io", found.AppliedBy)
}

func TestRepository_GetByVersion_NotFound(t *testing.T) {
	repo, tc := setupTestRepo(t)

	_, err := repo.GetByVersion(tc.Ctx, "99.0")
	assert.ErrorIs(t, err, manifest.ErrVersionNotFound)
}

func TestRepository_List(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store 3 versions
	for i, v := range []string{"1.0", "1.1", "2.0"} {
		entity := newTestEntity(v, "admin@meridian.io", manifest.ApplyStatusApplied)
		entity.AppliedAt = time.Now().UTC().Add(time.Duration(i) * time.Minute)
		err := repo.Store(tc.Ctx, entity, 0)
		require.NoError(t, err)
	}

	// List all
	versions, total, err := repo.List(tc.Ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, versions, 3)
	// Should be ordered by applied_at DESC
	assert.Equal(t, "2.0", versions[0].Version)
	assert.Equal(t, "1.1", versions[1].Version)
	assert.Equal(t, "1.0", versions[2].Version)
}

func TestRepository_List_Pagination(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store 5 versions
	for i, v := range []string{"1.0", "1.1", "1.2", "1.3", "2.0"} {
		entity := newTestEntity(v, "admin@meridian.io", manifest.ApplyStatusApplied)
		entity.AppliedAt = time.Now().UTC().Add(time.Duration(i) * time.Minute)
		err := repo.Store(tc.Ctx, entity, 0)
		require.NoError(t, err)
	}

	// Page 1 (limit 2, offset 0)
	versions, total, err := repo.List(tc.Ctx, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.Len(t, versions, 2)
	assert.Equal(t, "2.0", versions[0].Version)
	assert.Equal(t, "1.3", versions[1].Version)

	// Page 2 (limit 2, offset 2)
	versions, total, err = repo.List(tc.Ctx, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.Len(t, versions, 2)
	assert.Equal(t, "1.2", versions[0].Version)
	assert.Equal(t, "1.1", versions[1].Version)
}

func TestRepository_List_Empty(t *testing.T) {
	repo, tc := setupTestRepo(t)

	versions, total, err := repo.List(tc.Ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, versions)
}

func TestRepository_GetPreviousApplied(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store two applied versions
	entity1 := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity1.AppliedAt = time.Now().UTC().Add(-2 * time.Hour)
	err := repo.Store(tc.Ctx, entity1, 0)
	require.NoError(t, err)

	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity2.AppliedAt = time.Now().UTC().Add(-1 * time.Hour)
	err = repo.Store(tc.Ctx, entity2, 0)
	require.NoError(t, err)

	// Get previous before entity2's time
	prev, err := repo.GetPreviousApplied(tc.Ctx, entity2.AppliedAt)
	require.NoError(t, err)
	assert.Equal(t, "1.0", prev.Version)
}

func TestRepository_GetPreviousApplied_NotFound(t *testing.T) {
	repo, tc := setupTestRepo(t)

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	// No version before the earliest one
	_, err = repo.GetPreviousApplied(tc.Ctx, entity.AppliedAt)
	assert.ErrorIs(t, err, manifest.ErrVersionNotFound)
}

func TestRepository_Store_SequenceNumberIncrements(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// First store should get sequence_number = 1
	entity1 := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err := repo.Store(tc.Ctx, entity1, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), entity1.SequenceNumber)

	// Second store should get sequence_number = 2
	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity2, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), entity2.SequenceNumber)

	// Third store should get sequence_number = 3
	entity3 := newTestEntity("3.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity3, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), entity3.SequenceNumber)
}

func TestRepository_GetCurrentSequenceNumber(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// No versions yet: should return 0
	seq, err := repo.GetCurrentSequenceNumber(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), seq)

	// Store a version
	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	seq, err = repo.GetCurrentSequenceNumber(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), seq)

	// Store another
	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity2, 0)
	require.NoError(t, err)

	seq, err = repo.GetCurrentSequenceNumber(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), seq)
}

func TestRepository_Store_SequenceConflict(t *testing.T) {
	repo, tc := setupTestRepo(t)

	// Store first version (sequence becomes 1)
	entity1 := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err := repo.Store(tc.Ctx, entity1, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), entity1.SequenceNumber)

	// Attempt to store with expectedSeq=1 (matches current) - should succeed
	entity2 := newTestEntity("2.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity2, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), entity2.SequenceNumber)

	// Attempt to store with expectedSeq=1 (stale, current is 2) - should fail
	entity3 := newTestEntity("3.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	err = repo.Store(tc.Ctx, entity3, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, manifest.ErrSequenceConflict)
}

func TestRepository_Store_NewColumnsPopulated(t *testing.T) {
	repo, tc := setupTestRepo(t)

	checksum := "abc123def456"
	source := "api"
	resourcePath := "/manifests/tenant-1.yaml"

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity.Checksum = &checksum
	entity.Source = &source
	entity.ResourcePath = &resourcePath

	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	// Retrieve and verify
	found, err := repo.GetByVersion(tc.Ctx, "1.0")
	require.NoError(t, err)
	assert.Equal(t, int64(1), found.SequenceNumber)
	require.NotNil(t, found.Checksum)
	assert.Equal(t, "abc123def456", *found.Checksum)
	require.NotNil(t, found.Source)
	assert.Equal(t, "api", *found.Source)
	require.NotNil(t, found.ResourcePath)
	assert.Equal(t, "/manifests/tenant-1.yaml", *found.ResourcePath)
}

func TestRepository_Store_PartialApplyStatus(t *testing.T) {
	repo, tc := setupTestRepo(t)

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusPartial)
	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	found, err := repo.GetByVersion(tc.Ctx, "1.0")
	require.NoError(t, err)
	assert.Equal(t, manifest.ApplyStatusPartial, found.ApplyStatus)
}

func TestRepository_UpdatePhaseStatus(t *testing.T) {
	repo, tc := setupTestRepo(t)

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusPartial)
	err := repo.Store(tc.Ctx, entity, 0)
	require.NoError(t, err)

	// Set phase status
	now := time.Now().UTC()
	phaseMap := manifest.PhaseStatusMap{
		"phase_1": {
			Status:      manifest.PhaseStatusCompleted,
			StartedAt:   &now,
			CompletedAt: &now,
		},
		"phase_2": {
			Status:    manifest.PhaseStatusFailed,
			StartedAt: &now,
			Error:     "instrument registration failed",
		},
		"phase_3": {
			Status: manifest.PhaseStatusSkipped,
		},
	}
	err = entity.SetPhaseStatus(phaseMap)
	require.NoError(t, err)

	err = repo.UpdatePhaseStatus(tc.Ctx, entity.ID, entity.PhaseStatus)
	require.NoError(t, err)

	// Retrieve and verify
	found, err := repo.GetByVersion(tc.Ctx, "1.0")
	require.NoError(t, err)
	require.NotNil(t, found.PhaseStatus)

	ps, err := found.GetPhaseStatus()
	require.NoError(t, err)
	require.Len(t, ps, 3)

	assert.Equal(t, manifest.PhaseStatusCompleted, ps["phase_1"].Status)
	assert.Equal(t, manifest.PhaseStatusFailed, ps["phase_2"].Status)
	assert.Equal(t, "instrument registration failed", ps["phase_2"].Error)
	assert.Equal(t, manifest.PhaseStatusSkipped, ps["phase_3"].Status)
}

func TestVersionEntity_GetPhaseStatus_Nil(t *testing.T) {
	entity := &manifest.VersionEntity{}
	ps, err := entity.GetPhaseStatus()
	require.NoError(t, err)
	assert.Nil(t, ps)
}

func TestVersionEntity_SetPhaseStatus_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := manifest.PhaseStatusMap{
		"phase_1": {
			Status:      manifest.PhaseStatusCompleted,
			StartedAt:   &now,
			CompletedAt: &now,
		},
		"phase_2": {
			Status:    manifest.PhaseStatusFailed,
			StartedAt: &now,
			Error:     "something went wrong",
		},
	}

	entity := &manifest.VersionEntity{}
	err := entity.SetPhaseStatus(original)
	require.NoError(t, err)
	require.NotNil(t, entity.PhaseStatus)

	roundTripped, err := entity.GetPhaseStatus()
	require.NoError(t, err)
	assert.Equal(t, original["phase_1"].Status, roundTripped["phase_1"].Status)
	assert.Equal(t, original["phase_2"].Error, roundTripped["phase_2"].Error)
}

func TestVersionEntity_SetPhaseStatus_Nil(t *testing.T) {
	entity := &manifest.VersionEntity{}
	err := entity.SetPhaseStatus(nil)
	require.NoError(t, err)
	assert.Nil(t, entity.PhaseStatus)
}
