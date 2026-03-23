package manifest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrHistoryServiceRequired is returned when history service is nil.
var ErrHistoryServiceRequired = errors.New("history service is required")

// Applier is the interface for applying manifests via the standard pipeline.
// This decouples the history handler from the applier package to avoid import cycles.
type Applier interface {
	ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error)
}

// HistoryHandler implements the ManifestHistoryService gRPC interface.
type HistoryHandler struct {
	controlplanev1.UnimplementedManifestHistoryServiceServer

	history    *HistoryService
	exporter   *ExportService
	reconciler *ReconcileService
	applier    Applier
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

// SetApplier configures the Applier for rollback support.
// When nil, the RollbackManifest RPC returns Unimplemented.
func (h *HistoryHandler) SetApplier(applier Applier) {
	h.applier = applier
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

// RollbackManifest reverts the tenant's manifest to a previous version by
// re-applying the target version's content through the standard applier pipeline.
// A new version record is created (forward-only audit trail).
func (h *HistoryHandler) RollbackManifest(
	ctx context.Context,
	req *controlplanev1.RollbackManifestRequest,
) (*controlplanev1.RollbackManifestResponse, error) {
	if h.applier == nil {
		return nil, status.Error(codes.Unimplemented, "rollback not configured: applier not available")
	}
	if req.GetTargetSequenceNumber() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "target_sequence_number must be greater than 0")
	}
	if req.GetAppliedBy() == "" {
		return nil, status.Error(codes.InvalidArgument, "applied_by is required")
	}

	logger := h.logger.With(
		"target_sequence", req.GetTargetSequenceNumber(),
		"applied_by", req.GetAppliedBy(),
		"dry_run", req.GetDryRun(),
	)

	// Look up the target version by sequence number.
	targetEntity, err := h.history.repo.GetBySequenceNumber(ctx, req.GetTargetSequenceNumber())
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, status.Errorf(codes.NotFound, "manifest version with sequence number %d not found", req.GetTargetSequenceNumber())
		}
		logger.Error("failed to get target version", "error", err)
		return nil, status.Error(codes.Internal, "failed to get target version")
	}

	// Unmarshal the target manifest.
	targetManifest, err := unmarshalManifest(targetEntity.ManifestJSON)
	if err != nil {
		logger.Error("failed to unmarshal target manifest", "error", err)
		return nil, status.Error(codes.Internal, "failed to unmarshal target manifest")
	}

	// Generate diff between current and target for preview.
	currentSeq, err := h.history.repo.GetCurrentSequenceNumber(ctx)
	if err != nil {
		logger.Error("failed to get current sequence number", "error", err)
		return nil, status.Error(codes.Internal, "failed to get current sequence number")
	}

	var diffResp *controlplanev1.DiffManifestVersionsResponse
	if currentSeq > 0 && currentSeq != req.GetTargetSequenceNumber() {
		plan, baseSeq, targetSeq, diffErr := h.history.DiffVersionsBySequence(ctx, req.GetTargetSequenceNumber(), currentSeq)
		if diffErr != nil {
			logger.Warn("failed to generate rollback diff", "error", diffErr)
		} else {
			diffResp = &controlplanev1.DiffManifestVersionsResponse{
				BaseSequenceNumber:   baseSeq,
				TargetSequenceNumber: targetSeq,
				Actions:              diffPlanToProtoActions(plan),
				Summary:              diffPlanToProtoSummary(plan),
			}
		}
	}

	// If no changes, report NO_CHANGE.
	if currentSeq == req.GetTargetSequenceNumber() {
		return &controlplanev1.RollbackManifestResponse{
			Diff:    diffResp,
			Status:  controlplanev1.RollbackStatus_ROLLBACK_STATUS_NO_CHANGE,
			Message: "target version is already the current version",
		}, nil
	}

	// Dry run: return diff without applying.
	if req.GetDryRun() {
		logger.Info("dry run rollback preview")
		return &controlplanev1.RollbackManifestResponse{
			Diff:    diffResp,
			Status:  controlplanev1.RollbackStatus_ROLLBACK_STATUS_DRY_RUN,
			Message: fmt.Sprintf("dry run: would rollback to sequence %d (version %s)", req.GetTargetSequenceNumber(), targetEntity.Version),
		}, nil
	}

	// Apply the target manifest through the standard pipeline.
	logger.Info("applying rollback manifest")
	applyResp, err := h.applier.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  targetManifest,
		AppliedBy: fmt.Sprintf("rollback:%s", req.GetAppliedBy()),
		Force:     true, // Rollbacks may involve deletions from the current state
	})
	if err != nil {
		logger.Error("rollback apply failed", "error", err)
		return &controlplanev1.RollbackManifestResponse{
			Diff:    diffResp,
			Status:  controlplanev1.RollbackStatus_ROLLBACK_STATUS_FAILED,
			Message: fmt.Sprintf("rollback failed: %v", err),
		}, nil
	}

	if applyResp.Status != controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED {
		logger.Warn("rollback apply did not succeed", "apply_status", applyResp.Status.String())
		return &controlplanev1.RollbackManifestResponse{
			Diff:    diffResp,
			Status:  controlplanev1.RollbackStatus_ROLLBACK_STATUS_FAILED,
			Message: fmt.Sprintf("rollback apply returned status %s", applyResp.Status.String()),
		}, nil
	}

	// Return the snapshot from the apply response as the new version.
	logger.Info("rollback completed successfully", "new_sequence", applyResp.GetSequenceNumber())
	return &controlplanev1.RollbackManifestResponse{
		Version: applyResp.Snapshot,
		Diff:    diffResp,
		Status:  controlplanev1.RollbackStatus_ROLLBACK_STATUS_COMPLETED,
		Message: fmt.Sprintf("rolled back to sequence %d (version %s)", req.GetTargetSequenceNumber(), targetEntity.Version),
	}, nil
}
