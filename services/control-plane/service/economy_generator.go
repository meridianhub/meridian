package service

import (
	"fmt"
	"io/fs"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/grpc"
)

// EconomyGeneratorConfig holds configuration for RegisterEconomyGeneratorService.
type EconomyGeneratorConfig struct {
	// SchemaRegistry provides handler definitions for context assembly (required).
	SchemaRegistry *schema.Registry

	// ManifestHistory provides access to the current applied manifest for include_current_economy
	// requests. May be nil if that feature is not needed.
	ManifestHistory generator.ManifestHistorian

	// CookbookFS provides the cookbook pattern files for pattern matching.
	// May be nil if pattern matching is not required.
	CookbookFS fs.FS

	// LLMClient is used to generate and fix manifests. Required for GenerateManifest.
	// May be nil if only GetGenerationContext is used.
	LLMClient generator.LLMClient

	// Validator validates manifest YAML in the validate-fix loop. Required for GenerateManifest.
	// May be nil if only GetGenerationContext is used.
	Validator generator.ManifestValidator

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger
}

// RegisterEconomyGeneratorService creates and registers the EconomyGeneratorService on the
// given gRPC server. Validation of required fields is delegated to generator.NewGeneratorService.
func RegisterEconomyGeneratorService(server *grpc.Server, cfg EconomyGeneratorConfig) error {
	opts := []generator.ServiceOption{}
	if cfg.LLMClient != nil {
		opts = append(opts, generator.WithLLMClient(cfg.LLMClient))
	}
	if cfg.Validator != nil {
		opts = append(opts, generator.WithValidator(cfg.Validator))
	}

	svc, err := generator.NewGeneratorService(cfg.SchemaRegistry, cfg.ManifestHistory, cfg.CookbookFS, cfg.Logger, opts...)
	if err != nil {
		return fmt.Errorf("create economy generator service: %w", err)
	}

	controlplanev1.RegisterEconomyGeneratorServiceServer(server, svc)
	return nil
}
