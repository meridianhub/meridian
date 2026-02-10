package handler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ErrNodeRepositoryNil is returned when attempting to create a service with a nil repository.
var ErrNodeRepositoryNil = errors.New("node repository cannot be nil")

// Default subtree depth when max_depth is not specified.
const defaultMaxDepth = 10

// Prometheus metrics for node operations.
var (
	nodeOpsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "meridian",
		Subsystem: "reference_data_node",
		Name:      "operations_total",
		Help:      "Total number of node operations by type and status.",
	}, []string{"operation", "status"})

	nodeOpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "reference_data_node",
		Name:      "operation_duration_seconds",
		Help:      "Duration of node operations in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"operation"})

	nodeTraversalDepth = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "meridian",
		Subsystem: "reference_data_node",
		Name:      "traversal_depth",
		Help:      "Depth of tree traversal operations.",
		Buckets:   []float64{1, 2, 3, 5, 8, 10, 15, 20},
	}, []string{"operation"})
)

// NodeService implements the NodeService gRPC service.
type NodeService struct {
	pb.UnimplementedNodeServiceServer
	repo   node.Repository
	logger *slog.Logger
}

// NewNodeService creates a new node service.
func NewNodeService(repo node.Repository, logger *slog.Logger) (*NodeService, error) {
	if repo == nil {
		return nil, ErrNodeRepositoryNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &NodeService{
		repo:   repo,
		logger: logger,
	}, nil
}

// CreateNode creates a new reference data node.
func (s *NodeService) CreateNode(ctx context.Context, req *pb.CreateNodeRequest) (*pb.CreateNodeResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("create"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("create", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	builder := node.NewBuilder(tenantID.String()).WithNodeType(req.NodeType)

	if req.ParentId != "" {
		parentID, parseErr := uuid.Parse(req.ParentId)
		if parseErr != nil {
			nodeOpsTotal.WithLabelValues("create", "error").Inc()
			return nil, status.Errorf(codes.InvalidArgument, "invalid parent_id: %v", parseErr)
		}
		builder = builder.WithParentID(parentID)
	}

	if req.Attributes != nil {
		builder = builder.WithAttributes(structToMap(req.Attributes))
	}

	if req.ValidFrom != nil {
		builder = builder.WithValidFrom(req.ValidFrom.AsTime())
	}

	if req.ValidTo != nil {
		builder = builder.WithValidTo(req.ValidTo.AsTime())
	}

	n, buildErr := builder.Build()
	if buildErr != nil {
		nodeOpsTotal.WithLabelValues("create", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node: %v", buildErr)
	}

	if createErr := s.repo.Create(ctx, n); createErr != nil {
		nodeOpsTotal.WithLabelValues("create", "error").Inc()
		return nil, s.mapNodeError(createErr, "CreateNode", n.ID.String())
	}

	nodeOpsTotal.WithLabelValues("create", "ok").Inc()
	s.logger.Info("node created",
		"id", n.ID,
		"node_type", n.NodeType,
		"resolution_key", n.ResolutionKey)

	return &pb.CreateNodeResponse{
		Node: domainNodeToProto(n),
	}, nil
}

// UpdateNode modifies non-identity attributes of a node.
func (s *NodeService) UpdateNode(ctx context.Context, req *pb.UpdateNodeRequest) (*pb.UpdateNodeResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("update"))
	defer timer.ObserveDuration()

	_, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("update", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodeID, parseErr := uuid.Parse(req.Id)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("update", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node id: %v", parseErr)
	}

	// Fetch current node to prepare update
	existing, getErr := s.repo.GetByID(ctx, nodeID)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("update", "error").Inc()
		return nil, s.mapNodeError(getErr, "UpdateNode", req.Id)
	}

	// Verify version for optimistic locking
	if existing.Version != req.Version {
		nodeOpsTotal.WithLabelValues("update", "error").Inc()
		return nil, status.Errorf(codes.Aborted, "optimistic lock failure: client sent version %d, but current version is %d", req.Version, existing.Version)
	}

	// Apply updates
	if req.Attributes != nil {
		existing.Attributes = structToMap(req.Attributes)
	}
	if req.ValidTo != nil {
		vt := req.ValidTo.AsTime()
		existing.ValidTo = &vt
	}

	if updateErr := s.repo.Update(ctx, existing); updateErr != nil {
		nodeOpsTotal.WithLabelValues("update", "error").Inc()
		return nil, s.mapNodeError(updateErr, "UpdateNode", req.Id)
	}

	nodeOpsTotal.WithLabelValues("update", "ok").Inc()
	s.logger.Info("node updated",
		"id", existing.ID,
		"version", existing.Version)

	return &pb.UpdateNodeResponse{
		Node: domainNodeToProto(existing),
	}, nil
}

// GetNode retrieves a node by its UUID.
func (s *NodeService) GetNode(ctx context.Context, req *pb.GetNodeRequest) (*pb.GetNodeResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get"))
	defer timer.ObserveDuration()

	_, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodeID, parseErr := uuid.Parse(req.Id)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node id: %v", parseErr)
	}

	n, getErr := s.repo.GetByID(ctx, nodeID)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetNode", req.Id)
	}

	nodeOpsTotal.WithLabelValues("get", "ok").Inc()
	return &pb.GetNodeResponse{
		Node: domainNodeToProto(n),
	}, nil
}

// GetChildren returns child nodes of a given parent.
func (s *NodeService) GetChildren(ctx context.Context, req *pb.GetChildrenRequest) (*pb.GetChildrenResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get_children"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get_children", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	parentID, parseErr := uuid.Parse(req.ParentId)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get_children", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid parent_id: %v", parseErr)
	}

	children, getErr := s.repo.GetChildren(ctx, tenantID.String(), parentID, req.ActiveOnly)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get_children", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetChildren", req.ParentId)
	}

	nodeOpsTotal.WithLabelValues("get_children", "ok").Inc()
	return &pb.GetChildrenResponse{
		Nodes: domainNodesToProto(children),
	}, nil
}

// GetAncestors returns the chain of ancestors from the immediate parent to root.
func (s *NodeService) GetAncestors(ctx context.Context, req *pb.GetAncestorsRequest) (*pb.GetAncestorsResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get_ancestors"))
	defer timer.ObserveDuration()

	_, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get_ancestors", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodeID, parseErr := uuid.Parse(req.NodeId)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get_ancestors", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node_id: %v", parseErr)
	}

	ancestors, getErr := s.repo.GetAncestors(ctx, nodeID)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get_ancestors", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetAncestors", req.NodeId)
	}

	nodeTraversalDepth.WithLabelValues("get_ancestors").Observe(float64(len(ancestors)))
	nodeOpsTotal.WithLabelValues("get_ancestors", "ok").Inc()

	return &pb.GetAncestorsResponse{
		Ancestors: domainNodesToProto(ancestors),
	}, nil
}

// GetSubtree returns all descendants of a node up to a specified depth.
func (s *NodeService) GetSubtree(ctx context.Context, req *pb.GetSubtreeRequest) (*pb.GetSubtreeResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get_subtree"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get_subtree", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	rootID, parseErr := uuid.Parse(req.RootId)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get_subtree", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid root_id: %v", parseErr)
	}

	maxDepth := int(req.MaxDepth)
	if maxDepth == 0 {
		maxDepth = defaultMaxDepth
	}

	nodes, getErr := s.repo.GetSubtree(ctx, tenantID.String(), rootID, maxDepth)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get_subtree", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetSubtree", req.RootId)
	}

	nodeTraversalDepth.WithLabelValues("get_subtree").Observe(float64(maxDepth))
	nodeOpsTotal.WithLabelValues("get_subtree", "ok").Inc()

	return &pb.GetSubtreeResponse{
		Nodes: domainNodesToProto(nodes),
	}, nil
}

// GetNodeAsAt retrieves the version of a node that was valid at a given time.
func (s *NodeService) GetNodeAsAt(ctx context.Context, req *pb.GetNodeAsAtRequest) (*pb.GetNodeAsAtResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get_as_at"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get_as_at", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodeID, parseErr := uuid.Parse(req.NodeId)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get_as_at", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node_id: %v", parseErr)
	}

	asAt := req.AsAt.AsTime()
	n, getErr := s.repo.GetAsAt(ctx, tenantID.String(), nodeID, asAt)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get_as_at", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetNodeAsAt", req.NodeId)
	}

	nodeOpsTotal.WithLabelValues("get_as_at", "ok").Inc()
	return &pb.GetNodeAsAtResponse{
		Node: domainNodeToProto(n),
	}, nil
}

// GetNodeHistory returns all temporal versions of a node.
func (s *NodeService) GetNodeHistory(ctx context.Context, req *pb.GetNodeHistoryRequest) (*pb.GetNodeHistoryResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("get_history"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("get_history", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodeID, parseErr := uuid.Parse(req.NodeId)
	if parseErr != nil {
		nodeOpsTotal.WithLabelValues("get_history", "error").Inc()
		return nil, status.Errorf(codes.InvalidArgument, "invalid node_id: %v", parseErr)
	}

	versions, getErr := s.repo.GetHistory(ctx, tenantID.String(), nodeID)
	if getErr != nil {
		nodeOpsTotal.WithLabelValues("get_history", "error").Inc()
		return nil, s.mapNodeError(getErr, "GetNodeHistory", req.NodeId)
	}

	nodeOpsTotal.WithLabelValues("get_history", "ok").Inc()
	return &pb.GetNodeHistoryResponse{
		Versions: domainNodesToProto(versions),
	}, nil
}

// ImportNodes creates multiple nodes in a single batch transaction.
func (s *NodeService) ImportNodes(ctx context.Context, req *pb.ImportNodesRequest) (*pb.ImportNodesResponse, error) {
	timer := prometheus.NewTimer(nodeOpDuration.WithLabelValues("import"))
	defer timer.ObserveDuration()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		nodeOpsTotal.WithLabelValues("import", "error").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	nodes := make([]*node.Node, 0, len(req.Nodes))
	for i, createReq := range req.Nodes {
		builder := node.NewBuilder(tenantID.String()).WithNodeType(createReq.NodeType)

		if createReq.ParentId != "" {
			parentID, parseErr := uuid.Parse(createReq.ParentId)
			if parseErr != nil {
				nodeOpsTotal.WithLabelValues("import", "error").Inc()
				return nil, status.Errorf(codes.InvalidArgument, "invalid parent_id at index %d: %v", i, parseErr)
			}
			builder = builder.WithParentID(parentID)
		}

		if createReq.Attributes != nil {
			builder = builder.WithAttributes(structToMap(createReq.Attributes))
		}

		if createReq.ValidFrom != nil {
			builder = builder.WithValidFrom(createReq.ValidFrom.AsTime())
		}

		if createReq.ValidTo != nil {
			builder = builder.WithValidTo(createReq.ValidTo.AsTime())
		}

		n, buildErr := builder.Build()
		if buildErr != nil {
			nodeOpsTotal.WithLabelValues("import", "error").Inc()
			return nil, status.Errorf(codes.InvalidArgument, "invalid node at index %d: %v", i, buildErr)
		}
		nodes = append(nodes, n)
	}

	if bulkErr := s.repo.BulkCreate(ctx, nodes); bulkErr != nil {
		nodeOpsTotal.WithLabelValues("import", "error").Inc()
		return nil, s.mapNodeError(bulkErr, "ImportNodes", "batch")
	}

	nodeOpsTotal.WithLabelValues("import", "ok").Inc()
	s.logger.Info("nodes imported",
		"count", len(nodes))

	return &pb.ImportNodesResponse{
		ImportedCount: int32(len(nodes)),
		Nodes:         domainNodesToProto(nodes),
	}, nil
}

// mapNodeError converts domain errors to appropriate gRPC status codes.
func (s *NodeService) mapNodeError(err error, operation, id string) error {
	switch {
	case errors.Is(err, node.ErrNotFound):
		s.logger.Warn("node not found",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.NotFound, "node not found: %s", id)

	case errors.Is(err, node.ErrAlreadyExists):
		s.logger.Warn("node already exists",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.AlreadyExists, "active node with this resolution key already exists")

	case errors.Is(err, node.ErrParentNotFound):
		s.logger.Warn("parent node not found",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.NotFound, "parent node not found")

	case errors.Is(err, node.ErrParentNotActive):
		s.logger.Warn("parent node not active",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.FailedPrecondition, "parent node is not active")

	case errors.Is(err, node.ErrCrossTenantParent):
		s.logger.Warn("cross-tenant parent reference",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.PermissionDenied, "parent node belongs to a different tenant")

	case errors.Is(err, node.ErrImmutableIdentity):
		s.logger.Warn("immutable identity change attempted",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.FailedPrecondition, "cannot change identity fields on active node")

	case errors.Is(err, node.ErrOptimisticLock):
		s.logger.Warn("optimistic lock failure",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.Aborted, "node was modified by another transaction")

	case errors.Is(err, node.ErrCircularHierarchy):
		s.logger.Warn("circular hierarchy detected",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.FailedPrecondition, "circular hierarchy detected")

	case errors.Is(err, node.ErrMaxDepthExceeded):
		s.logger.Warn("max hierarchy depth exceeded",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.FailedPrecondition, "maximum hierarchy depth exceeded")

	case errors.Is(err, node.ErrInvalidTimeRange):
		s.logger.Warn("invalid time range",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.InvalidArgument, "valid_to must be after valid_from")

	case errors.Is(err, node.ErrAlreadySuperseded):
		s.logger.Warn("node already superseded",
			"operation", operation,
			"id", id)
		return status.Errorf(codes.FailedPrecondition, "node has already been superseded")

	case errors.Is(err, node.ErrInvalidNode):
		s.logger.Warn("invalid node",
			"operation", operation,
			"id", id,
			"error", err)
		return status.Errorf(codes.InvalidArgument, "invalid node: %v", err)

	case errors.Is(err, tenant.ErrMissingTenantContext):
		return status.Error(codes.Unauthenticated, "missing tenant context")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"id", id,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// domainNodeToProto converts a domain Node to proto ReferenceDataNode.
func domainNodeToProto(n *node.Node) *pb.ReferenceDataNode {
	if n == nil {
		return nil
	}

	proto := &pb.ReferenceDataNode{
		Id:            n.ID.String(),
		TenantId:      n.TenantID,
		NodeType:      n.NodeType,
		ResolutionKey: n.ResolutionKey,
		ValidFrom:     timestamppb.New(n.ValidFrom),
		CreatedAt:     timestamppb.New(n.CreatedAt),
		Version:       n.Version,
	}

	if n.ParentID != nil {
		proto.ParentId = n.ParentID.String()
	}

	if n.ValidTo != nil {
		proto.ValidTo = timestamppb.New(*n.ValidTo)
	}

	if n.Attributes != nil {
		attrs, err := mapToStruct(n.Attributes)
		if err != nil {
			slog.Warn("failed to convert node attributes to proto",
				"node_id", n.ID,
				"error", err)
		} else {
			proto.Attributes = attrs
		}
	}

	return proto
}

// domainNodesToProto converts a slice of domain Nodes to proto.
func domainNodesToProto(nodes []*node.Node) []*pb.ReferenceDataNode {
	result := make([]*pb.ReferenceDataNode, len(nodes))
	for i, n := range nodes {
		result[i] = domainNodeToProto(n)
	}
	return result
}

// structToMap converts a protobuf Struct to map[string]any.
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// mapToStruct converts map[string]any to a protobuf Struct.
func mapToStruct(m map[string]any) (*structpb.Struct, error) {
	if len(m) == 0 {
		return &structpb.Struct{Fields: map[string]*structpb.Value{}}, nil
	}
	// Convert any time.Time values to strings for structpb compatibility
	converted := make(map[string]any, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case time.Time:
			converted[k] = val.Format(time.RFC3339)
		default:
			converted[k] = val
		}
	}
	return structpb.NewStruct(converted)
}
