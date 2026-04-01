package manifest

import (
	"context"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- NewReconcileService tests ---

func TestNewReconcileService_NilHistory(t *testing.T) {
	_, err := NewReconcileService(nil, &ExportService{}, nil)
	assert.ErrorIs(t, err, ErrHistoryServiceRequired)
}

func TestNewReconcileService_NilExporter(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	_, err = NewReconcileService(svc, nil, nil)
	assert.ErrorIs(t, err, ErrNilExporter)
}

func TestNewReconcileService_NilDiffer(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	exporter, err := NewExportService(svc, nil)
	require.NoError(t, err)

	reconciler, err := NewReconcileService(svc, exporter, nil)
	require.NoError(t, err)
	assert.NotNil(t, reconciler)
}

// --- diffPlanToDriftResult tests ---
// These tests verify the core conversion logic without needing a DB.

func TestDiffPlanToDriftItems_NoDrift(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceAccountType, ResourceCode: "CURRENT", Action: differ.ActionNoChange},
		},
	}

	result := diffPlanToReconcileResult(plan, "1.0")

	assert.Empty(t, result.DriftItems)
	assert.Equal(t, 0, result.Summary.TotalDrifted)
	assert.Equal(t, 2, result.Summary.TotalChecked)
	assert.Equal(t, "1.0", result.ReconciledVersion)
}

func TestDiffPlanToDriftItems_MissingResource(t *testing.T) {
	// DELETE in differ means: in stored (old) but not in live (new) -> MISSING.
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionDelete, Description: "Delete instrument GBP"},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionNoChange},
		},
	}

	result := diffPlanToReconcileResult(plan, "1.0")

	require.Len(t, result.DriftItems, 1)
	assert.Equal(t, DriftTypeMissing, result.DriftItems[0].DriftType)
	assert.Equal(t, "GBP", result.DriftItems[0].ResourceCode)
	assert.Equal(t, "instrument", result.DriftItems[0].ResourceType)
	assert.Equal(t, 1, result.Summary.Missing)
	assert.Equal(t, 1, result.Summary.TotalDrifted)
	assert.Equal(t, 2, result.Summary.TotalChecked)
}

func TestDiffPlanToDriftItems_ExtraResource(t *testing.T) {
	// CREATE in differ means: in live (new) but not in stored (old) -> EXTRA.
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionCreate, Description: "Create instrument EUR"},
		},
	}

	result := diffPlanToReconcileResult(plan, "1.0")

	require.Len(t, result.DriftItems, 1)
	assert.Equal(t, DriftTypeExtra, result.DriftItems[0].DriftType)
	assert.Equal(t, "EUR", result.DriftItems[0].ResourceCode)
	assert.Equal(t, 1, result.Summary.Extra)
	assert.Equal(t, 1, result.Summary.TotalDrifted)
}

func TestDiffPlanToDriftItems_ModifiedResource(t *testing.T) {
	// UPDATE in differ means: both exist but differ -> MODIFIED.
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{
				ResourceType: differ.ResourceInstrument,
				ResourceCode: "GBP",
				Action:       differ.ActionUpdate,
				Description:  "Update instrument GBP (name: \"Pound\" -> \"Sterling\")",
			},
		},
	}

	result := diffPlanToReconcileResult(plan, "1.0")

	require.Len(t, result.DriftItems, 1)
	assert.Equal(t, DriftTypeModified, result.DriftItems[0].DriftType)
	assert.Equal(t, "GBP", result.DriftItems[0].ResourceCode)
	assert.Contains(t, result.DriftItems[0].Description, "Update instrument GBP")
	assert.Equal(t, 1, result.Summary.Modified)
}

func TestDiffPlanToDriftItems_MixedDrift(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{ResourceType: differ.ResourceInstrument, ResourceCode: "GBP", Action: differ.ActionNoChange},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "KWH", Action: differ.ActionDelete},
			{ResourceType: differ.ResourceInstrument, ResourceCode: "EUR", Action: differ.ActionCreate},
			{ResourceType: differ.ResourceSaga, ResourceCode: "settle", Action: differ.ActionUpdate, Description: "script changed"},
		},
	}

	result := diffPlanToReconcileResult(plan, "2.0")

	assert.Equal(t, 4, result.Summary.TotalChecked)
	assert.Equal(t, 3, result.Summary.TotalDrifted)
	assert.Equal(t, 1, result.Summary.Missing)
	assert.Equal(t, 1, result.Summary.Extra)
	assert.Equal(t, 1, result.Summary.Modified)
	assert.Equal(t, "2.0", result.ReconciledVersion)
}

// --- DriftItem to Proto conversion tests ---

func TestToDriftTypeProto(t *testing.T) {
	tests := []struct {
		input    DriftItemType
		expected controlplanev1.DriftType
	}{
		{DriftTypeMissing, controlplanev1.DriftType_DRIFT_TYPE_MISSING},
		{DriftTypeModified, controlplanev1.DriftType_DRIFT_TYPE_MODIFIED},
		{DriftTypeExtra, controlplanev1.DriftType_DRIFT_TYPE_EXTRA},
		{"unknown", controlplanev1.DriftType_DRIFT_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, toDriftTypeProto(tt.input))
	}
}

func TestReconcileResult_ToProtoResponse(t *testing.T) {
	reconciledAt := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)
	result := &ReconcileResult{
		DriftItems: []DriftItem{
			{
				ResourceType: "instrument",
				ResourceCode: "GBP",
				DriftType:    DriftTypeMissing,
				Description:  "Missing in live state",
			},
			{
				ResourceType: "saga",
				ResourceCode: "settle",
				DriftType:    DriftTypeExtra,
				Description:  "Extra in live state",
			},
		},
		Summary: ReconcileSummary{
			TotalChecked: 10,
			TotalDrifted: 2,
			Missing:      1,
			Extra:        1,
		},
		ReconciledVersion: "1.0",
		ReconciledAt:      reconciledAt,
		Warnings:          []string{"warn1"},
	}

	resp := result.ToProtoResponse()
	require.NotNil(t, resp)

	assert.Equal(t, "1.0", resp.ReconciledVersion)
	require.NotNil(t, resp.ReconciledAt)
	assert.Equal(t, reconciledAt.Unix(), resp.ReconciledAt.AsTime().Unix())
	assert.Len(t, resp.DriftItems, 2)
	assert.Equal(t, controlplanev1.DriftType_DRIFT_TYPE_MISSING, resp.DriftItems[0].DriftType)
	assert.Equal(t, "GBP", resp.DriftItems[0].ResourceCode)
	assert.Equal(t, controlplanev1.DriftType_DRIFT_TYPE_EXTRA, resp.DriftItems[1].DriftType)

	require.NotNil(t, resp.Summary)
	assert.Equal(t, int32(10), resp.Summary.TotalChecked)
	assert.Equal(t, int32(2), resp.Summary.TotalDrifted)
	assert.Equal(t, int32(1), resp.Summary.Missing)
	assert.Equal(t, int32(1), resp.Summary.Extra)
	assert.Equal(t, int32(0), resp.Summary.Modified)

	assert.Equal(t, []string{"warn1"}, resp.Warnings)
}

func TestReconcileResult_ToProtoResponse_NoDrift(t *testing.T) {
	result := &ReconcileResult{
		Summary: ReconcileSummary{
			TotalChecked: 5,
		},
		ReconciledVersion: "1.0",
	}

	resp := result.ToProtoResponse()
	assert.Empty(t, resp.DriftItems)
	assert.Equal(t, int32(5), resp.Summary.TotalChecked)
	assert.Equal(t, int32(0), resp.Summary.TotalDrifted)
}

// --- gRPC handler tests ---

func TestReconcileManifest_NilReconciler_ReturnsUnimplemented(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.ReconcileManifest(context.Background(), &controlplanev1.ReconcileManifestRequest{})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestNewHistoryHandlerWithReconcile(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	exporter, err := NewExportService(svc, nil)
	require.NoError(t, err)

	reconciler, err := NewReconcileService(svc, exporter, nil)
	require.NoError(t, err)

	handler, err := NewHistoryHandlerWithReconcile(svc, exporter, reconciler, nil)
	require.NoError(t, err)
	assert.NotNil(t, handler)
	assert.NotNil(t, handler.reconciler)
	assert.NotNil(t, handler.exporter)
}

func TestNewHistoryHandlerWithReconcile_NilHistory(t *testing.T) {
	_, err := NewHistoryHandlerWithReconcile(nil, nil, nil, nil)
	assert.ErrorIs(t, err, ErrHistoryServiceRequired)
}

// --- filterManifestSections tests ---

func TestFilterManifestSections_OnlyInstruments(t *testing.T) {
	m := testManifest("1.0")

	filtered := filterManifestSections(m, []string{"instruments"})

	assert.NotNil(t, filtered.Instruments)
	assert.Nil(t, filtered.AccountTypes)
	assert.Nil(t, filtered.Sagas)
	assert.Nil(t, filtered.ValuationRules)
}

func TestFilterManifestSections_InvalidSection_ReturnsNothing(t *testing.T) {
	m := testManifest("1.0")

	filtered := filterManifestSections(m, []string{"nonexistent"})

	assert.Nil(t, filtered.Instruments)
	assert.Nil(t, filtered.AccountTypes)
}

func TestFilterManifestSections_MultipleSections(t *testing.T) {
	m := testManifest("1.0")

	filtered := filterManifestSections(m, []string{"instruments", "sagas"})

	assert.NotNil(t, filtered.Instruments)
	assert.NotNil(t, filtered.Sagas)
	assert.Nil(t, filtered.AccountTypes)
}

func TestFilterManifestSections_PreservesVersionAndMetadata(t *testing.T) {
	m := testManifest("1.0")

	filtered := filterManifestSections(m, []string{"instruments"})

	assert.Equal(t, m.Version, filtered.Version)
	assert.Equal(t, m.Metadata, filtered.Metadata)
}

// --- End-to-end-ish reconcile test using the differ directly ---

func TestReconcile_EndToEnd_NoDrift(t *testing.T) {
	// Build stored and live manifests that are identical.
	stored := testManifest("1.0")
	live := testManifest("1.0")

	d := differ.New(nil, nil, nil)
	plan, err := d.Diff(context.Background(), stored, live, differ.WithSkipSafetyChecks())
	require.NoError(t, err)

	result := diffPlanToReconcileResult(plan, "1.0")

	assert.Empty(t, result.DriftItems)
	assert.Equal(t, 0, result.Summary.TotalDrifted)
	assert.Greater(t, result.Summary.TotalChecked, 0)
}

func TestReconcile_EndToEnd_MissingInstrument(t *testing.T) {
	// Stored has 2 instruments, live has 1.
	stored := testManifest("1.0")
	live := testManifest("1.0")
	live.Instruments = live.Instruments[:1] // Remove KWH

	d := differ.New(nil, nil, nil)
	plan, err := d.Diff(context.Background(), stored, live, differ.WithSkipSafetyChecks())
	require.NoError(t, err)

	result := diffPlanToReconcileResult(plan, "1.0")

	assert.Equal(t, 1, result.Summary.Missing)
	foundMissing := false
	for _, item := range result.DriftItems {
		if item.ResourceCode == "KWH" && item.DriftType == DriftTypeMissing {
			foundMissing = true
		}
	}
	assert.True(t, foundMissing, "expected KWH to be reported as MISSING")
}

func TestReconcile_EndToEnd_ExtraInstrument(t *testing.T) {
	// Live has an instrument that stored doesn't.
	stored := testManifest("1.0")
	live := testManifest("1.0")
	live.Instruments = append(live.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
	})

	d := differ.New(nil, nil, nil)
	plan, err := d.Diff(context.Background(), stored, live, differ.WithSkipSafetyChecks())
	require.NoError(t, err)

	result := diffPlanToReconcileResult(plan, "1.0")

	assert.Equal(t, 1, result.Summary.Extra)
	foundExtra := false
	for _, item := range result.DriftItems {
		if item.ResourceCode == "EUR" && item.DriftType == DriftTypeExtra {
			foundExtra = true
		}
	}
	assert.True(t, foundExtra, "expected EUR to be reported as EXTRA")
}

func TestReconcile_EndToEnd_ModifiedInstrument(t *testing.T) {
	stored := testManifest("1.0")
	live := testManifest("1.0")
	// Modify the name of GBP in live.
	live.Instruments[0] = &controlplanev1.InstrumentDefinition{
		Code: "GBP",
		Name: "Pound Sterling", // Different name
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "GBP",
			Precision: 2,
		},
	}

	d := differ.New(nil, nil, nil)
	plan, err := d.Diff(context.Background(), stored, live, differ.WithSkipSafetyChecks())
	require.NoError(t, err)

	result := diffPlanToReconcileResult(plan, "1.0")

	assert.Equal(t, 1, result.Summary.Modified)
	foundModified := false
	for _, item := range result.DriftItems {
		if item.ResourceCode == "GBP" && item.DriftType == DriftTypeModified {
			foundModified = true
		}
	}
	assert.True(t, foundModified, "expected GBP to be reported as MODIFIED")
}
