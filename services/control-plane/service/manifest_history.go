package service

import (
	"context"
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

	// Applier enables the RollbackManifest RPC by providing the manifest
	// application pipeline. When nil, RollbackManifest returns Unimplemented.
	Applier manifest.Applier
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

	if cfg.Applier != nil {
		handler.SetApplier(cfg.Applier)
	}

	controlplanev1.RegisterManifestHistoryServiceServer(server, handler)
	return nil
}

// SeedManifestVersion stores a manifest as the initial applied version in the
// tenant's manifest_versions table. Idempotent - returns (false, nil) if a
// manifest version already exists. The context must carry tenant identity via
// tenant.WithTenant. Returns (true, nil) when a new version was stored.
func SeedManifestVersion(ctx context.Context, db *gorm.DB, mf *controlplanev1.Manifest, appliedBy string) (bool, error) {
	if db == nil {
		return false, ErrDBRequired
	}

	repo, err := manifest.NewRepository(db)
	if err != nil {
		return false, fmt.Errorf("manifest repository: %w", err)
	}

	historySvc, err := manifest.NewHistoryService(repo)
	if err != nil {
		return false, fmt.Errorf("manifest history service: %w", err)
	}

	// Check if a manifest already exists - skip if so (idempotent)
	_, err = historySvc.GetCurrentManifest(ctx)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, manifest.ErrVersionNotFound) {
		return false, fmt.Errorf("check existing manifest: %w", err)
	}

	_, err = historySvc.StoreManifestVersion(ctx, mf, appliedBy, nil, manifest.ApplyStatusApplied, nil, 0)
	if err != nil {
		return false, fmt.Errorf("store manifest version: %w", err)
	}

	return true, nil
}
