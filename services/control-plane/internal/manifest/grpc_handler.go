package manifest

import (
	"context"
	"errors"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrHistoryServiceRequired is returned when history service is nil.
var ErrHistoryServiceRequired = errors.New("history service is required")

// HistoryHandler implements the ManifestHistoryService gRPC interface.
type HistoryHandler struct {
	controlplanev1.UnimplementedManifestHistoryServiceServer

	history *HistoryService
	logger  *slog.Logger
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
