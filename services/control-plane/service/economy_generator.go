package service

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/grpc"
)

// EconomyGeneratorServiceConfig holds configuration for RegisterEconomyGeneratorService.
type EconomyGeneratorServiceConfig struct {
	// SchemaRegistry provides handler definitions for context assembly (required).
	SchemaRegistry *schema.Registry

	// ManifestHistory provides access to the current applied manifest for include_current_economy
	// requests. May be nil if that feature is not needed.
	ManifestHistory generator.ManifestHistorian

	// CookbookFS provides the cookbook pattern files for pattern matching.
	// May be nil if pattern matching is not required.
	CookbookFS fs.FS

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger
}

// ErrSchemaRegistryRequired is returned when SchemaRegistry is nil during service registration.
var ErrSchemaRegistryRequired = errors.New("economy generator service: schema registry is required")

// RegisterEconomyGeneratorService creates and registers the EconomyGeneratorService on the
// given gRPC server.
func RegisterEconomyGeneratorService(server *grpc.Server, cfg EconomyGeneratorServiceConfig) error {
	if cfg.SchemaRegistry == nil {
		return ErrSchemaRegistryRequired
	}

	svc, err := generator.NewGeneratorService(cfg.SchemaRegistry, cfg.ManifestHistory, cfg.CookbookFS, cfg.Logger)
	if err != nil {
		return fmt.Errorf("economy generator service: %w", err)
	}

	controlplanev1.RegisterEconomyGeneratorServiceServer(server, svc)
	return nil
}
