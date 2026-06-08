package handler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// stubNodeRepo is an in-memory node.Repository whose behavior is fully
// controllable per-method. It lets the black-box handler tests exercise the
// repo-error and not-found branches without a testcontainer. Each "...Err"
// field, when non-nil, is returned by the corresponding method.
type stubNodeRepo struct {
	createErr       error
	updateErr       error
	getByIDErr      error
	getByIDNode     *node.Node
	getChildrenErr  error
	getAncestorsErr error
	getSubtreeErr   error
	getAsAtErr      error
	getAsAtNode     *node.Node
	getHistoryErr   error
	bulkCreateErr   error
}

func (r *stubNodeRepo) Create(_ context.Context, _ *node.Node) error { return r.createErr }

func (r *stubNodeRepo) Update(_ context.Context, _ *node.Node) error { return r.updateErr }

func (r *stubNodeRepo) GetByID(_ context.Context, _ uuid.UUID) (*node.Node, error) {
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return r.getByIDNode, nil
}

func (r *stubNodeRepo) GetAsAt(_ context.Context, _ string, _ uuid.UUID, _ time.Time) (*node.Node, error) {
	if r.getAsAtErr != nil {
		return nil, r.getAsAtErr
	}
	return r.getAsAtNode, nil
}

func (r *stubNodeRepo) GetHistory(_ context.Context, _ string, _ uuid.UUID) ([]*node.Node, error) {
	return nil, r.getHistoryErr
}

func (r *stubNodeRepo) GetByResolutionKey(_ context.Context, _, _ string, _ time.Time) (*node.Node, error) {
	return nil, node.ErrNotFound
}

func (r *stubNodeRepo) GetChildren(_ context.Context, _ string, _ uuid.UUID, _ bool) ([]*node.Node, error) {
	return nil, r.getChildrenErr
}

func (r *stubNodeRepo) ListByType(_ context.Context, _, _ string) ([]*node.Node, error) {
	return nil, nil
}

func (r *stubNodeRepo) ListRoots(_ context.Context, _ string) ([]*node.Node, error) {
	return nil, nil
}

func (r *stubNodeRepo) Supersede(_ context.Context, _ uuid.UUID, _ *node.Node) error {
	return nil
}

func (r *stubNodeRepo) GetAncestors(_ context.Context, _ uuid.UUID) ([]*node.Node, error) {
	return nil, r.getAncestorsErr
}

func (r *stubNodeRepo) GetSubtree(_ context.Context, _ string, _ uuid.UUID, _ int) ([]*node.Node, error) {
	return nil, r.getSubtreeErr
}

func (r *stubNodeRepo) GetAtTime(_ context.Context, _, _ string, _ time.Time) (*node.Node, error) {
	return nil, node.ErrNotFound
}

func (r *stubNodeRepo) BulkCreate(_ context.Context, _ []*node.Node) error { return r.bulkCreateErr }

// newStubNodeService builds a NodeService backed by the supplied stub repo.
func newStubNodeService(t *testing.T, repo node.Repository) *handler.NodeService {
	t.Helper()
	svc, err := handler.NewNodeService(repo, nil)
	require.NoError(t, err)
	return svc
}

// nodeTenantCtx returns a context carrying a valid tenant ID.
func nodeTenantCtx(t *testing.T) context.Context {
	t.Helper()
	tid, err := tenant.NewTenantID("cov_tenant")
	require.NoError(t, err)
	return tenant.WithTenant(context.Background(), tid)
}

// errStub is an arbitrary unmapped repo error that maps to codes.Internal.
var errStub = errors.New("stub repo failure")

// --- CreateNode: ValidTo branch + repo error ---

func TestNodeService_CreateNode_WithValidToAndRepoError(t *testing.T) {
	repo := &stubNodeRepo{createErr: node.ErrAlreadyExists}
	svc := newStubNodeService(t, repo)
	ctx := nodeTenantCtx(t)

	from := time.Now()
	_, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType:  "region",
		ValidFrom: timestamppb.New(from),
		ValidTo:   timestamppb.New(from.Add(24 * time.Hour)),
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// --- UpdateNode branches ---

func TestNodeService_UpdateNode_MissingTenant(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.UpdateNode(context.Background(), &pb.UpdateNodeRequest{Id: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestNodeService_UpdateNode_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.UpdateNode(nodeTenantCtx(t), &pb.UpdateNodeRequest{Id: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_UpdateNode_GetByIDError(t *testing.T) {
	repo := &stubNodeRepo{getByIDErr: node.ErrNotFound}
	svc := newStubNodeService(t, repo)

	_, err := svc.UpdateNode(nodeTenantCtx(t), &pb.UpdateNodeRequest{Id: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestNodeService_UpdateNode_RepoUpdateError(t *testing.T) {
	id := uuid.New()
	existing := &node.Node{ID: id, TenantID: "cov_tenant", NodeType: "region", Version: 3}
	repo := &stubNodeRepo{getByIDNode: existing, updateErr: node.ErrOptimisticLock}
	svc := newStubNodeService(t, repo)

	// Version matches existing so we pass the optimistic-lock pre-check and reach
	// repo.Update, which returns ErrOptimisticLock -> Aborted.
	_, err := svc.UpdateNode(nodeTenantCtx(t), &pb.UpdateNodeRequest{
		Id:      id.String(),
		Version: 3,
		ValidTo: timestamppb.New(time.Now().Add(time.Hour)),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

func TestNodeService_UpdateNode_Success(t *testing.T) {
	id := uuid.New()
	existing := &node.Node{
		ID:        id,
		TenantID:  "cov_tenant",
		NodeType:  "region",
		Version:   1,
		ValidFrom: time.Now(),
		// A time.Time attribute drives the time-to-string conversion branch in
		// mapToStruct (via domainNodeToProto) on the response path.
		Attributes: map[string]any{"measured_at": time.Now()},
	}
	repo := &stubNodeRepo{getByIDNode: existing}
	svc := newStubNodeService(t, repo)

	resp, err := svc.UpdateNode(nodeTenantCtx(t), &pb.UpdateNodeRequest{
		Id:      id.String(),
		Version: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, id.String(), resp.GetNode().GetId())
	// Attributes round-tripped: the time.Time became an RFC3339 string.
	assert.NotNil(t, resp.GetNode().GetAttributes())
}

// --- GetChildren branches ---

func TestNodeService_GetChildren_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetChildren(nodeTenantCtx(t), &pb.GetChildrenRequest{ParentId: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_GetChildren_RepoError(t *testing.T) {
	repo := &stubNodeRepo{getChildrenErr: errStub}
	svc := newStubNodeService(t, repo)

	_, err := svc.GetChildren(nodeTenantCtx(t), &pb.GetChildrenRequest{ParentId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- GetAncestors branches ---

func TestNodeService_GetAncestors_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetAncestors(nodeTenantCtx(t), &pb.GetAncestorsRequest{NodeId: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_GetAncestors_RepoError(t *testing.T) {
	repo := &stubNodeRepo{getAncestorsErr: node.ErrMaxDepthExceeded}
	svc := newStubNodeService(t, repo)

	_, err := svc.GetAncestors(nodeTenantCtx(t), &pb.GetAncestorsRequest{NodeId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// --- GetSubtree branches ---

func TestNodeService_GetSubtree_MissingTenant(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetSubtree(context.Background(), &pb.GetSubtreeRequest{RootId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestNodeService_GetSubtree_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetSubtree(nodeTenantCtx(t), &pb.GetSubtreeRequest{RootId: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_GetSubtree_DefaultDepthAndRepoError(t *testing.T) {
	repo := &stubNodeRepo{getSubtreeErr: errStub}
	svc := newStubNodeService(t, repo)

	// MaxDepth omitted (0) exercises the defaultMaxDepth assignment, then the
	// repo error path maps to Internal.
	_, err := svc.GetSubtree(nodeTenantCtx(t), &pb.GetSubtreeRequest{RootId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestNodeService_GetSubtree_DefaultDepthSuccess(t *testing.T) {
	repo := &stubNodeRepo{}
	svc := newStubNodeService(t, repo)

	// MaxDepth omitted -> defaultMaxDepth; empty result is a valid success.
	resp, err := svc.GetSubtree(nodeTenantCtx(t), &pb.GetSubtreeRequest{RootId: uuid.New().String()})
	require.NoError(t, err)
	assert.Empty(t, resp.GetNodes())
}

// --- GetNodeAsAt branches ---

func TestNodeService_GetNodeAsAt_MissingTenant(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetNodeAsAt(context.Background(), &pb.GetNodeAsAtRequest{
		NodeId: uuid.New().String(),
		AsAt:   timestamppb.New(time.Now()),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestNodeService_GetNodeAsAt_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetNodeAsAt(nodeTenantCtx(t), &pb.GetNodeAsAtRequest{
		NodeId: "not-a-uuid",
		AsAt:   timestamppb.New(time.Now()),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_GetNodeAsAt_Success(t *testing.T) {
	id := uuid.New()
	repo := &stubNodeRepo{getAsAtNode: &node.Node{
		ID:        id,
		TenantID:  "cov_tenant",
		NodeType:  "region",
		Version:   2,
		ValidFrom: time.Now(),
	}}
	svc := newStubNodeService(t, repo)

	resp, err := svc.GetNodeAsAt(nodeTenantCtx(t), &pb.GetNodeAsAtRequest{
		NodeId: id.String(),
		AsAt:   timestamppb.New(time.Now()),
	})
	require.NoError(t, err)
	assert.Equal(t, id.String(), resp.GetNode().GetId())
	assert.Equal(t, "region", resp.GetNode().GetNodeType())
}

func TestNodeService_GetNodeAsAt_NotFound(t *testing.T) {
	id := uuid.New()
	repo := &stubNodeRepo{getAsAtErr: node.ErrNotFound}
	svc := newStubNodeService(t, repo)

	_, err := svc.GetNodeAsAt(nodeTenantCtx(t), &pb.GetNodeAsAtRequest{
		NodeId: id.String(),
		AsAt:   timestamppb.New(time.Now()),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// --- GetNodeHistory branches ---

func TestNodeService_GetNodeHistory_MissingTenant(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetNodeHistory(context.Background(), &pb.GetNodeHistoryRequest{NodeId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestNodeService_GetNodeHistory_InvalidUUID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.GetNodeHistory(nodeTenantCtx(t), &pb.GetNodeHistoryRequest{NodeId: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_GetNodeHistory_RepoError(t *testing.T) {
	repo := &stubNodeRepo{getHistoryErr: errStub}
	svc := newStubNodeService(t, repo)

	_, err := svc.GetNodeHistory(nodeTenantCtx(t), &pb.GetNodeHistoryRequest{NodeId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- ImportNodes branches ---

func TestNodeService_ImportNodes_InvalidParentID(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	_, err := svc.ImportNodes(nodeTenantCtx(t), &pb.ImportNodesRequest{
		Nodes: []*pb.CreateNodeRequest{
			{NodeType: "region", ParentId: "not-a-uuid"},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_ImportNodes_BuildError(t *testing.T) {
	svc := newStubNodeService(t, &stubNodeRepo{})

	from := time.Now()
	// valid_to before valid_from trips builder validation -> InvalidArgument.
	_, err := svc.ImportNodes(nodeTenantCtx(t), &pb.ImportNodesRequest{
		Nodes: []*pb.CreateNodeRequest{
			{
				NodeType:  "region",
				ValidFrom: timestamppb.New(from),
				ValidTo:   timestamppb.New(from.Add(-time.Hour)),
			},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNodeService_ImportNodes_BulkCreateError(t *testing.T) {
	repo := &stubNodeRepo{bulkCreateErr: errStub}
	svc := newStubNodeService(t, repo)

	from := time.Now()
	parentID := uuid.New()
	_, err := svc.ImportNodes(nodeTenantCtx(t), &pb.ImportNodesRequest{
		Nodes: []*pb.CreateNodeRequest{
			{
				NodeType:  "region",
				ParentId:  parentID.String(),
				ValidFrom: timestamppb.New(from),
				ValidTo:   timestamppb.New(from.Add(time.Hour)),
			},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestNodeService_ImportNodes_Success(t *testing.T) {
	repo := &stubNodeRepo{}
	svc := newStubNodeService(t, repo)

	resp, err := svc.ImportNodes(nodeTenantCtx(t), &pb.ImportNodesRequest{
		Nodes: []*pb.CreateNodeRequest{
			{NodeType: "region"},
			{NodeType: "zone"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.GetImportedCount())
	assert.Len(t, resp.GetNodes(), 2)
}
