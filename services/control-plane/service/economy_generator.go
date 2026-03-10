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

// ErrGeneratorSchemaRegistryRequired is returned when SchemaRegistry is nil.
var ErrGeneratorSchemaRegistryRequired = errors.New("economy generator service: schema registry is required")

// ErrGeneratorCookbookFSRequired is returned when CookbookFS is nil.
var ErrGeneratorCookbookFSRequired = errors.New("economy generator service: cookbook FS is required")

// ErrGeneratorLLMClientRequired is returned when LLMClient is nil.
var ErrGeneratorLLMClientRequired = errors.New("economy generator service: LLM client is required")

// ErrGeneratorValidatorRequired is returned when Validator is nil.
var ErrGeneratorValidatorRequired = errors.New("economy generator service: validator is required")

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
// on the given gRPC server.
func RegisterEconomyGeneratorService(server *grpc.Server, cfg EconomyGeneratorConfig) error {
	if cfg.SchemaRegistry == nil {
		return ErrGeneratorSchemaRegistryRequired
	}
	if cfg.CookbookFS == nil {
		return ErrGeneratorCookbookFSRequired
	}
	if cfg.LLMClient == nil {
		return ErrGeneratorLLMClientRequired
	}
	if cfg.Validator == nil {
		return ErrGeneratorValidatorRequired
	}

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
