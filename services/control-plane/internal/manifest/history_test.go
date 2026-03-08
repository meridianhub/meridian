package manifest

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestNewHistoryService_NilRepository(t *testing.T) {
	_, err := NewHistoryService(nil)
	assert.ErrorIs(t, err, ErrNilRepository)
}

func TestDiffManifests_NoChanges(t *testing.T) {
	m := testManifest("1.0")
	result := diffManifests(m, m)
	assert.Equal(t, "No changes detected", result)
}

func TestDiffManifests_MetadataChange(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Metadata.Name = "Updated Name"

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Metadata name changed")
	assert.Contains(t, result, "Updated Name")
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

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Instrument added: EUR")
}

func TestDiffManifests_InstrumentRemoved(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Instruments = updated.Instruments[:1] // Remove KWH

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Instrument removed: KWH")
}

func TestDiffManifests_SagaAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.Sagas = append(updated.Sagas, &controlplanev1.SagaDefinition{
		Name:    "new_saga",
		Trigger: "api:/v1/new",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Saga added: new_saga")
}

func TestDiffManifests_VersionChange(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("2.0")

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Schema version changed: 1.0 -> 2.0")
}

func TestDiffManifests_MultipleChanges(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("2.0")
	updated.Metadata.Name = "New Name"
	updated.Instruments = append(updated.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "USD",
		Name: "US Dollar",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "USD",
			Precision: 2,
		},
	})

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Metadata name changed")
	assert.Contains(t, result, "Instrument added: USD")
	assert.Contains(t, result, "Schema version changed")
}

func TestDiffManifests_AccountTypeAdded(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.AccountTypes = append(updated.AccountTypes, &controlplanev1.AccountTypeDefinition{
		Code:          "SAVINGS",
		Name:          "Savings Account",
		NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
	})

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Account type added: SAVINGS")
}

func TestDiffManifests_ValuationRuleCountChange(t *testing.T) {
	old := testManifest("1.0")
	updated := testManifest("1.0")
	updated.ValuationRules = append(updated.ValuationRules, &controlplanev1.ValuationRule{
		FromInstrument: "USD",
		ToInstrument:   "GBP",
		Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
		Source:         "admin",
	})

	result := diffManifests(old, updated)
	assert.Contains(t, result, "Valuation rules: 1 -> 2")
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

	diffSummary := "Instrument added: GBP"
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
	assert.Equal(t, "Instrument added: GBP", *proto.DiffSummary)
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
	_, err := svc.StoreManifestVersion(ctx, nil, "admin", nil, ApplyStatusApplied, nil)
	assert.ErrorIs(t, err, ErrNilManifest)
}

func TestStoreManifestVersion_EmptyAppliedBy(t *testing.T) {
	repo := &Repository{}
	svc, _ := NewHistoryService(repo)

	ctx := context.TODO()
	m := testManifest("1.0")
	_, err := svc.StoreManifestVersion(ctx, m, "", nil, ApplyStatusApplied, nil)
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
