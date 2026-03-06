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
}

// ErrDBRequired is returned when DB is nil during service registration.
var ErrDBRequired = errors.New("manifest history service: database connection is required")

// RegisterManifestHistoryService creates and registers the ManifestHistoryService
// on the given gRPC server.
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

	handler, err := manifest.NewHistoryHandler(historySvc, cfg.Logger)
	if err != nil {
		return fmt.Errorf("manifest history handler: %w", err)
	}

	controlplanev1.RegisterManifestHistoryServiceServer(server, handler)
	return nil
}
