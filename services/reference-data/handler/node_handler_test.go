package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

func setupNodeTestRepo(t *testing.T) (*node.PostgresRepository, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.NewTestPool(t)
	repo := node.NewPostgresRepository(pool)
	return repo, pool
}

func setupNodeTenantCtx(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func setupNodeService(t *testing.T) (*handler.NodeService, *pgxpool.Pool) {
	t.Helper()
	repo, pool := setupNodeTestRepo(t)
	svc, err := handler.NewNodeService(repo, nil)
	require.NoError(t, err)
	return svc, pool
}

func TestNewNodeService(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo, _ := setupNodeTestRepo(t)
		svc, err := handler.NewNodeService(repo, nil)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("nil repository", func(t *testing.T) {
		_, err := handler.NewNodeService(nil, nil)
		assert.ErrorIs(t, err, handler.ErrNodeRepositoryNil)
	})
}

func TestNodeService_CRUDFlow(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_crud")

	// Create a root node
	createResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "region",
		Attributes: mustStruct(t, map[string]any{
			"name":    "UK South",
			"country": "GB",
		}),
	})
	require.NoError(t, err)
	require.NotNil(t, createResp.Node)

	nodeID := createResp.Node.Id
	assert.NotEmpty(t, nodeID)
	assert.Equal(t, "region", createResp.Node.NodeType)
	assert.Equal(t, int64(1), createResp.Node.Version)
	assert.Contains(t, createResp.Node.ResolutionKey, "region:")
	assert.NotNil(t, createResp.Node.Attributes)

	// Get the node back
	getResp, err := svc.GetNode(ctx, &pb.GetNodeRequest{Id: nodeID})
	require.NoError(t, err)
	assert.Equal(t, nodeID, getResp.Node.Id)
	assert.Equal(t, "region", getResp.Node.NodeType)

	// Update the node
	updateResp, err := svc.UpdateNode(ctx, &pb.UpdateNodeRequest{
		Id:      nodeID,
		Version: 1,
		Attributes: mustStruct(t, map[string]any{
			"name":    "UK South Updated",
			"country": "GB",
		}),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updateResp.Node.Version)

	// Verify update persisted
	getResp2, err := svc.GetNode(ctx, &pb.GetNodeRequest{Id: nodeID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), getResp2.Node.Version)
	attrs := getResp2.Node.Attributes.AsMap()
	assert.Equal(t, "UK South Updated", attrs["name"])
}

func TestNodeService_ThreeLevelHierarchy(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_hierarchy")

	// Create root: dno
	dnoResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "dno",
		Attributes: mustStruct(t, map[string]any{
			"name": "Western Power Distribution",
		}),
	})
	require.NoError(t, err)
	dnoID := dnoResp.Node.Id

	// Create mid-level: gsp
	gspResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "gsp",
		ParentId: dnoID,
		Attributes: mustStruct(t, map[string]any{
			"name": "Grid Supply Point A",
		}),
	})
	require.NoError(t, err)
	gspID := gspResp.Node.Id

	// Create leaf: meter
	meterResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "meter",
		ParentId: gspID,
		Attributes: mustStruct(t, map[string]any{
			"serial": "MTR-001",
		}),
	})
	require.NoError(t, err)
	meterID := meterResp.Node.Id

	// Verify resolution key contains full path
	assert.Contains(t, meterResp.Node.ResolutionKey, "dno:")
	assert.Contains(t, meterResp.Node.ResolutionKey, "gsp:")
	assert.Contains(t, meterResp.Node.ResolutionKey, "meter:")

	// Get children of dno
	childrenResp, err := svc.GetChildren(ctx, &pb.GetChildrenRequest{
		ParentId:   dnoID,
		ActiveOnly: true,
	})
	require.NoError(t, err)
	assert.Len(t, childrenResp.Nodes, 1)
	assert.Equal(t, gspID, childrenResp.Nodes[0].Id)

	// Get children of gsp
	grandChildrenResp, err := svc.GetChildren(ctx, &pb.GetChildrenRequest{
		ParentId:   gspID,
		ActiveOnly: true,
	})
	require.NoError(t, err)
	assert.Len(t, grandChildrenResp.Nodes, 1)
	assert.Equal(t, meterID, grandChildrenResp.Nodes[0].Id)

	// Get subtree from dno - should include all 3 levels
	subtreeResp, err := svc.GetSubtree(ctx, &pb.GetSubtreeRequest{
		RootId:   dnoID,
		MaxDepth: 10,
	})
	require.NoError(t, err)
	assert.Len(t, subtreeResp.Nodes, 3) // dno, gsp, meter
}

func TestNodeService_GetAncestorsOnLeaf(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_ancestors")

	// Build 3-level hierarchy
	rootResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{NodeType: "region"})
	require.NoError(t, err)

	midResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "zone",
		ParentId: rootResp.Node.Id,
	})
	require.NoError(t, err)

	leafResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "meter",
		ParentId: midResp.Node.Id,
	})
	require.NoError(t, err)

	// Get ancestors of leaf
	ancestorsResp, err := svc.GetAncestors(ctx, &pb.GetAncestorsRequest{
		NodeId: leafResp.Node.Id,
	})
	require.NoError(t, err)
	assert.Len(t, ancestorsResp.Ancestors, 2)
	// Ancestors are ordered parent-first: [zone, region]
	assert.Equal(t, midResp.Node.Id, ancestorsResp.Ancestors[0].Id)
	assert.Equal(t, rootResp.Node.Id, ancestorsResp.Ancestors[1].Id)

	// Root has no ancestors
	rootAncestors, err := svc.GetAncestors(ctx, &pb.GetAncestorsRequest{
		NodeId: rootResp.Node.Id,
	})
	require.NoError(t, err)
	assert.Empty(t, rootAncestors.Ancestors)
}

func TestNodeService_BiTemporalGetNodeAsAt(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_bitemporal")

	now := time.Now()
	pastTime := now.Add(-24 * time.Hour)
	futureTime := now.Add(24 * time.Hour)

	// Create a node valid from the past
	createResp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType:  "meter",
		ValidFrom: timestamppb.New(pastTime),
		Attributes: mustStruct(t, map[string]any{
			"reading": "initial",
		}),
	})
	require.NoError(t, err)
	nodeID := createResp.Node.Id

	// Query at current time - should find the node
	asAtResp, err := svc.GetNodeAsAt(ctx, &pb.GetNodeAsAtRequest{
		NodeId: nodeID,
		AsAt:   timestamppb.New(now),
	})
	require.NoError(t, err)
	assert.Equal(t, nodeID, asAtResp.Node.Id)

	// Query before valid_from - should NOT find the node
	_, err = svc.GetNodeAsAt(ctx, &pb.GetNodeAsAtRequest{
		NodeId: nodeID,
		AsAt:   timestamppb.New(pastTime.Add(-48 * time.Hour)),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())

	// Close the node by setting valid_to
	_, err = svc.UpdateNode(ctx, &pb.UpdateNodeRequest{
		Id:      nodeID,
		Version: 1,
		ValidTo: timestamppb.New(futureTime),
	})
	require.NoError(t, err)

	// Query after valid_to - should NOT find it
	_, err = svc.GetNodeAsAt(ctx, &pb.GetNodeAsAtRequest{
		NodeId: nodeID,
		AsAt:   timestamppb.New(futureTime.Add(24 * time.Hour)),
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNodeService_ImportNodes(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_import")

	// Build a batch of 100 nodes: 10 regions, each with 9 zones
	requests := make([]*pb.CreateNodeRequest, 0, 100)

	// Create 10 root nodes first (we'll use their returned IDs for children)
	rootIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		resp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
			NodeType: "region",
			Attributes: mustStruct(t, map[string]any{
				"index": float64(i),
			}),
		})
		require.NoError(t, err)
		rootIDs[i] = resp.Node.Id
	}

	// Build 90 zone children, 9 per region
	for i := 0; i < 10; i++ {
		for j := 0; j < 9; j++ {
			requests = append(requests, &pb.CreateNodeRequest{
				NodeType: "zone",
				ParentId: rootIDs[i],
				Attributes: mustStruct(t, map[string]any{
					"region_idx": float64(i),
					"zone_idx":   float64(j),
				}),
			})
		}
	}

	importResp, err := svc.ImportNodes(ctx, &pb.ImportNodesRequest{
		Nodes: requests,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(90), importResp.ImportedCount)
	assert.Len(t, importResp.Nodes, 90)

	// Verify each imported node has a resolution key
	for _, n := range importResp.Nodes {
		assert.NotEmpty(t, n.ResolutionKey)
		assert.Contains(t, n.ResolutionKey, "zone:")
	}

	// Verify tree via GetChildren
	childrenResp, err := svc.GetChildren(ctx, &pb.GetChildrenRequest{
		ParentId:   rootIDs[0],
		ActiveOnly: true,
	})
	require.NoError(t, err)
	assert.Len(t, childrenResp.Nodes, 9)
}

func TestNodeService_TenantIsolation(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctxA := setupNodeTenantCtx(t, pool, "tenant_a")
	ctxB := setupNodeTenantCtx(t, pool, "tenant_b")

	// Create node in tenant A
	respA, err := svc.CreateNode(ctxA, &pb.CreateNodeRequest{
		NodeType: "region",
		Attributes: mustStruct(t, map[string]any{
			"name": "Tenant A Region",
		}),
	})
	require.NoError(t, err)

	// Create node in tenant B
	_, err = svc.CreateNode(ctxB, &pb.CreateNodeRequest{
		NodeType: "region",
		Attributes: mustStruct(t, map[string]any{
			"name": "Tenant B Region",
		}),
	})
	require.NoError(t, err)

	// Tenant B cannot see tenant A's node
	_, err = svc.GetNode(ctxB, &pb.GetNodeRequest{Id: respA.Node.Id})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNodeService_ValidationErrors(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_validation")

	t.Run("missing node type", func(t *testing.T) {
		_, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
			NodeType: "",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("invalid parent_id", func(t *testing.T) {
		_, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
			NodeType: "region",
			ParentId: "not-a-uuid",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("non-existent parent", func(t *testing.T) {
		_, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
			NodeType: "zone",
			ParentId: uuid.New().String(),
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("get non-existent node", func(t *testing.T) {
		_, err := svc.GetNode(ctx, &pb.GetNodeRequest{
			Id: uuid.New().String(),
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("invalid node id format", func(t *testing.T) {
		_, err := svc.GetNode(ctx, &pb.GetNodeRequest{
			Id: "not-a-uuid",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("optimistic lock conflict", func(t *testing.T) {
		resp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{NodeType: "region"})
		require.NoError(t, err)

		_, err = svc.UpdateNode(ctx, &pb.UpdateNodeRequest{
			Id:      resp.Node.Id,
			Version: 999, // Wrong version
			Attributes: mustStruct(t, map[string]any{
				"updated": true,
			}),
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Aborted, st.Code())
	})
}

func TestNodeService_MissingTenantContext(t *testing.T) {
	svc, _ := setupNodeService(t)
	// Use background context without tenant
	ctx := context.Background()

	t.Run("create without tenant", func(t *testing.T) {
		_, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{NodeType: "region"})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("get without tenant", func(t *testing.T) {
		_, err := svc.GetNode(ctx, &pb.GetNodeRequest{Id: uuid.New().String()})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("get children without tenant", func(t *testing.T) {
		_, err := svc.GetChildren(ctx, &pb.GetChildrenRequest{ParentId: uuid.New().String()})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("get ancestors without tenant", func(t *testing.T) {
		_, err := svc.GetAncestors(ctx, &pb.GetAncestorsRequest{NodeId: uuid.New().String()})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("import without tenant", func(t *testing.T) {
		_, err := svc.ImportNodes(ctx, &pb.ImportNodesRequest{
			Nodes: []*pb.CreateNodeRequest{{NodeType: "region"}},
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})
}

func TestNodeService_GetNodeHistory(t *testing.T) {
	svc, pool := setupNodeService(t)
	ctx := setupNodeTenantCtx(t, pool, "test_history")

	// Create initial version
	resp, err := svc.CreateNode(ctx, &pb.CreateNodeRequest{
		NodeType: "meter",
		Attributes: mustStruct(t, map[string]any{
			"reading": "v1",
		}),
	})
	require.NoError(t, err)

	// Get history - should have 1 version
	histResp, err := svc.GetNodeHistory(ctx, &pb.GetNodeHistoryRequest{
		NodeId: resp.Node.Id,
	})
	require.NoError(t, err)
	assert.Len(t, histResp.Versions, 1)
	assert.Equal(t, resp.Node.Id, histResp.Versions[0].Id)
}

// mustStruct creates a structpb.Struct from a map, failing the test on error.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}
