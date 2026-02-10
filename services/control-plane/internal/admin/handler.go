package admin

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements the CausationVisualizerService gRPC service.
type Handler struct {
	controlplanev1.UnimplementedCausationVisualizerServiceServer
	visualizer *CausationVisualizer
	logger     *slog.Logger
}

// NewHandler creates a new CausationVisualizerService handler.
func NewHandler(visualizer *CausationVisualizer, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		visualizer: visualizer,
		logger:     logger,
	}
}

// GetCausationTreeForPosition traces a financial position log back to its
// originating saga and returns the full causation tree.
func (h *Handler) GetCausationTreeForPosition(
	ctx context.Context,
	req *controlplanev1.GetCausationTreeForPositionRequest,
) (*controlplanev1.GetCausationTreeForPositionResponse, error) {
	positionID, err := uuid.Parse(req.GetPositionId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid position_id: %v", err)
	}

	result, posInfo, err := h.visualizer.GetCausationTreeForPosition(ctx, positionID)
	if err != nil {
		return nil, h.mapError(err, "position", positionID)
	}

	return &controlplanev1.GetCausationTreeForPositionResponse{
		Tree:       treeNodeToProto(result.Tree),
		Depth:      int32(result.Depth),
		PositionId: positionID.String(),
		AccountId:  posInfo.AccountID,
		SagaId:     result.SagaID.String(),
	}, nil
}

// GetCausationTreeForTransaction traces a transaction back to its originating
// saga and returns the full causation tree.
func (h *Handler) GetCausationTreeForTransaction(
	ctx context.Context,
	req *controlplanev1.GetCausationTreeForTransactionRequest,
) (*controlplanev1.GetCausationTreeForTransactionResponse, error) {
	transactionID, err := uuid.Parse(req.GetTransactionId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transaction_id: %v", err)
	}

	result, err := h.visualizer.GetCausationTreeForTransaction(ctx, transactionID)
	if err != nil {
		return nil, h.mapError(err, "transaction", transactionID)
	}

	return &controlplanev1.GetCausationTreeForTransactionResponse{
		Tree:          treeNodeToProto(result.Tree),
		Depth:         int32(result.Depth),
		TransactionId: transactionID.String(),
		SagaId:        result.SagaID.String(),
	}, nil
}

// GetCausationTreeForEvent traces an event back through the saga execution
// chain and returns the full causation tree.
func (h *Handler) GetCausationTreeForEvent(
	ctx context.Context,
	req *controlplanev1.GetCausationTreeForEventRequest,
) (*controlplanev1.GetCausationTreeForEventResponse, error) {
	eventID, err := uuid.Parse(req.GetEventId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid event_id: %v", err)
	}

	result, err := h.visualizer.GetCausationTreeForEvent(ctx, eventID)
	if err != nil {
		return nil, h.mapError(err, "event", eventID)
	}

	return &controlplanev1.GetCausationTreeForEventResponse{
		Tree:    treeNodeToProto(result.Tree),
		Depth:   int32(result.Depth),
		EventId: eventID.String(),
		SagaId:  result.SagaID.String(),
	}, nil
}

// mapError converts domain errors to gRPC status errors.
func (h *Handler) mapError(err error, entityType string, entityID uuid.UUID) error {
	switch {
	case errors.Is(err, ErrNoSagaFound):
		return status.Errorf(codes.NotFound, "no saga found for %s: %s", entityType, entityID)
	case errors.Is(err, ErrCausationChainTooDeep):
		return status.Errorf(codes.NotFound, "causation chain too deep for %s: %s", entityType, entityID)
	case errors.Is(err, saga.ErrSagaNotFound):
		return status.Errorf(codes.NotFound, "saga not found for %s: %s", entityType, entityID)
	default:
		h.logger.Error("failed to trace causation tree",
			"entity_type", entityType,
			"entity_id", entityID,
			"error", err,
		)
		return status.Errorf(codes.Internal, "failed to trace causation tree for %s", entityType)
	}
}

// treeNodeToProto converts a domain CausationTreeNode to its proto representation.
func treeNodeToProto(node *saga.CausationTreeNode) *sagav1.CausationTreeNode {
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
		proto.Steps = append(proto.Steps, stepNodeToProto(&step))
	}

	return proto
}

// stepNodeToProto converts a domain StepNode to its proto representation.
func stepNodeToProto(step *saga.StepNode) *sagav1.StepNode {
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
		proto.ChildSagas = append(proto.ChildSagas, treeNodeToProto(child))
	}

	return proto
}
