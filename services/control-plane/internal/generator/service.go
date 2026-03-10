package generator

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// defaultMaxFixIterations is applied when the request leaves max_fix_iterations at 0.
const defaultMaxFixIterations = 3

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
	llmClient       LLMClient
	validator       ManifestValidator
	logger          *slog.Logger
}

// ErrNilSchemaRegistry is returned when a nil schema registry is provided.
var ErrNilSchemaRegistry = fmt.Errorf("schema registry is required")

// NewGeneratorService creates a new Service.
// manifestHistory may be nil if include_current_economy is never used.
// llmClient and validator are required for GenerateManifest; they may be nil if only
// GetGenerationContext is used.
func NewGeneratorService(
	registry *schema.Registry,
	manifestHistory ManifestHistorian,
	cookbookFS fs.FS,
	logger *slog.Logger,
	opts ...ServiceOption,
) (*Service, error) {
	if registry == nil {
		return nil, ErrNilSchemaRegistry
	}
	if logger == nil {
		logger = slog.Default()
	}
	svc := &Service{
		schemaRegistry:  registry,
		manifestHistory: manifestHistory,
		cookbookFS:      cookbookFS,
		logger:          logger.With("component", "generator_service"),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// ServiceOption configures optional fields on a Service.
type ServiceOption func(*Service)

// WithLLMClient sets the LLM client for manifest generation.
func WithLLMClient(client LLMClient) ServiceOption {
	return func(s *Service) {
		s.llmClient = client
	}
}

// WithValidator sets the manifest validator for the validate-fix loop.
func WithValidator(v ManifestValidator) ServiceOption {
	return func(s *Service) {
		s.validator = v
	}
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

// GenerateManifest generates a new economy manifest from a natural language description.
// Create mode only — amend mode returns Unimplemented.
func (s *Service) GenerateManifest(
	ctx context.Context,
	req *controlplanev1.GenerateManifestRequest,
) (*controlplanev1.GenerateManifestResponse, error) {
	// Amend mode is not yet implemented (task 12).
	if req.GetMode() == controlplanev1.GenerationMode_GENERATION_MODE_AMEND {
		return nil, status.Error(codes.Unimplemented, "GENERATION_MODE_AMEND is not yet implemented")
	}

	if req.GetDescription() == "" {
		return nil, status.Error(codes.InvalidArgument, "description is required")
	}

	if s.llmClient == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM client is not configured")
	}
	if s.validator == nil {
		return nil, status.Error(codes.FailedPrecondition, "manifest validator is not configured")
	}

	// Determine max fix iterations.
	maxIter := int(req.GetMaxFixIterations())
	if maxIter == 0 {
		maxIter = defaultMaxFixIterations
	}

	// Assemble the generation context / prompt.
	assembleOpts := ContextAssemblerOptions{
		Description:     req.GetDescription(),
		IncludePatterns: true,
		MaxPatterns:     3,
	}
	if prefs := req.GetPreferences(); prefs != nil {
		assembleOpts.Industry = prefs.GetIndustry()
	}

	assembled, err := AssembleContext(assembleOpts, s.schemaRegistry, s.cookbookFS)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to assemble generation context", "error", err)
		return nil, status.Errorf(codes.Internal, "assemble context: %v", err)
	}

	// Call the LLM to generate the initial manifest YAML.
	generatedYAML, err := s.llmClient.Generate(ctx, assembled.Prompt)
	if err != nil {
		s.logger.ErrorContext(ctx, "LLM generation failed", "error", err)
		return nil, status.Errorf(codes.Internal, "generate manifest: %v", err)
	}

	// Run the validate-fix loop.
	fixResult, err := ValidateAndFix(ctx, generatedYAML, ValidateFixOptions{
		MaxIterations:  maxIter,
		LLMClient:      s.llmClient,
		Validator:      s.validator,
		SchemaRegistry: s.schemaRegistry,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "validate-fix loop failed", "error", err)
		return nil, status.Errorf(codes.Internal, "validate and fix manifest: %v", err)
	}

	// Extract metadata from the final manifest YAML.
	meta, extractErr := extractManifestMetadata(fixResult.FinalManifest)
	if extractErr != nil {
		// Non-fatal: metadata extraction failure doesn't block returning the manifest.
		s.logger.WarnContext(ctx, "failed to extract manifest metadata", "error", extractErr)
		meta = &controlplanev1.GenerationMetadata{}
	}

	// Populate metadata from generation process.
	meta.FixIterations = int32(fixResult.IterationsUsed)
	meta.PatternsUsed = patternNames(assembled.MatchedPatterns)

	// Convert validation findings to proto.
	protoErrors := toProtoValidationErrors(fixResult.Errors)
	protoWarnings := toProtoValidationErrors(fixResult.Warnings)

	return &controlplanev1.GenerateManifestResponse{
		ManifestYaml:       fixResult.FinalManifest,
		Valid:              fixResult.Valid,
		ValidationErrors:   protoErrors,
		ValidationWarnings: protoWarnings,
		GenerationMetadata: meta,
	}, nil
}

// patternNames extracts the name field from each PatternMatch.
func patternNames(patterns []PatternMatch) []string {
	if len(patterns) == 0 {
		return nil
	}
	names := make([]string, len(patterns))
	for i, p := range patterns {
		names[i] = p.Name
	}
	return names
}

// toProtoValidationErrors converts generator.ValidationError slices to proto ValidationError slices.
func toProtoValidationErrors(errs []ValidationError) []*controlplanev1.ValidationError {
	if len(errs) == 0 {
		return nil
	}
	out := make([]*controlplanev1.ValidationError, len(errs))
	for i, e := range errs {
		out[i] = &controlplanev1.ValidationError{
			Code:       e.Code,
			Path:       e.Path,
			Message:    e.Message,
			Suggestion: e.Suggestion,
		}
	}
	return out
}

// manifestYAMLDoc is a minimal struct for extracting top-level manifest keys from YAML.
type manifestYAMLDoc struct {
	Instruments  []map[string]interface{} `yaml:"instruments"`
	AccountTypes []map[string]interface{} `yaml:"account_types"`
	Sagas        []map[string]interface{} `yaml:"sagas"`
}

// extractManifestMetadata parses the manifest YAML to extract created resource names.
func extractManifestMetadata(manifestYAMLStr string) (*controlplanev1.GenerationMetadata, error) {
	var doc manifestYAMLDoc
	if err := yaml.Unmarshal([]byte(manifestYAMLStr), &doc); err != nil {
		return nil, fmt.Errorf("parse manifest YAML: %w", err)
	}

	meta := &controlplanev1.GenerationMetadata{}

	for _, inst := range doc.Instruments {
		if code, ok := inst["code"].(string); ok && code != "" {
			meta.InstrumentsCreated = append(meta.InstrumentsCreated, code)
		}
	}

	for _, at := range doc.AccountTypes {
		if code, ok := at["code"].(string); ok && code != "" {
			meta.AccountTypesCreated = append(meta.AccountTypesCreated, code)
		}
	}

	for _, saga := range doc.Sagas {
		if name, ok := saga["name"].(string); ok && name != "" {
			meta.SagasCreated = append(meta.SagasCreated, name)
		}
	}

	return meta, nil
}

// manifestValidatorAdapter implements generator.ManifestValidator by wrapping
// a function that validates manifest YAML.
type manifestValidatorAdapter struct {
	validateFn func(ctx context.Context, manifestYAML string) (*ValidationResult, error)
}

// ValidateDryRun implements ManifestValidator.
func (a *manifestValidatorAdapter) ValidateDryRun(ctx context.Context, manifestYAML string) (*ValidationResult, error) {
	return a.validateFn(ctx, manifestYAML)
}

// NewManifestValidatorAdapter creates a ManifestValidator from a function that converts
// YAML to a proto Manifest and validates it.
func NewManifestValidatorAdapter(
	validateFn func(ctx context.Context, manifestYAML string) (*ValidationResult, error),
) ManifestValidator {
	return &manifestValidatorAdapter{validateFn: validateFn}
}

// yamlToProtoManifest converts a YAML manifest string to a controlplanev1.Manifest proto.
func yamlToProtoManifest(manifestYAML string) (*controlplanev1.Manifest, error) {
	var raw interface{}
	if err := yaml.Unmarshal([]byte(manifestYAML), &raw); err != nil {
		return nil, fmt.Errorf("parse manifest YAML: %w", err)
	}

	jsonCompatible := convertYAMLToJSONCompatible(raw)

	jsonBytes, err := json.Marshal(jsonCompatible)
	if err != nil {
		return nil, fmt.Errorf("re-encode manifest to JSON: %w", err)
	}

	m := &controlplanev1.Manifest{}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(jsonBytes, m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest proto: %w", err)
	}
	return m, nil
}

// convertYAMLToJSONCompatible recursively converts yaml.v3-decoded values to
// JSON-compatible Go types.
func convertYAMLToJSONCompatible(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			out[k] = convertYAMLToJSONCompatible(v2)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			out[fmt.Sprintf("%v", k)] = convertYAMLToJSONCompatible(v2)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = convertYAMLToJSONCompatible(item)
		}
		return out
	default:
		return v
	}
}
