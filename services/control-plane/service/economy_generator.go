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

// EconomyGeneratorConfig holds configuration for the EconomyGeneratorService.
type EconomyGeneratorConfig struct {
	// SchemaRegistry provides handler metadata for prompt assembly and error enrichment (required).
	SchemaRegistry *schema.Registry

	// CookbookFS is the filesystem containing cookbook patterns (required).
	CookbookFS fs.FS

	// LLMClient is used to generate and fix manifests (required).
	LLMClient generator.LLMClient

	// Validator validates manifest YAML in the validate-fix loop (required).
	Validator generator.ManifestValidator

	// ManifestClient retrieves current tenant manifests for amend mode (optional).
	ManifestClient generator.ManifestHistorian

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger
}

// RegisterEconomyGeneratorService creates and registers the EconomyGeneratorService
// on the given gRPC server. Validation of required fields is delegated to NewService.
func RegisterEconomyGeneratorService(server *grpc.Server, cfg EconomyGeneratorConfig) error {
	svc, err := generator.NewService(generator.Config{
		SchemaRegistry: cfg.SchemaRegistry,
		CookbookFS:     cfg.CookbookFS,
		LLMClient:      cfg.LLMClient,
		Validator:      cfg.Validator,
		ManifestClient: cfg.ManifestClient,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return fmt.Errorf("create generator service: %w", err)
	}

	controlplanev1.RegisterEconomyGeneratorServiceServer(server, svc)
	return nil
}
