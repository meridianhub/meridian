package manifest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

// --- toProtoApplyStatus coverage for all statuses ---

func TestToProtoApplyStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		input    ApplyStatus
		expected controlplanev1.ApplyStatus
	}{
		{ApplyStatusApplied, controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED},
		{ApplyStatusFailed, controlplanev1.ApplyStatus_APPLY_STATUS_FAILED},
		{ApplyStatusRolledBack, controlplanev1.ApplyStatus_APPLY_STATUS_ROLLED_BACK},
		{ApplyStatusPartial, controlplanev1.ApplyStatus_APPLY_STATUS_PARTIAL},
		{"unknown", controlplanev1.ApplyStatus_APPLY_STATUS_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, toProtoApplyStatus(tt.input))
		})
	}
}

// --- EntityToProto with optional fields ---

func TestEntityToProto_WithApplyJobID(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	jobID := uuid.New()
	entity.ApplyJobID = &jobID

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	require.NotNil(t, proto.ApplyJobId)
	assert.Equal(t, jobID.String(), *proto.ApplyJobId)
}

func TestEntityToProto_WithDiffSummary(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	summary := "Added instrument GBP"
	entity.DiffSummary = &summary

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	require.NotNil(t, proto.DiffSummary)
	assert.Equal(t, "Added instrument GBP", *proto.DiffSummary)
}

func TestEntityToProto_WithRelationshipGraph(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	graph := `{"nodes":["a","b"]}`
	entity.RelationshipGraph = &graph

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	require.NotNil(t, proto.RelationshipGraph)
	assert.Equal(t, `{"nodes":["a","b"]}`, *proto.RelationshipGraph)
}

func TestEntityToProto_WithChecksum(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	checksum := "sha256:abc123"
	entity.Checksum = &checksum

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	require.NotNil(t, proto.Checksum)
	assert.Equal(t, "sha256:abc123", *proto.Checksum)
}

func TestEntityToProto_WithSourceAndResourcePath(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	source := "cli"
	resourcePath := "/manifests/v1.yaml"
	entity.Source = &source
	entity.ResourcePath = &resourcePath

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	require.NotNil(t, proto.Source)
	assert.Equal(t, "cli", *proto.Source)
	require.NotNil(t, proto.ResourcePath)
	assert.Equal(t, "/manifests/v1.yaml", *proto.ResourcePath)
}

func TestEntityToProto_NilPhaseStatus(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	assert.Nil(t, proto.PhaseStatus)
}

// --- phaseStatusMapToProto ---

func TestPhaseStatusMapToProto_WithTimestamps(t *testing.T) {
	now := time.Now().UTC()
	m := PhaseStatusMap{
		"p1": {
			Status:      "COMPLETED",
			StartedAt:   &now,
			CompletedAt: &now,
		},
	}
	result := phaseStatusMapToProto(m)
	require.Len(t, result, 1)
	assert.Equal(t, "COMPLETED", result["p1"].Status)
	assert.NotNil(t, result["p1"].StartedAt)
	assert.NotNil(t, result["p1"].CompletedAt)
}

func TestPhaseStatusMapToProto_WithoutTimestamps(t *testing.T) {
	m := PhaseStatusMap{
		"p1": {
			Status: "PENDING",
			Error:  "waiting",
		},
	}
	result := phaseStatusMapToProto(m)
	require.Len(t, result, 1)
	assert.Equal(t, "PENDING", result["p1"].Status)
	assert.Nil(t, result["p1"].StartedAt)
	assert.Nil(t, result["p1"].CompletedAt)
	assert.Equal(t, "waiting", result["p1"].Error)
}

// --- VersionEntity phase status round-trip ---

func TestVersionEntity_SetAndGetPhaseStatus_RoundTrip(t *testing.T) {
	entity := &VersionEntity{}
	now := time.Now().UTC()
	m := PhaseStatusMap{
		"p1": {Status: "COMPLETED", StartedAt: &now, CompletedAt: &now},
	}
	err := entity.SetPhaseStatus(m)
	require.NoError(t, err)
	require.NotNil(t, entity.PhaseStatus)

	result, err := entity.GetPhaseStatus()
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, PhaseExecutionStatus("COMPLETED"), result["p1"].Status)
}

func TestVersionEntity_GetPhaseStatus_NilField(t *testing.T) {
	entity := &VersionEntity{}
	result, err := entity.GetPhaseStatus()
	require.NoError(t, err)
	assert.Nil(t, result)
}

// --- EntityToProto with sequence number ---

func TestEntityToProto_SequenceNumber(t *testing.T) {
	entity := newTestEntity(t, "1.0")
	entity.SequenceNumber = 42

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	assert.Equal(t, int64(42), proto.SequenceNumber)
}

// --- Entity with all nil optional fields ---

func TestEntityToProto_AllNilOptionalFields(t *testing.T) {
	m := testManifest("1.0")
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(m)
	require.NoError(t, err)

	entity := &VersionEntity{
		ID:           uuid.New(),
		Version:      "1.0",
		ManifestJSON: string(jsonBytes),
		AppliedAt:    time.Now().UTC(),
		AppliedBy:    "admin",
		ApplyStatus:  ApplyStatusApplied,
		CreatedAt:    time.Now().UTC(),
	}

	proto, err := EntityToProto(entity)
	require.NoError(t, err)
	assert.Nil(t, proto.ApplyJobId)
	assert.Nil(t, proto.DiffSummary)
	assert.Nil(t, proto.RelationshipGraph)
	assert.Nil(t, proto.Checksum)
	assert.Nil(t, proto.Source)
	assert.Nil(t, proto.ResourcePath)
	assert.Nil(t, proto.PhaseStatus)
}

// --- Error sentinels ---

func TestHistoryErrorSentinels(t *testing.T) {
	assert.EqualError(t, ErrNilRepository, "repository cannot be nil")
	assert.EqualError(t, ErrNilManifest, "manifest cannot be nil")
	assert.EqualError(t, ErrEmptyAppliedBy, "applied_by cannot be empty")
}
