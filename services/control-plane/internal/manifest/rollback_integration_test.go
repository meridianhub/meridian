package manifest_test

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testApplier records apply calls and returns configurable responses.
type testApplier struct {
	called  bool
	request *controlplanev1.ApplyManifestRequest
	resp    *controlplanev1.ApplyManifestResponse
	err     error
}

func (a *testApplier) ApplyManifest(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	a.called = true
	a.request = req
	if a.err != nil {
		return nil, a.err
	}
	if a.resp != nil {
		return a.resp, nil
	}
	return &controlplanev1.ApplyManifestResponse{
		Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
	}, nil
}

func setupRollbackHandler(t *testing.T) (*manifest.HistoryHandler, *manifest.HistoryService, *testApplier, context.Context) {
	t.Helper()
	svc, tc := setupHistoryService(t)
	handler, err := manifest.NewHistoryHandler(svc, nil)
	require.NoError(t, err)

	applier := &testApplier{}
	handler.SetApplier(applier)

	return handler, svc, applier, tc.Ctx
}

func TestRollbackManifest_VersionNotFound(t *testing.T) {
	handler, _, _, ctx := setupRollbackHandler(t)

	_, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 999,
		AppliedBy:            "admin",
	})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "999")
}

func TestRollbackManifest_DryRun(t *testing.T) {
	handler, svc, applier, ctx := setupRollbackHandler(t)

	// Store two versions so we have something to rollback to.
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	m2 := testManifestProto("2.0")
	_, err = svc.StoreManifestVersion(ctx, m2, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	resp, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 1,
		DryRun:               true,
		AppliedBy:            "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, controlplanev1.RollbackStatus_ROLLBACK_STATUS_DRY_RUN, resp.Status)
	assert.Contains(t, resp.Message, "dry run")
	assert.False(t, applier.called, "applier should not be called during dry run")
}

func TestRollbackManifest_NoChange(t *testing.T) {
	handler, svc, applier, ctx := setupRollbackHandler(t)

	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	// Rollback to the current version (sequence 1 when only 1 version exists).
	resp, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 1,
		AppliedBy:            "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, controlplanev1.RollbackStatus_ROLLBACK_STATUS_NO_CHANGE, resp.Status)
	assert.Contains(t, resp.Message, "already the current version")
	assert.False(t, applier.called)
}

func TestRollbackManifest_SuccessfulRollback(t *testing.T) {
	handler, svc, applier, ctx := setupRollbackHandler(t)

	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	m2 := testManifestProto("2.0")
	_, err = svc.StoreManifestVersion(ctx, m2, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	resp, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 1,
		AppliedBy:            "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, controlplanev1.RollbackStatus_ROLLBACK_STATUS_COMPLETED, resp.Status)
	assert.Contains(t, resp.Message, "rolled back to sequence 1")
	assert.True(t, applier.called)
	assert.Equal(t, "rollback:admin", applier.request.AppliedBy)
	assert.True(t, applier.request.Force)
}

func TestRollbackManifest_ApplierFailure(t *testing.T) {
	handler, svc, applier, ctx := setupRollbackHandler(t)

	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	m2 := testManifestProto("2.0")
	_, err = svc.StoreManifestVersion(ctx, m2, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	applier.err = assert.AnError

	resp, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 1,
		AppliedBy:            "admin",
	})
	require.NoError(t, err) // Handler returns response, not error
	assert.Equal(t, controlplanev1.RollbackStatus_ROLLBACK_STATUS_FAILED, resp.Status)
	assert.Contains(t, resp.Message, "apply pipeline returned an error")
}

func TestRollbackManifest_ApplierNonAppliedStatus(t *testing.T) {
	handler, svc, applier, ctx := setupRollbackHandler(t)

	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	m2 := testManifestProto("2.0")
	_, err = svc.StoreManifestVersion(ctx, m2, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	applier.resp = &controlplanev1.ApplyManifestResponse{
		Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED,
	}

	resp, err := handler.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: 1,
		AppliedBy:            "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, controlplanev1.RollbackStatus_ROLLBACK_STATUS_FAILED, resp.Status)
	assert.Contains(t, resp.Message, "VALIDATION_FAILED")
}
