package generator

import (
	"context"
	"encoding/json"
	"errors"
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

// ManifestHistorian retrieves current manifest state for amend mode.
// Implemented by an adapter wrapping the manifest history service.
type ManifestHistorian interface {
	// GetCurrentManifest retrieves the latest applied manifest for the given tenant.
	// Returns the manifest proto and any error. Used in amend mode (task 12).
	GetCurrentManifest(ctx context.Context, tenantID string) (*controlplanev1.Manifest, error)
}

// Sentinel errors for Service construction.
var (
	ErrSchemaRegistryRequired = errors.New("generator service: schema registry is required")
	ErrCookbookFSRequired     = errors.New("generator service: cookbook FS is required")
	ErrLLMClientRequired      = errors.New("generator service: LLM client is required")
	ErrValidatorRequired      = errors.New("generator service: validator is required")
)

// Service implements the EconomyService gRPC interface.
// It assembles a generation context, calls the LLM, and runs the validate-fix loop.
type Service struct {
	controlplanev1.UnimplementedEconomyGeneratorServiceServer

	schemaRegistry *schema.Registry
	manifestClient ManifestHistorian
	cookbookFS     fs.FS
	llmClient      LLMClient
	validator      ManifestValidator
	logger         *slog.Logger
}

// Config contains dependencies for creating a Service.
type Config struct {
	SchemaRegistry *schema.Registry
	ManifestClient ManifestHistorian
	CookbookFS     fs.FS
	LLMClient      LLMClient
	Validator      ManifestValidator
	Logger         *slog.Logger
}

// NewService creates a new Service with the given dependencies.
// ManifestClient is optional — it is only required for amend mode (task 12).
func NewService(cfg Config) (*Service, error) {
	if cfg.SchemaRegistry == nil {
		return nil, ErrSchemaRegistryRequired
	}
	if cfg.CookbookFS == nil {
		return nil, ErrCookbookFSRequired
	}
	if cfg.LLMClient == nil {
		return nil, ErrLLMClientRequired
	}
	if cfg.Validator == nil {
		return nil, ErrValidatorRequired
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		schemaRegistry: cfg.SchemaRegistry,
		manifestClient: cfg.ManifestClient,
		cookbookFS:     cfg.CookbookFS,
		llmClient:      cfg.LLMClient,
		validator:      cfg.Validator,
		logger:         cfg.Logger,
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

	// Validate request.
	if req.GetDescription() == "" {
		return nil, status.Error(codes.InvalidArgument, "description is required")
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

// manifestYAML is a minimal struct for extracting top-level manifest keys from YAML.
// We use interface{} values to avoid having to mirror the full proto structure.
type manifestYAML struct {
	Instruments  []map[string]interface{} `yaml:"instruments"`
	AccountTypes []map[string]interface{} `yaml:"account_types"`
	Sagas        []map[string]interface{} `yaml:"sagas"`
}

// extractManifestMetadata parses the manifest YAML to extract created resource names.
func extractManifestMetadata(manifestYAMLStr string) (*controlplanev1.GenerationMetadata, error) {
	var doc manifestYAML
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
// a function that validates manifest YAML. This allows the generator service to
// inject any validation implementation without a circular dependency.
type manifestValidatorAdapter struct {
	validateFn func(ctx context.Context, manifestYAML string) (*ValidationResult, error)
}

// ValidateDryRun implements ManifestValidator.
func (a *manifestValidatorAdapter) ValidateDryRun(ctx context.Context, manifestYAML string) (*ValidationResult, error) {
	return a.validateFn(ctx, manifestYAML)
}

// NewManifestValidatorAdapter creates a ManifestValidator from a function that converts
// YAML to a proto Manifest and validates it. The provided validateFn should parse the
// YAML, call the real validator, and return structured results.
//
// Typical use: wrap a *validator.ManifestValidator by converting YAML → JSON → proto.
func NewManifestValidatorAdapter(
	validateFn func(ctx context.Context, manifestYAML string) (*ValidationResult, error),
) ManifestValidator {
	return &manifestValidatorAdapter{validateFn: validateFn}
}

// yamlToProtoManifest converts a YAML manifest string to a controlplanev1.Manifest proto.
// It first marshals YAML to a generic Go map, then re-encodes to JSON, then uses
// protojson to unmarshal into the proto type.
func yamlToProtoManifest(manifestYAML string) (*controlplanev1.Manifest, error) {
	// Parse YAML into a generic map.
	var raw interface{}
	if err := yaml.Unmarshal([]byte(manifestYAML), &raw); err != nil {
		return nil, fmt.Errorf("parse manifest YAML: %w", err)
	}

	// Convert YAML-decoded value to JSON-compatible representation.
	jsonCompatible := convertYAMLToJSONCompatible(raw)

	// Marshal to JSON.
	jsonBytes, err := json.Marshal(jsonCompatible)
	if err != nil {
		return nil, fmt.Errorf("re-encode manifest to JSON: %w", err)
	}

	// Unmarshal JSON into proto.
	m := &controlplanev1.Manifest{}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(jsonBytes, m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest proto: %w", err)
	}
	return m, nil
}

// convertYAMLToJSONCompatible recursively converts yaml.v3-decoded values to
// JSON-compatible Go types. yaml.v3 uses map[string]interface{} for mappings and
// []interface{} for sequences, which are already JSON-compatible. However, map keys
// decoded from YAML may occasionally be non-string types (e.g., int) which JSON
// cannot marshal. This function normalises those to strings.
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
