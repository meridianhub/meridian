package manifest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestNewHistoryHandler_NilHistory(t *testing.T) {
	_, err := NewHistoryHandler(nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "history service is required")
}

func TestNewHistoryHandler_NilLogger(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)
	assert.NotNil(t, handler)
}

func newTestEntity(t *testing.T, version string) *VersionEntity {
	t.Helper()
	m := testManifest(version)
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(m)
	require.NoError(t, err)

	return &VersionEntity{
		ID:           uuid.New(),
		Version:      version,
		ManifestJSON: string(jsonBytes),
		AppliedAt:    time.Now().UTC(),
		AppliedBy:    "admin@meridian.io",
		ApplyStatus:  ApplyStatusApplied,
		CreatedAt:    time.Now().UTC(),
	}
}

func TestGetCurrentManifest_EntityToProtoConversion(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	proto, err := EntityToProto(entity)
	require.NoError(t, err)

	assert.Equal(t, entity.ID.String(), proto.Id)
	assert.Equal(t, "1.0", proto.Version)
	assert.Equal(t, "admin@meridian.io", proto.AppliedBy)
	assert.Equal(t, controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED, proto.ApplyStatus)
	assert.NotNil(t, proto.Manifest)
}

func TestGetManifestVersion_EmptyVersionReturnsInvalidArgument(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)
	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.GetManifestVersion(context.Background(), &controlplanev1.GetManifestVersionRequest{
		Version: "",
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "version is required")
}

func TestNewHistoryHandler_ErrorSentinel(t *testing.T) {
	_, err := NewHistoryHandler(nil, nil)
	assert.ErrorIs(t, err, ErrHistoryServiceRequired)
}

func TestDiffManifestVersions_InvalidTargetSequenceNumber(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)
	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.DiffManifestVersions(context.Background(), &controlplanev1.DiffManifestVersionsRequest{
		TargetSequenceNumber: 0,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "target_sequence_number")
}

func TestDiffPlanToProtoActions(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{
				ResourceType: differ.ResourceInstrument,
				ResourceCode: "GBP",
				Action:       differ.ActionCreate,
				Description:  "Create instrument GBP",
				Breaking:     false,
			},
			{
				ResourceType: differ.ResourceSaga,
				ResourceCode: "settle",
				Action:       differ.ActionDelete,
				Description:  "Delete saga settle",
				Breaking:     true,
			},
		},
	}

	actions := diffPlanToProtoActions(plan)
	require.Len(t, actions, 2)

	assert.Equal(t, "instrument", actions[0].ResourceType)
	assert.Equal(t, "CREATE", actions[0].Action)
	assert.Equal(t, "GBP", actions[0].ResourceCode)
	assert.Equal(t, "Create instrument GBP", actions[0].Description)
	assert.False(t, actions[0].Breaking)

	assert.Equal(t, "saga", actions[1].ResourceType)
	assert.Equal(t, "DELETE", actions[1].Action)
	assert.Equal(t, "settle", actions[1].ResourceCode)
	assert.True(t, actions[1].Breaking)
}

func TestDiffPlanToProtoSummary(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{Action: differ.ActionCreate},
			{Action: differ.ActionCreate},
			{Action: differ.ActionUpdate},
			{Action: differ.ActionDelete},
			{Action: differ.ActionNoChange},
			{Action: differ.ActionNoChange},
			{Action: differ.ActionNoChange},
		},
		HasBreakingChanges: true,
	}

	summary := diffPlanToProtoSummary(plan)
	assert.Equal(t, int32(7), summary.TotalActions)
	assert.Equal(t, int32(2), summary.Creates)
	assert.Equal(t, int32(1), summary.Updates)
	assert.Equal(t, int32(1), summary.Deletes)
	assert.Equal(t, int32(3), summary.NoChanges)
	assert.True(t, summary.HasBreakingChanges)
}

func TestDiffPlanToProtoSummary_Empty(t *testing.T) {
	plan := &differ.DiffPlan{}

	summary := diffPlanToProtoSummary(plan)
	assert.Equal(t, int32(0), summary.TotalActions)
	assert.Equal(t, int32(0), summary.Creates)
	assert.False(t, summary.HasBreakingChanges)
}

func TestExportManifest_NilExporter_ReturnsUnimplemented(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.ExportManifest(context.Background(), &controlplanev1.ExportManifestRequest{})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestNewHistoryHandlerWithExport(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	exporter, err := NewExportService(svc, nil)
	require.NoError(t, err)

	handler, err := NewHistoryHandlerWithExport(svc, exporter, nil)
	require.NoError(t, err)
	assert.NotNil(t, handler)
	assert.NotNil(t, handler.exporter)
}

func TestNewHistoryHandlerWithExport_NilHistory(t *testing.T) {
	_, err := NewHistoryHandlerWithExport(nil, nil, nil)
	assert.ErrorIs(t, err, ErrHistoryServiceRequired)
}

// --- ListManifestVersions helper test (conversion logic) ---

func TestListManifestVersions_EmptyList(t *testing.T) {
	// Test the conversion path with an empty entities list.
	// We can't easily test the full RPC without a DB, but we can verify
	// that the conversion functions work correctly.
	entities := []VersionEntity{}
	versions := make([]*controlplanev1.ManifestVersion, 0, len(entities))
	for i := range entities {
		v, err := EntityToProto(&entities[i])
		require.NoError(t, err)
		versions = append(versions, v)
	}
	assert.Empty(t, versions)
}

func TestListManifestVersions_ConversionSuccess(t *testing.T) {
	// Verify entity-to-proto conversion works for multiple entities
	entity1 := newTestEntity(t, "1.0")
	entity2 := newTestEntity(t, "2.0")

	entities := []*VersionEntity{entity1, entity2}
	versions := make([]*controlplanev1.ManifestVersion, 0, len(entities))
	for _, e := range entities {
		v, err := EntityToProto(e)
		require.NoError(t, err)
		versions = append(versions, v)
	}
	require.Len(t, versions, 2)
	assert.Equal(t, "1.0", versions[0].Version)
	assert.Equal(t, "2.0", versions[1].Version)
}

// --- EntityToProto extended tests ---

func TestEntityToProto_InvalidManifestJSON(t *testing.T) {
	entity := &VersionEntity{
		ID:           uuid.New(),
		Version:      "1.0",
		ManifestJSON: "invalid json{{{",
		AppliedAt:    time.Now().UTC(),
		AppliedBy:    "admin",
		ApplyStatus:  ApplyStatusApplied,
		CreatedAt:    time.Now().UTC(),
	}

	_, err := EntityToProto(entity)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal manifest JSON")
}

func TestEntityToProto_InvalidPhaseStatusJSON(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	badJSON := "not json"
	entity.PhaseStatus = &badJSON

	_, err := EntityToProto(entity)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal phase_status")
}

// --- phaseStatusMapToProto additional test ---

func TestPhaseStatusMapToProto_Empty(t *testing.T) {
	result := phaseStatusMapToProto(PhaseStatusMap{})
	assert.NotNil(t, result)
	assert.Len(t, result, 0)
}

