package manifest

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- DiffManifestVersions: negative target ---

func TestDiffManifestVersions_NegativeTargetSequenceNumber(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)
	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.DiffManifestVersions(context.Background(), &controlplanev1.DiffManifestVersionsRequest{
		TargetSequenceNumber: -1,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// --- ExportManifest with nil exporter (already tested, but verify code path) ---

func TestExportManifest_NilExporter_CodePath(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)
	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.ExportManifest(context.Background(), &controlplanev1.ExportManifestRequest{
		IncludeSections: []string{"instruments"},
	})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

// --- ReconcileManifest with nil reconciler ---

func TestReconcileManifest_NilReconciler_WithVersion(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)
	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	_, err = handler.ReconcileManifest(context.Background(), &controlplanev1.ReconcileManifestRequest{
		Version:         "1.0",
		IncludeSections: []string{"instruments"},
	})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

// --- NewHistoryHandlerWithReconcile constructor paths ---

func TestNewHistoryHandlerWithReconcile_ValidConstruction(t *testing.T) {
	repo := &Repository{}
	svc, err := NewHistoryService(repo)
	require.NoError(t, err)

	exporter, err := NewExportService(svc, nil)
	require.NoError(t, err)

	reconciler, err := NewReconcileService(svc, exporter, nil)
	require.NoError(t, err)

	handler, err := NewHistoryHandlerWithReconcile(svc, exporter, reconciler, nil)
	require.NoError(t, err)
	require.NotNil(t, handler)
	assert.NotNil(t, handler.reconciler)
	assert.NotNil(t, handler.exporter)
	assert.NotNil(t, handler.logger)
}

// --- diffPlanToProtoActions empty ---

func TestDiffPlanToProtoActions_Empty(t *testing.T) {
	plan := &differ.DiffPlan{}
	actions := diffPlanToProtoActions(plan)
	assert.Empty(t, actions)
}

// --- diffPlanToProtoSummary: all action types ---

func TestDiffPlanToProtoSummary_AllActionTypes(t *testing.T) {
	plan := &differ.DiffPlan{
		Actions: []differ.PlannedAction{
			{Action: differ.ActionCreate},
			{Action: differ.ActionUpdate},
			{Action: differ.ActionDelete},
			{Action: differ.ActionNoChange},
		},
		HasBreakingChanges: false,
	}

	summary := diffPlanToProtoSummary(plan)
	assert.Equal(t, int32(4), summary.TotalActions)
	assert.Equal(t, int32(1), summary.Creates)
	assert.Equal(t, int32(1), summary.Updates)
	assert.Equal(t, int32(1), summary.Deletes)
	assert.Equal(t, int32(1), summary.NoChanges)
	assert.False(t, summary.HasBreakingChanges)
}
