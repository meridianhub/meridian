// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
)

// AdminHandler implements the SagaAdminService gRPC service.
// It provides debugging and visualization endpoints for saga instances.
type AdminHandler struct {
	sagav1.UnimplementedSagaAdminServiceServer
	treeRepo *CausationTreeRepository
	logger   *slog.Logger
}

// NewAdminHandler creates a new AdminHandler.
func NewAdminHandler(treeRepo *CausationTreeRepository, logger *slog.Logger) *AdminHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminHandler{
		treeRepo: treeRepo,
		logger:   logger,
	}
}

// GetCausationTree returns the full parent->child saga hierarchy for debugging.
func (h *AdminHandler) GetCausationTree(
	ctx context.Context,
	req *sagav1.GetCausationTreeRequest,
) (*sagav1.GetCausationTreeResponse, error) {
	sagaID, err := uuid.Parse(req.SagaId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid saga_id: %v", err)
	}

	h.logger.Debug("fetching causation tree",
		"saga_id", sagaID,
	)

	tree, err := h.treeRepo.GetCausationTree(ctx, sagaID)
	if err != nil {
		if errors.Is(err, ErrSagaNotFound) {
			return nil, status.Errorf(codes.NotFound, "saga not found: %s", sagaID)
		}
		h.logger.Error("failed to get causation tree",
			"saga_id", sagaID,
			"error", err,
		)
		return nil, status.Errorf(codes.Internal, "failed to retrieve causation tree")
	}

	// Get tree depth for observability
	depth, err := h.treeRepo.GetTreeDepth(ctx, sagaID)
	if err != nil {
		h.logger.Warn("failed to get tree depth",
			"saga_id", sagaID,
			"error", err,
		)
		// Don't fail the request for this
		depth = 0
	}

	h.logger.Info("causation tree retrieved",
		"saga_id", sagaID,
		"depth", depth,
	)

	return &sagav1.GetCausationTreeResponse{
		Tree:  h.treeNodeToProto(tree),
		Depth: int32(depth),
	}, nil
}

// treeNodeToProto converts a domain CausationTreeNode to proto.
func (h *AdminHandler) treeNodeToProto(node *CausationTreeNode) *sagav1.CausationTreeNode {
	if node == nil {
		return nil
	}

	proto := &sagav1.CausationTreeNode{
		SagaId:   node.SagaID.String(),
		SagaName: node.SagaName,
		Status:   node.Status,
		Steps:    make([]*sagav1.StepNode, 0, len(node.Steps)),
	}

	if node.EffectiveAt != nil {
		proto.EffectiveAt = timestamppb.New(*node.EffectiveAt)
	}
	if node.KnowledgeAt != nil {
		proto.KnowledgeAt = timestamppb.New(*node.KnowledgeAt)
	}
	if node.FailedStep != nil {
		proto.FailedStep = &sagav1.FailedStep{
			Index:         int32(node.FailedStep.Index),
			Error:         node.FailedStep.Error,
			ErrorCategory: node.FailedStep.ErrorCategory,
		}
	}

	for _, step := range node.Steps {
		proto.Steps = append(proto.Steps, h.stepNodeToProto(&step))
	}

	return proto
}

// stepNodeToProto converts a domain StepNode to proto.
func (h *AdminHandler) stepNodeToProto(step *StepNode) *sagav1.StepNode {
	if step == nil {
		return nil
	}

	proto := &sagav1.StepNode{
		Index:      int32(step.Index),
		Name:       step.Name,
		Status:     step.Status,
		ChildSagas: make([]*sagav1.CausationTreeNode, 0, len(step.ChildSagas)),
	}

	if step.ExecutedAt != nil {
		proto.ExecutedAt = timestamppb.New(*step.ExecutedAt)
	}
	if step.Error != nil {
		proto.Error = *step.Error
	}

	for _, child := range step.ChildSagas {
		proto.ChildSagas = append(proto.ChildSagas, h.treeNodeToProto(child))
	}

	return proto
}
