package manifest_test

import (
	"testing"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHistoryService(t *testing.T) (*manifest.HistoryService, *testdb.TenantTestContext) {
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

	svc, err := manifest.NewHistoryService(repo)
	require.NoError(t, err)

	return svc, tc
}

func TestHistoryService_StoreAndRetrieve(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	stored, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)
	assert.Equal(t, "1.0", stored.Version)
	assert.Equal(t, "admin@meridian.io", stored.AppliedBy)
	assert.Equal(t, manifest.ApplyStatusApplied, stored.ApplyStatus)

	// Retrieve current
	current, err := svc.GetCurrentManifest(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, "1.0", current.Version)
}

func TestHistoryService_StoreWithJobID(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	jobID := uuid.New()
	stored, err := svc.StoreManifestVersion(tc.Ctx, m, "system", &jobID, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)
	require.NotNil(t, stored.ApplyJobID)
	assert.Equal(t, jobID, *stored.ApplyJobID)
}

func TestHistoryService_DiffSummaryGeneration(t *testing.T) {
	svc, tc := setupHistoryService(t)

	// Store first version
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m1, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Store second version with changes
	m2 := testManifestProto("2.0")
	m2.Instruments = append(m2.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})

	stored, err := svc.StoreManifestVersion(tc.Ctx, m2, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)
	require.NotNil(t, stored.DiffSummary)
	assert.Contains(t, *stored.DiffSummary, "Instrument added: EUR")
	assert.Contains(t, *stored.DiffSummary, "Schema version changed: 1.0 -> 2.0")
}

func TestHistoryService_NoDiffForFirstVersion(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	stored, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)
	// First version has no previous to diff against, so diff_summary should be nil
	assert.Nil(t, stored.DiffSummary)
}

func TestHistoryService_NoDiffForFailedStatus(t *testing.T) {
	svc, tc := setupHistoryService(t)

	// Store a successful version first
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m1, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Store a failed version - should not attempt diff
	m2 := testManifestProto("2.0")
	stored, err := svc.StoreManifestVersion(tc.Ctx, m2, "admin@meridian.io", nil, manifest.ApplyStatusFailed, nil)
	require.NoError(t, err)
	assert.Nil(t, stored.DiffSummary)
}

func TestHistoryService_GetManifestVersion(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("3.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	found, err := svc.GetManifestVersion(tc.Ctx, "3.0")
	require.NoError(t, err)
	assert.Equal(t, "3.0", found.Version)
}

func TestHistoryService_GetManifestVersion_NotFound(t *testing.T) {
	svc, tc := setupHistoryService(t)

	_, err := svc.GetManifestVersion(tc.Ctx, "99.0")
	assert.ErrorIs(t, err, manifest.ErrVersionNotFound)
}

func TestHistoryService_ListManifestVersions(t *testing.T) {
	svc, tc := setupHistoryService(t)

	// Store 3 versions
	for _, v := range []string{"1.0", "1.1", "2.0"} {
		m := testManifestProto(v)
		_, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
		require.NoError(t, err)
	}

	versions, total, err := svc.ListManifestVersions(tc.Ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, versions, 3)
}

func TestHistoryService_ListManifestVersions_DefaultLimit(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Zero limit should default to 20
	versions, _, err := svc.ListManifestVersions(tc.Ctx, 0, 0)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

func TestHistoryService_CompareVersions(t *testing.T) {
	svc, tc := setupHistoryService(t)

	// Store v1
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m1, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Store v2 with changes
	m2 := testManifestProto("2.0")
	m2.Instruments = append(m2.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "USD",
		Name: "US Dollar",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "USD",
			Precision: 2,
		},
	})
	_, err = svc.StoreManifestVersion(tc.Ctx, m2, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	diff, err := svc.CompareVersions(tc.Ctx, "1.0", "2.0")
	require.NoError(t, err)
	assert.Contains(t, diff, "Instrument added: USD")
	assert.Contains(t, diff, "Schema version changed: 1.0 -> 2.0")
}

func TestHistoryService_CompareVersions_NotFound(t *testing.T) {
	svc, tc := setupHistoryService(t)

	_, err := svc.CompareVersions(tc.Ctx, "1.0", "2.0")
	assert.Error(t, err)
}

func TestHistoryService_RollbackToVersion(t *testing.T) {
	svc, tc := setupHistoryService(t)

	// Store v1.0
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m1, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Store v2.0
	m2 := testManifestProto("2.0")
	m2.Instruments = append(m2.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})
	_, err = svc.StoreManifestVersion(tc.Ctx, m2, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	// Verify current is v2.0
	current, err := svc.GetCurrentManifest(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, "2.0", current.Version)

	// Rollback to v1.0
	jobID := uuid.New()
	rollback, err := svc.RollbackToVersion(tc.Ctx, "1.0", "admin@meridian.io", &jobID)
	require.NoError(t, err)
	assert.Equal(t, "1.0", rollback.Version)
	assert.Equal(t, manifest.ApplyStatusApplied, rollback.ApplyStatus)

	// Current should now be the rollback (v1.0 content, new record)
	current, err = svc.GetCurrentManifest(tc.Ctx)
	require.NoError(t, err)
	assert.Equal(t, "1.0", current.Version)

	// Verify forward-only audit trail: should have 3 records total
	versions, total, err := svc.ListManifestVersions(tc.Ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, versions, 3)
}

func TestHistoryService_RollbackToVersion_NotFound(t *testing.T) {
	svc, tc := setupHistoryService(t)

	_, err := svc.RollbackToVersion(tc.Ctx, "99.0", "admin@meridian.io", nil)
	assert.Error(t, err)
}

func TestHistoryService_RollbackToVersion_EmptyAppliedBy(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	_, err = svc.RollbackToVersion(tc.Ctx, "1.0", "", nil)
	assert.ErrorIs(t, err, manifest.ErrEmptyAppliedBy)
}

func TestHistoryService_EntityToProto(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	stored, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	proto, err := manifest.EntityToProto(stored)
	require.NoError(t, err)

	assert.Equal(t, stored.ID.String(), proto.Id)
	assert.Equal(t, "1.0", proto.Version)
	assert.NotNil(t, proto.Manifest)
	assert.Equal(t, "admin@meridian.io", proto.AppliedBy)
	assert.Equal(t, controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED, proto.ApplyStatus)
}

func TestHistoryService_EntityToProto_IncludesSequenceNumber(t *testing.T) {
	svc, tc := setupHistoryService(t)

	m := testManifestProto("1.0")
	stored, err := svc.StoreManifestVersion(tc.Ctx, m, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	proto, err := manifest.EntityToProto(stored)
	require.NoError(t, err)

	assert.Equal(t, int64(1), proto.SequenceNumber)

	// Store second version
	m2 := testManifestProto("2.0")
	stored2, err := svc.StoreManifestVersion(tc.Ctx, m2, "admin@meridian.io", nil, manifest.ApplyStatusApplied, nil)
	require.NoError(t, err)

	proto2, err := manifest.EntityToProto(stored2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), proto2.SequenceNumber)
}

func TestHistoryService_EntityToProto_IncludesNewFields(t *testing.T) {
	repo, tc := setupTestRepo(t)

	checksum := "sha256:abc123"
	source := "cli"
	resourcePath := "/path/to/manifest.yaml"

	entity := newTestEntity("1.0", "admin@meridian.io", manifest.ApplyStatusApplied)
	entity.Checksum = &checksum
	entity.Source = &source
	entity.ResourcePath = &resourcePath

	err := repo.Store(tc.Ctx, entity)
	require.NoError(t, err)

	proto, err := manifest.EntityToProto(entity)
	require.NoError(t, err)

	require.NotNil(t, proto.Checksum)
	assert.Equal(t, "sha256:abc123", *proto.Checksum)
	require.NotNil(t, proto.Source)
	assert.Equal(t, "cli", *proto.Source)
	require.NotNil(t, proto.ResourcePath)
	assert.Equal(t, "/path/to/manifest.yaml", *proto.ResourcePath)
}
