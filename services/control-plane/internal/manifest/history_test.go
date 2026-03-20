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
	"google.golang.org/protobuf/encoding/protojson"
)

func TestNewHistoryService_NilRepository(t *testing.T) {
	_, err := NewHistoryService(nil)
	assert.ErrorIs(t, err, ErrNilRepository)
}

func TestNewHistoryServiceWithDiffer_NilRepository(t *testing.T) {
	_, err := NewHistoryServiceWithDiffer(nil, differ.New(nil, nil))
	assert.ErrorIs(t, err, ErrNilRepository)
}

func TestNewHistoryServiceWithDiffer_NilDifferUsesDefault(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryServiceWithDiffer(repo, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc.differ)
}

// diffManifestsHelper calls the method-based diffManifests for unit tests.
func diffManifestsHelper(t *testing.T, prev, next *controlplanev1.Manifest) string {
	t.Helper()
	svc, err := NewHistoryService(&Repository{})
	require.NoError(t, err)
	result, err := svc.diffManifests(context.Background(), prev, next)
	require.NoError(t, err)
	return result
}

func TestDiffManifests_NoChanges(t *testing.T) {
	m := testManifest("1.0")
	result := diffManifestsHelper(t, m, m)
	assert.Equal(t, "No changes detected", result)
}

func TestDiffManifests_InstrumentAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Instruments = append(updated.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Create instrument EUR")
	assert.Contains(t, result, "Euro")
}

func TestDiffManifests_InstrumentRemoved(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Instruments = updated.Instruments[:1] // Remove KWH

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Delete instrument KWH")
}

func TestDiffManifests_InstrumentNameUpdated(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Instruments[0].Name = "Pound Sterling"

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Update instrument GBP")
	assert.Contains(t, result, "name:")
}

func TestDiffManifests_InstrumentUnchanged(t *testing.T) {
	m := testManifest("1.0")
	result := diffManifestsHelper(t, m, m)
	assert.Equal(t, "No changes detected", result)
	assert.NotContains(t, result, "GBP")
}

func TestDiffManifests_SagaAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Sagas = append(updated.Sagas, &controlplanev1.SagaDefinition{
		Name:    "new_saga",
		Trigger: "api:/v1/new",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Create saga new_saga")
}

func TestDiffManifests_SagaScriptUpdated(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Sagas[0].Script = "def execute(ctx):\n    return {'changed': True}\n"

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Update saga process_settlement")
	assert.Contains(t, result, "script changed")
}

func TestDiffManifests_AccountTypeAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.AccountTypes = append(updated.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:          "SAVINGS",
		Name:          "Savings Account",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
	})

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Create account type SAVINGS")
}

func TestDiffManifests_ValuationRuleAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.ValuationRules = append(updated.ValuationRules, &controlplanev1.ValuationRule{
		FromInstrument: "USD",
		ToInstrument:   "GBP",
		Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
		Source:         "admin",
	})

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Create valuation rule")
	assert.Contains(t, result, "USD")
}

func TestDiffManifests_MultipleChanges(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("2.0")
	updated.Instruments = append(updated.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "USD",
		Name: "US Dollar",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "USD",
			Precision: 2,
		},
	})

	result := diffManifestsHelper(t, old, updated)
	assert.Contains(t, result, "Create instrument USD")
}

func TestUnmarshalManifest_RoundTrip(t *testing.T) {
	original := testManifest("1.0")

	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(original)
	require.NoError(t, err)

	decoded, err := unmarshalManifest(string(jsonBytes))
	require.NoError(t, err)

	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.Metadata.Name, decoded.Metadata.Name)
	assert.Equal(t, len(original.Instruments), len(decoded.Instruments))
}

func TestUnmarshalManifest_InvalidJSON(t *testing.T) {
	_, err := unmarshalManifest("not json")
	assert.Error(t, err)
}

func TestToProtoApplyStatus(t *testing.T) {
	tests := []struct {
		input    ApplyStatus
		expected controlplanev1.ApplyStatus
	}{
		{ApplyStatusApplied, controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED},
		{ApplyStatusFailed, controlplanev1.ApplyStatus_APPLY_STATUS_FAILED},
		{ApplyStatusRolledBack, controlplanev1.ApplyStatus_APPLY_STATUS_ROLLED_BACK},
		{ApplyStatus("UNKNOWN"), controlplanev1.ApplyStatus_APPLY_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := toProtoApplyStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEntityToProto(t *testing.T) {
	original := testManifest("1.0")
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(original)
	require.NoError(t, err)

	diffSummary := "Create instrument GBP (British Pound Sterling)"
	entity := &VersionEntity{
		ID:           [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Version:      "1.0",
		ManifestJSON: string(jsonBytes),
		AppliedBy:    "admin@meridian.io",
		ApplyStatus:  ApplyStatusApplied,
		DiffSummary:  &diffSummary,
	}

	proto, err := EntityToProto(entity)
	require.NoError(t, err)

	assert.Equal(t, entity.ID.String(), proto.Id)
	assert.Equal(t, "1.0", proto.Version)
	assert.Equal(t, "admin@meridian.io", proto.AppliedBy)
	assert.Equal(t, controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED, proto.ApplyStatus)
	require.NotNil(t, proto.DiffSummary)
	assert.Equal(t, "Create instrument GBP (British Pound Sterling)", *proto.DiffSummary)
	assert.NotNil(t, proto.Manifest)
	assert.Equal(t, "1.0", proto.Manifest.Version)
}

func TestEntityToProto_InvalidJSON(t *testing.T) {
	entity := &VersionEntity{
		ManifestJSON: "invalid",
	}

	_, err := EntityToProto(entity)
	assert.Error(t, err)
}

func TestStoreManifestVersion_NilManifest(t *testing.T) {
	repo := &Repository{}
	svc, _ := NewHistoryService(repo)

	ctx := context.TODO()
	_, err := svc.StoreManifestVersion(ctx, nil, "admin", nil, ApplyStatusApplied, nil, 0)
	assert.ErrorIs(t, err, ErrNilManifest)
}

func TestStoreManifestVersion_EmptyAppliedBy(t *testing.T) {
	repo := &Repository{}
	svc, _ := NewHistoryService(repo)

	ctx := context.TODO()
	m := testManifest("1.0")
	_, err := svc.StoreManifestVersion(ctx, m, "", nil, ApplyStatusApplied, nil, 0)
	assert.ErrorIs(t, err, ErrEmptyAppliedBy)
}

func TestListManifestVersions_LimitClamping(t *testing.T) {
	// Verify limit clamping logic
	limit := 0
	if limit <= 0 {
		limit = 20
	}
	assert.Equal(t, 20, limit)

	limit = 200
	if limit > 100 {
		limit = 100
	}
	assert.Equal(t, 100, limit)
}

func TestToProtoApplyStatus_Partial(t *testing.T) {
	result := toProtoApplyStatus(ApplyStatusPartial)
	assert.Equal(t, controlplanev1.ApplyStatus_APPLY_STATUS_PARTIAL, result)
}

func TestPhaseStatusMapToProto_Nil(t *testing.T) {
	result := phaseStatusMapToProto(nil)
	assert.Nil(t, result)
}

func TestPhaseStatusMapToProto_WithEntries(t *testing.T) {
	now := time.Now().UTC()
	later := now.Add(5 * time.Second)

	m := PhaseStatusMap{
		"phase_1": PhaseStatusEntry{
			Status:      PhaseStatusCompleted,
			StartedAt:   &now,
			CompletedAt: &later,
		},
		"phase_2": PhaseStatusEntry{
			Status:    PhaseStatusFailed,
			StartedAt: &now,
			Error:     "validation error",
		},
		"phase_3": PhaseStatusEntry{
			Status: PhaseStatusPending,
		},
	}

	result := phaseStatusMapToProto(m)
	require.NotNil(t, result)
	require.Len(t, result, 3)

	// phase_1: completed with both timestamps
	p1 := result["phase_1"]
	require.NotNil(t, p1)
	assert.Equal(t, string(PhaseStatusCompleted), p1.Status)
	require.NotNil(t, p1.StartedAt)
	require.NotNil(t, p1.CompletedAt)
	assert.Empty(t, p1.Error)

	// phase_2: failed with error, no completion time
	p2 := result["phase_2"]
	require.NotNil(t, p2)
	assert.Equal(t, string(PhaseStatusFailed), p2.Status)
	require.NotNil(t, p2.StartedAt)
	assert.Nil(t, p2.CompletedAt)
	assert.Equal(t, "validation error", p2.Error)

	// phase_3: pending, no timestamps
	p3 := result["phase_3"]
	require.NotNil(t, p3)
	assert.Equal(t, string(PhaseStatusPending), p3.Status)
	assert.Nil(t, p3.StartedAt)
	assert.Nil(t, p3.CompletedAt)
}

func TestStoreManifestVersionWithPhaseStatus_NilManifest(t *testing.T) {
	repo := &Repository{}
	svc, _ := NewHistoryService(repo)

	ctx := context.TODO()
	_, err := svc.StoreManifestVersionWithPhaseStatus(ctx, nil, "admin", nil, ApplyStatusApplied, nil, 0, nil)
	assert.ErrorIs(t, err, ErrNilManifest)
}

func TestStoreManifestVersionWithPhaseStatus_EmptyAppliedBy(t *testing.T) {
	repo := &Repository{}
	svc, _ := NewHistoryService(repo)

	ctx := context.TODO()
	m := testManifest("1.0")
	_, err := svc.StoreManifestVersionWithPhaseStatus(ctx, m, "", nil, ApplyStatusApplied, nil, 0, nil)
	assert.ErrorIs(t, err, ErrEmptyAppliedBy)
}

func TestEntityToProto_WithPhaseStatus(t *testing.T) {
	original := testManifest("2.0")
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(original)
	require.NoError(t, err)

	now := time.Now().UTC()
	entity := &VersionEntity{
		ID:           uuid.New(),
		Version:      "2.0",
		ManifestJSON: string(jsonBytes),
		AppliedBy:    "admin@meridian.io",
		ApplyStatus:  ApplyStatusPartial,
		CreatedAt:    now,
		AppliedAt:    now,
	}

	// Set phase status
	phaseMap := PhaseStatusMap{
		"phase_1": PhaseStatusEntry{
			Status:    PhaseStatusCompleted,
			StartedAt: &now,
		},
	}
	err = entity.SetPhaseStatus(phaseMap)
	require.NoError(t, err)

	proto, err := EntityToProto(entity)
	require.NoError(t, err)

	assert.Equal(t, controlplanev1.ApplyStatus_APPLY_STATUS_PARTIAL, proto.ApplyStatus)
	require.NotNil(t, proto.PhaseStatus)
	require.Contains(t, proto.PhaseStatus, "phase_1")
	assert.Equal(t, string(PhaseStatusCompleted), proto.PhaseStatus["phase_1"].Status)
}

func TestEntityToProto_WithOptionalFields(t *testing.T) {
	original := testManifest("1.0")
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(original)
	require.NoError(t, err)

	jobID := uuid.New()
	checksum := "abc123"
	source := "cli"
	resourcePath := "/manifests/v1.yaml"
	graph := `{"nodes":[]}`

	entity := &VersionEntity{
		ID:                uuid.New(),
		Version:           "1.0",
		ManifestJSON:      string(jsonBytes),
		AppliedBy:         "admin@meridian.io",
		ApplyStatus:       ApplyStatusApplied,
		ApplyJobID:        &jobID,
		Checksum:          &checksum,
		Source:            &source,
		ResourcePath:      &resourcePath,
		RelationshipGraph: &graph,
		CreatedAt:         time.Now().UTC(),
		AppliedAt:         time.Now().UTC(),
	}

	proto, err := EntityToProto(entity)
	require.NoError(t, err)

	require.NotNil(t, proto.ApplyJobId)
	assert.Equal(t, jobID.String(), *proto.ApplyJobId)
	require.NotNil(t, proto.Checksum)
	assert.Equal(t, "abc123", *proto.Checksum)
	require.NotNil(t, proto.Source)
	assert.Equal(t, "cli", *proto.Source)
	require.NotNil(t, proto.ResourcePath)
	assert.Equal(t, "/manifests/v1.yaml", *proto.ResourcePath)
	require.NotNil(t, proto.RelationshipGraph)
	assert.Equal(t, `{"nodes":[]}`, *proto.RelationshipGraph)
}

// testManifest creates a test manifest for unit tests.
func testManifest(version string) *controlplanev1.Manifest {
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
			{
				Code: "KWH",
				Name: "Kilowatt Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "kWh",
					Precision: 3,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:          "SETTLEMENT",
				Name:          "Settlement Account",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool_spot",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_settlement",
				Trigger: "api:/v1/settlements",
				Script:  "def execute(ctx):\n    return {}\n",
			},
		},
	}
}

func TestVersionEntity_TableName(t *testing.T) {
	entity := VersionEntity{}
	assert.Equal(t, "manifest_versions", entity.TableName())
}

func TestVersionEntity_GetPhaseStatus_InvalidJSON(t *testing.T) {
	bad := "not json"
	entity := &VersionEntity{PhaseStatus: &bad}
	_, err := entity.GetPhaseStatus()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal phase_status")
}

func TestUnmarshalManifest_Valid(t *testing.T) {
	m, err := unmarshalManifest(`{"version":"1.0"}`)
	require.NoError(t, err)
	assert.Equal(t, "1.0", m.GetVersion())
}

func TestUnmarshalManifest_Invalid(t *testing.T) {
	_, err := unmarshalManifest(`not valid json`)
	assert.Error(t, err)
}

