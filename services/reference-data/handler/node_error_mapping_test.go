package handler

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestMapNodeError_AllCases(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	svc := &NodeService{logger: logger}

	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "ErrNotFound maps to NotFound",
			err:          node.ErrNotFound,
			expectedCode: codes.NotFound,
			expectedMsg:  "node not found",
		},
		{
			name:         "ErrAlreadyExists maps to AlreadyExists",
			err:          node.ErrAlreadyExists,
			expectedCode: codes.AlreadyExists,
			expectedMsg:  "active node with this resolution key already exists",
		},
		{
			name:         "ErrParentNotFound maps to NotFound",
			err:          node.ErrParentNotFound,
			expectedCode: codes.NotFound,
			expectedMsg:  "parent node not found",
		},
		{
			name:         "ErrParentNotActive maps to FailedPrecondition",
			err:          node.ErrParentNotActive,
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  "parent node is not active",
		},
		{
			name:         "ErrCrossTenantParent maps to PermissionDenied",
			err:          node.ErrCrossTenantParent,
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "parent node belongs to a different tenant",
		},
		{
			name:         "ErrImmutableIdentity maps to FailedPrecondition",
			err:          node.ErrImmutableIdentity,
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  "cannot change identity fields on active node",
		},
		{
			name:         "ErrOptimisticLock maps to Aborted",
			err:          node.ErrOptimisticLock,
			expectedCode: codes.Aborted,
			expectedMsg:  "node was modified by another transaction",
		},
		{
			name:         "ErrCircularHierarchy maps to FailedPrecondition",
			err:          node.ErrCircularHierarchy,
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  "circular hierarchy detected",
		},
		{
			name:         "ErrMaxDepthExceeded maps to FailedPrecondition",
			err:          node.ErrMaxDepthExceeded,
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  "maximum hierarchy depth exceeded",
		},
		{
			name:         "ErrInvalidTimeRange maps to InvalidArgument",
			err:          node.ErrInvalidTimeRange,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "valid_to must be after valid_from",
		},
		{
			name:         "ErrAlreadySuperseded maps to FailedPrecondition",
			err:          node.ErrAlreadySuperseded,
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  "node has already been superseded",
		},
		{
			name:         "ErrInvalidNode maps to InvalidArgument",
			err:          node.ErrInvalidNode,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "invalid node",
		},
		{
			name:         "ErrMissingTenantContext maps to Unauthenticated",
			err:          tenant.ErrMissingTenantContext,
			expectedCode: codes.Unauthenticated,
			expectedMsg:  "missing tenant context",
		},
		{
			name:         "unknown error maps to Internal",
			err:          assert.AnError,
			expectedCode: codes.Internal,
			expectedMsg:  "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := svc.mapNodeError(tc.err, "TestOp", "test-id")

			st, ok := status.FromError(result)
			assert.True(t, ok, "expected gRPC status error")
			assert.Equal(t, tc.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tc.expectedMsg)
		})
	}
}
