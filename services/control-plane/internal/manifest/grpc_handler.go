package manifest

import (
	"context"
	"errors"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrHistoryServiceRequired is returned when history service is nil.
var ErrHistoryServiceRequired = errors.New("history service is required")

// HistoryHandler implements the ManifestHistoryService gRPC interface.
type HistoryHandler struct {
	controlplanev1.UnimplementedManifestHistoryServiceServer

	history    *HistoryService
	exporter   *ExportService
	reconciler *ReconcileService
	logger     *slog.Logger
}

// NewHistoryHandler creates a new HistoryHandler.
func NewHistoryHandler(history *HistoryService, logger *slog.Logger) (*HistoryHandler, error) {
	if history == nil {
		return nil, ErrHistoryServiceRequired
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &HistoryHandler{
		history: history,
		logger:  logger.With("component", "manifest_history_handler"),
	}, nil
}

// NewHistoryHandlerWithExport creates a HistoryHandler with export support.
// The exporter enables the ExportManifest RPC; when nil, the RPC returns Unimplemented.
func NewHistoryHandlerWithExport(history *HistoryService, exporter *ExportService, logger *slog.Logger) (*HistoryHandler, error) {
	h, err := NewHistoryHandler(history, logger)
	if err != nil {
		return nil, err
	}
	h.exporter = exporter
	return h, nil
}

// NewHistoryHandlerWithReconcile creates a HistoryHandler with export and reconcile support.
// The reconciler enables the ReconcileManifest RPC; when nil, the RPC returns Unimplemented.
func NewHistoryHandlerWithReconcile(history *HistoryService, exporter *ExportService, reconciler *ReconcileService, logger *slog.Logger) (*HistoryHandler, error) {
	h, err := NewHistoryHandlerWithExport(history, exporter, logger)
	if err != nil {
		return nil, err
	}
	h.reconciler = reconciler
	return h, nil
}

// GetCurrentManifest retrieves the most recently applied manifest for the tenant.
func (h *HistoryHandler) GetCurrentManifest(
	ctx context.Context,
	_ *controlplanev1.GetCurrentManifestRequest,
) (*controlplanev1.GetCurrentManifestResponse, error) {
	entity, err := h.history.GetCurrentManifest(ctx)
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Error(codes.NotFound, "no applied manifest found")
		}
		h.logger.Error("failed to get current manifest", "error", err)
		return nil, status.Error(codes.Internal, "failed to get current manifest")
	}

	version, err := EntityToProto(entity)
	if err != nil {
		h.logger.Error("failed to convert entity to proto", "error", err)
		return nil, status.Error(codes.Internal, "failed to convert manifest version")
	}

	return &controlplanev1.GetCurrentManifestResponse{Version: version}, nil
}

// GetManifestVersion retrieves a specific manifest version by its version string.
func (h *HistoryHandler) GetManifestVersion(
	ctx context.Context,
	req *controlplanev1.GetManifestVersionRequest,
) (*controlplanev1.GetManifestVersionResponse, error) {
	if req.GetVersion() == "" {
		return nil, status.Error(codes.InvalidArgument, "version is required")
	}

	entity, err := h.history.GetManifestVersion(ctx, req.GetVersion())
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Errorf(codes.NotFound, "manifest version %q not found", req.GetVersion())
		}
		h.logger.Error("failed to get manifest version", "version", req.GetVersion(), "error", err)
		return nil, status.Error(codes.Internal, "failed to get manifest version")
	}

	version, err := EntityToProto(entity)
	if err != nil {
		h.logger.Error("failed to convert entity to proto", "error", err)
		return nil, status.Error(codes.Internal, "failed to convert manifest version")
	}

	return &controlplanev1.GetManifestVersionResponse{Version: version}, nil
}

// ListManifestVersions returns a paginated list of manifest versions.
func (h *HistoryHandler) ListManifestVersions(
	ctx context.Context,
	req *controlplanev1.ListManifestVersionsRequest,
) (*controlplanev1.ListManifestVersionsResponse, error) {
	limit := int(req.GetLimit())
	offset := int(req.GetOffset())

	entities, totalCount, err := h.history.ListManifestVersions(ctx, limit, offset)
	if err != nil {
		h.logger.Error("failed to list manifest versions", "error", err)
		return nil, status.Error(codes.Internal, "failed to list manifest versions")
	}

	versions := make([]*controlplanev1.ManifestVersion, 0, len(entities))
	for i := range entities {
		v, err := EntityToProto(&entities[i])
		if err != nil {
			h.logger.Error("failed to convert entity to proto", "entity_id", entities[i].ID, "error", err)
			return nil, status.Error(codes.Internal, "failed to convert manifest version")
		}
		versions = append(versions, v)
	}

	return &controlplanev1.ListManifestVersionsResponse{
		Versions:   versions,
		TotalCount: int32(totalCount),
	}, nil
}

// DiffManifestVersions compares two manifest versions by sequence number and
// returns a structured diff with per-resource actions and summary statistics.
func (h *HistoryHandler) DiffManifestVersions(
	ctx context.Context,
	req *controlplanev1.DiffManifestVersionsRequest,
) (*controlplanev1.DiffManifestVersionsResponse, error) {
	if req.GetTargetSequenceNumber() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "target_sequence_number must be greater than 0")
	}

	plan, baseSeq, targetSeq, err := h.history.DiffVersionsBySequence(
		ctx, req.GetBaseSequenceNumber(), req.GetTargetSequenceNumber(),
	)
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Error(codes.NotFound, "manifest version not found")
		}
		h.logger.Error("failed to diff manifest versions",
			"base_seq", req.GetBaseSequenceNumber(),
			"target_seq", req.GetTargetSequenceNumber(),
			"error", err,
		)
		return nil, status.Error(codes.Internal, "failed to diff manifest versions")
	}

	return &controlplanev1.DiffManifestVersionsResponse{
		BaseSequenceNumber:   baseSeq,
		TargetSequenceNumber: targetSeq,
		Actions:              diffPlanToProtoActions(plan),
		Summary:              diffPlanToProtoSummary(plan),
	}, nil
}

// diffPlanToProtoActions converts differ.DiffPlan actions to proto DiffAction messages.
func diffPlanToProtoActions(plan *differ.DiffPlan) []*controlplanev1.DiffAction {
	actions := make([]*controlplanev1.DiffAction, 0, len(plan.Actions))
	for _, a := range plan.Actions {
		actions = append(actions, &controlplanev1.DiffAction{
			ResourceType: string(a.ResourceType),
			Action:       string(a.Action),
			ResourceCode: a.ResourceCode,
			Description:  a.Description,
			Breaking:     a.Breaking,
		})
	}
	return actions
}

// diffPlanToProtoSummary converts a differ.DiffPlan to a proto DiffSummary.
func diffPlanToProtoSummary(plan *differ.DiffPlan) *controlplanev1.DiffSummary {
	summary := &controlplanev1.DiffSummary{
		TotalActions:       int32(len(plan.Actions)),
		HasBreakingChanges: plan.HasBreakingChanges,
	}
	for _, a := range plan.Actions {
		switch a.Action {
		case differ.ActionCreate:
			summary.Creates++
		case differ.ActionUpdate:
			summary.Updates++
		case differ.ActionDelete:
			summary.Deletes++
		case differ.ActionNoChange:
			summary.NoChanges++
		}
	}
	return summary
}

// ExportManifest reconstructs a manifest from live service state.
func (h *HistoryHandler) ExportManifest(
	ctx context.Context,
	req *controlplanev1.ExportManifestRequest,
) (*controlplanev1.ExportManifestResponse, error) {
	if h.exporter == nil {
		return nil, status.Error(codes.Unimplemented, "export manifest not configured")
	}

	result, err := h.exporter.Export(ctx, req.GetIncludeSections(), req.GetManifestVersion())
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Error(codes.NotFound, "fallback manifest version not found")
		}
		h.logger.Error("failed to export manifest", "error", err)
		return nil, status.Error(codes.Internal, "failed to export manifest")
	}

	return result.ToProtoResponse(), nil
}

// ReconcileManifest compares a stored manifest against live service state
// and reports any drift as structured output.
func (h *HistoryHandler) ReconcileManifest(
	ctx context.Context,
	req *controlplanev1.ReconcileManifestRequest,
) (*controlplanev1.ReconcileManifestResponse, error) {
	if h.reconciler == nil {
		return nil, status.Error(codes.Unimplemented, "reconcile manifest not configured")
	}

	result, err := h.reconciler.Reconcile(ctx, req.GetVersion(), req.GetIncludeSections())
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Error(codes.NotFound, "manifest version not found")
		}
		h.logger.Error("failed to reconcile manifest",
			"version", req.GetVersion(),
			"error", err,
		)
		return nil, status.Error(codes.Internal, "failed to reconcile manifest")
	}

	return result.ToProtoResponse(), nil
}
