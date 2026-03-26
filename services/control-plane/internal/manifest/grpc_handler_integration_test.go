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

// newHandlerFromSvc creates a bare HistoryHandler from an existing service.
func newHandlerFromSvc(t *testing.T, svc *manifest.HistoryService) *manifest.HistoryHandler {
	t.Helper()
	handler, err := manifest.NewHistoryHandler(svc, nil)
	require.NoError(t, err)
	return handler
}

// newHandlerWithExportFromSvc creates a HistoryHandler with export support.
func newHandlerWithExportFromSvc(t *testing.T, svc *manifest.HistoryService) (*manifest.HistoryHandler, *manifest.ExportService) {
	t.Helper()
	exporter, err := manifest.NewExportService(svc, nil)
	require.NoError(t, err)
	handler, err := manifest.NewHistoryHandlerWithExport(svc, exporter, nil)
	require.NoError(t, err)
	return handler, exporter
}

// newHandlerWithReconcileFromSvc creates a HistoryHandler with export and reconcile support.
func newHandlerWithReconcileFromSvc(t *testing.T, svc *manifest.HistoryService) *manifest.HistoryHandler {
	t.Helper()
	exporter, err := manifest.NewExportService(svc, nil)
	require.NoError(t, err)
	reconciler, err := manifest.NewReconcileService(svc, exporter, nil)
	require.NoError(t, err)
	handler, err := manifest.NewHistoryHandlerWithReconcile(svc, exporter, reconciler, nil)
	require.NoError(t, err)
	return handler
}

// storeVersion is a test helper that stores a manifest version and fails the test on error.
func storeVersion(t *testing.T, svc *manifest.HistoryService, ctx context.Context, version string) {
	t.Helper()
	m := testManifestProto(version)
	_, err := svc.StoreManifestVersion(ctx, m, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)
}

// --- GetCurrentManifest integration tests ---

func TestGetCurrentManifest_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler := newHandlerFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("NotFound when no versions exist", func(t *testing.T) {
		_, err := handler.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "no applied manifest found")
	})

	storeVersion(t, svc, ctx, "1.0")

	t.Run("Success returns latest applied version", func(t *testing.T) {
		resp, err := handler.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp.Version)
		assert.Equal(t, "1.0", resp.Version.Version)
		assert.Equal(t, "admin", resp.Version.AppliedBy)
		assert.NotNil(t, resp.Version.AppliedAt)
	})
}

// --- GetManifestVersion integration tests ---

func TestGetManifestVersion_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler := newHandlerFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("NotFound for unknown version", func(t *testing.T) {
		_, err := handler.GetManifestVersion(ctx, &controlplanev1.GetManifestVersionRequest{
			Version: "99.99",
		})
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "99.99")
	})

	storeVersion(t, svc, ctx, "2.0")

	t.Run("Success returns the requested version", func(t *testing.T) {
		resp, err := handler.GetManifestVersion(ctx, &controlplanev1.GetManifestVersionRequest{
			Version: "2.0",
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Version)
		assert.Equal(t, "2.0", resp.Version.Version)
		assert.NotNil(t, resp.Version.AppliedAt)
	})
}

// --- ListManifestVersions integration tests ---

func TestListManifestVersions_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler := newHandlerFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("Empty list when no versions exist", func(t *testing.T) {
		resp, err := handler.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Versions)
		assert.Equal(t, int32(0), resp.TotalCount)
	})

	for _, v := range []string{"1.0", "2.0", "3.0"} {
		storeVersion(t, svc, ctx, v)
	}

	t.Run("Returns all versions with correct total count", func(t *testing.T) {
		resp, err := handler.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Versions, 3)
		assert.Equal(t, int32(3), resp.TotalCount)
	})

	t.Run("Pagination limits results", func(t *testing.T) {
		resp, err := handler.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Versions, 2)
		assert.Equal(t, int32(3), resp.TotalCount)
	})

	t.Run("Pagination with offset returns remaining without overlap", func(t *testing.T) {
		firstPage, err := handler.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		require.Len(t, firstPage.Versions, 2)

		secondPage, err := handler.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{
			Limit:  10,
			Offset: 2,
		})
		require.NoError(t, err)
		assert.Len(t, secondPage.Versions, 1)
		assert.Equal(t, int32(3), secondPage.TotalCount)

		// Verify pages don't overlap
		firstVersions := make(map[string]bool)
		for _, v := range firstPage.Versions {
			firstVersions[v.Version] = true
		}
		for _, v := range secondPage.Versions {
			assert.False(t, firstVersions[v.Version], "version %s appeared in both pages", v.Version)
		}
	})
}

// --- DiffManifestVersions integration tests ---

func TestDiffManifestVersions_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler := newHandlerFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("NotFound for unknown sequence", func(t *testing.T) {
		_, err := handler.DiffManifestVersions(ctx, &controlplanev1.DiffManifestVersionsRequest{
			TargetSequenceNumber: 999,
		})
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "manifest version not found")
	})

	// Store version 1
	m1 := testManifestProto("1.0")
	_, err := svc.StoreManifestVersion(ctx, m1, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	// Store version 2 with an extra instrument
	m2 := testManifestProto("2.0")
	m2.Instruments = append(m2.Instruments, &controlplanev1.InstrumentDefinition{
		Code:       "EUR",
		Name:       "Euro",
		Type:       controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{Unit: "EUR", Precision: 2},
	})
	_, err = svc.StoreManifestVersion(ctx, m2, "admin", nil, manifest.ApplyStatusApplied, nil, 0)
	require.NoError(t, err)

	t.Run("Success with explicit base and target", func(t *testing.T) {
		resp, err := handler.DiffManifestVersions(ctx, &controlplanev1.DiffManifestVersionsRequest{
			BaseSequenceNumber:   1,
			TargetSequenceNumber: 2,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), resp.BaseSequenceNumber)
		assert.Equal(t, int64(2), resp.TargetSequenceNumber)
		require.NotNil(t, resp.Summary)
		assert.Greater(t, resp.Summary.TotalActions, int32(0))
	})

	t.Run("Success with zero base defaults to previous", func(t *testing.T) {
		resp, err := handler.DiffManifestVersions(ctx, &controlplanev1.DiffManifestVersionsRequest{
			BaseSequenceNumber:   0,
			TargetSequenceNumber: 2,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), resp.BaseSequenceNumber)
		assert.Equal(t, int64(2), resp.TargetSequenceNumber)
		assert.NotNil(t, resp.Summary)
	})
}

// --- ExportManifest integration tests ---

func TestExportManifest_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler, _ := newHandlerWithExportFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("Success with no fallback and no collectors", func(t *testing.T) {
		resp, err := handler.ExportManifest(ctx, &controlplanev1.ExportManifestRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotEmpty(t, resp.Checksum)
	})

	storeVersion(t, svc, ctx, "1.0")

	t.Run("Success uses latest version as fallback", func(t *testing.T) {
		resp, err := handler.ExportManifest(ctx, &controlplanev1.ExportManifestRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp.Manifest)
		assert.Equal(t, "1.0", resp.Manifest.Version)
		assert.NotEmpty(t, resp.Checksum)
	})

	storeVersion(t, svc, ctx, "2.0")

	t.Run("Success with specific version as fallback", func(t *testing.T) {
		resp, err := handler.ExportManifest(ctx, &controlplanev1.ExportManifestRequest{
			ManifestVersion: "1.0",
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Manifest)
		assert.Equal(t, "1.0", resp.Manifest.Version)
	})

	t.Run("Section filter includes requested section", func(t *testing.T) {
		resp, err := handler.ExportManifest(ctx, &controlplanev1.ExportManifestRequest{
			IncludeSections: []string{"instruments"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Manifest)
		assert.NotEmpty(t, resp.Manifest.Instruments)
	})

	t.Run("Section filter excludes non-requested sections", func(t *testing.T) {
		// Request only sagas; instruments are stored but should be absent in response.
		resp, err := handler.ExportManifest(ctx, &controlplanev1.ExportManifestRequest{
			IncludeSections: []string{"sagas"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Manifest)
		assert.Empty(t, resp.Manifest.Instruments)
	})
}

// --- ReconcileManifest integration tests ---

func TestReconcileManifest_Integration(t *testing.T) {
	svc, tc := setupHistoryService(t)
	handler := newHandlerWithReconcileFromSvc(t, svc)
	ctx := tc.Ctx

	t.Run("NotFound when no versions exist", func(t *testing.T) {
		_, err := handler.ReconcileManifest(ctx, &controlplanev1.ReconcileManifestRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("NotFound for unknown specific version", func(t *testing.T) {
		_, err := handler.ReconcileManifest(ctx, &controlplanev1.ReconcileManifestRequest{
			Version: "nonexistent-99.99",
		})
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "manifest version not found")
	})

	storeVersion(t, svc, ctx, "1.0")

	t.Run("Success with explicit version", func(t *testing.T) {
		resp, err := handler.ReconcileManifest(ctx, &controlplanev1.ReconcileManifestRequest{
			Version: "1.0",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "1.0", resp.ReconciledVersion)
		assert.NotNil(t, resp.Summary)
	})

	t.Run("Success with current version (no version specified)", func(t *testing.T) {
		resp, err := handler.ReconcileManifest(ctx, &controlplanev1.ReconcileManifestRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "1.0", resp.ReconciledVersion)
	})

	t.Run("Success with section filter", func(t *testing.T) {
		resp, err := handler.ReconcileManifest(ctx, &controlplanev1.ReconcileManifestRequest{
			Version:         "1.0",
			IncludeSections: []string{"instruments"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotNil(t, resp.Summary)
	})
}
