package service

import (
	"errors"
	"fmt"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ManifestHistoryServiceConfig holds the configuration for RegisterManifestHistoryService.
type ManifestHistoryServiceConfig struct {
	// DB is the GORM database connection (required).
	DB *gorm.DB

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger

	// ExportCollectors provides live service collectors for ExportManifest.
	// When nil, the ExportManifest RPC returns Unimplemented.
	ExportCollectors *manifest.ExportCollectors
}

// ErrDBRequired is returned when DB is nil during service registration.
var ErrDBRequired = errors.New("manifest history service: database connection is required")

// RegisterManifestHistoryService creates and registers the ManifestHistoryService
// on the given gRPC server. When ExportCollectors is provided, the ExportManifest
// RPC is enabled; otherwise it returns Unimplemented.
func RegisterManifestHistoryService(server *grpc.Server, cfg ManifestHistoryServiceConfig) error {
	if cfg.DB == nil {
		return ErrDBRequired
	}

	repo, err := manifest.NewRepository(cfg.DB)
	if err != nil {
		return fmt.Errorf("manifest repository: %w", err)
	}

	historySvc, err := manifest.NewHistoryService(repo)
	if err != nil {
		return fmt.Errorf("manifest history service: %w", err)
	}

	var handler *manifest.HistoryHandler
	if cfg.ExportCollectors != nil {
		exporter, exportErr := manifest.NewExportService(historySvc, cfg.ExportCollectors)
		if exportErr != nil {
			return fmt.Errorf("manifest export service: %w", exportErr)
		}
		reconciler, reconcileErr := manifest.NewReconcileService(historySvc, exporter, nil)
		if reconcileErr != nil {
			return fmt.Errorf("manifest reconcile service: %w", reconcileErr)
		}
		handler, err = manifest.NewHistoryHandlerWithReconcile(historySvc, exporter, reconciler, cfg.Logger)
	} else {
		handler, err = manifest.NewHistoryHandler(historySvc, cfg.Logger)
	}
	if err != nil {
		return fmt.Errorf("manifest history handler: %w", err)
	}

	controlplanev1.RegisterManifestHistoryServiceServer(server, handler)
	return nil
}
