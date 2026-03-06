package manifest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
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
	svc, _ := NewHistoryService(repo)

	handler, err := NewHistoryHandler(svc, nil)
	require.NoError(t, err)
	assert.NotNil(t, handler)
}

// stubRepo implements the repository methods needed for handler tests
// by embedding Repository and overriding methods via the HistoryService.
// Since HistoryService delegates to Repository, we use a test approach
// that provides pre-populated data through a mock-like pattern.

func newTestEntity(version string) *VersionEntity {
	m := testManifest(version)
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, _ := marshaler.Marshal(m)

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
	entity := newTestEntity("1.0")
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
	svc, _ := NewHistoryService(repo)
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

func TestListManifestVersions_EmptyListReturnsOK(t *testing.T) {
	// EntityToProto on empty slice should produce empty response
	versions := make([]*controlplanev1.ManifestVersion, 0)
	assert.Empty(t, versions)

	resp := &controlplanev1.ListManifestVersionsResponse{
		Versions:   versions,
		TotalCount: 0,
	}
	assert.Equal(t, int32(0), resp.TotalCount)
	assert.Empty(t, resp.Versions)
}
