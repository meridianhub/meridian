package generator

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// ManifestHistorian provides access to the current applied manifest for a tenant.
type ManifestHistorian interface {
	GetCurrentManifest(ctx context.Context) (*controlplanev1.Manifest, error)
}

// Service implements the EconomyGeneratorServiceServer gRPC interface.
type Service struct {
	controlplanev1.UnimplementedEconomyGeneratorServiceServer

	schemaRegistry  *schema.Registry
	manifestHistory ManifestHistorian
	cookbookFS      fs.FS
	logger          *slog.Logger
}

// ErrNilSchemaRegistry is returned when a nil schema registry is provided.
var ErrNilSchemaRegistry = errors.New("schema registry is required")

// NewGeneratorService creates a new Service.
// manifestHistory may be nil if include_current_economy is never used.
func NewGeneratorService(
	registry *schema.Registry,
	manifestHistory ManifestHistorian,
	cookbookFS fs.FS,
	logger *slog.Logger,
) (*Service, error) {
	if registry == nil {
		return nil, ErrNilSchemaRegistry
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		schemaRegistry:  registry,
		manifestHistory: manifestHistory,
		cookbookFS:      cookbookFS,
		logger:          logger.With("component", "generator_service"),
	}, nil
}

// GetGenerationContext returns the context that would be used for generating a manifest
// from the given description, without performing the actual generation.
func (s *Service) GetGenerationContext(
	ctx context.Context,
	req *controlplanev1.GetGenerationContextRequest,
) (*controlplanev1.GetGenerationContextResponse, error) {
	if req.GetDescription() == "" {
		return nil, status.Error(codes.InvalidArgument, "description is required")
	}

	opts := ContextAssemblerOptions{
		Description:           req.GetDescription(),
		IncludePatterns:       !req.GetExcludePatterns(),
		MaxPatterns:           3,
		IncludeCurrentEconomy: req.GetIncludeCurrentEconomy(),
	}

	var currentManifest *controlplanev1.Manifest
	var currentEconomyYAML string

	if req.GetIncludeCurrentEconomy() {
		if s.manifestHistory == nil {
			return nil, status.Error(codes.FailedPrecondition, "manifest history is not available")
		}

		manifest, err := s.manifestHistory.GetCurrentManifest(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to get current manifest", "error", err)
			return nil, status.Error(codes.Internal, "failed to load current manifest")
		}

		currentManifest = manifest
		opts.CurrentManifest = currentManifest

		// Serialize manifest for the response field.
		marshaler := protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: false}
		data, err := marshaler.Marshal(currentManifest)
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to serialize current manifest", "error", err)
			return nil, status.Error(codes.Internal, "failed to serialize current manifest")
		}
		currentEconomyYAML = string(data)
	}

	assembled, err := AssembleContext(opts, s.schemaRegistry, s.cookbookFS)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to assemble context", "error", err)
		return nil, status.Error(codes.Internal, "failed to assemble generation context")
	}

	matchedPatterns := make([]*controlplanev1.PatternContext, 0, len(assembled.MatchedPatterns))
	for _, p := range assembled.MatchedPatterns {
		matchedPatterns = append(matchedPatterns, &controlplanev1.PatternContext{
			Name:             p.Name,
			Title:            p.Title,
			Score:            p.Score,
			ManifestFragment: p.ManifestFragment,
			SagaScript:       p.SagaScript,
		})
	}

	return &controlplanev1.GetGenerationContextResponse{
		HandlerReferenceCard:  BuildHandlerReferenceCard(s.schemaRegistry),
		TopicList:             BuildTopicList(),
		ManifestSchemaSummary: BuildManifestSchemaSummary(),
		MatchedPatterns:       matchedPatterns,
		CurrentEconomyYaml:    currentEconomyYAML,
	}, nil
}
