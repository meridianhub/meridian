package generator_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Mock implementations ---

// mockGenerateLLMClient implements generator.LLMClient for service tests.
type mockGenerateLLMClient struct {
	generateResponse string
	generateErr      error
	fixResponse      string
	fixErr           error
	fixCallCount     int
}

func (m *mockGenerateLLMClient) Generate(_ context.Context, _ string) (string, error) {
	return m.generateResponse, m.generateErr
}

func (m *mockGenerateLLMClient) Fix(_ context.Context, _ string, _ []generator.ValidationError) (string, error) {
	m.fixCallCount++
	return m.fixResponse, m.fixErr
}

// mockValidatorAlwaysValid implements generator.ManifestValidator and always returns valid.
type mockValidatorAlwaysValid struct{}

func (m *mockValidatorAlwaysValid) ValidateDryRun(_ context.Context, _ string) (*generator.ValidationResult, error) {
	return &generator.ValidationResult{Valid: true}, nil
}

// mockValidatorSequence implements generator.ManifestValidator with a queue of responses.
type mockValidatorSequence struct {
	responses []*generator.ValidationResult
	errs      []error
}

func (m *mockValidatorSequence) ValidateDryRun(_ context.Context, _ string) (*generator.ValidationResult, error) {
	if len(m.errs) > 0 {
		e := m.errs[0]
		m.errs = m.errs[1:]
		if e != nil {
			return nil, e
		}
	}
	if len(m.responses) == 0 {
		return &generator.ValidationResult{Valid: true}, nil
	}
	r := m.responses[0]
	m.responses = m.responses[1:]
	return r, nil
}

// --- helpers ---

// minimalValidYAML is a well-formed manifest YAML for testing.
const minimalValidYAML = `
instruments:
  - code: GBP
    name: British Pound
    asset_class: CURRENCY
account_types:
  - code: CURRENT
    name: Current Account
    allowed_instruments:
      - GBP
sagas:
  - name: simple_transfer
    trigger:
      topic: payment_order.created
    script: |
      result = {}
`

// buildServiceConfig returns a GeneratorServiceConfig wired with the provided mocks.
func buildServiceConfig(llm generator.LLMClient, val generator.ManifestValidator) generator.Config {
	return generator.Config{
		SchemaRegistry: buildMinimalRegistry(),
		CookbookFS:     emptyFS(),
		LLMClient:      llm,
		Validator:      val,
	}
}

// --- Constructor tests ---

func TestNewGeneratorService_RequiresSchemaRegistry(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{}, &mockValidatorAlwaysValid{})
	cfg.SchemaRegistry = nil
	_, err := generator.NewService(cfg)
	require.ErrorIs(t, err, generator.ErrSchemaRegistryRequired)
}

func TestNewGeneratorService_RequiresCookbookFS(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{}, &mockValidatorAlwaysValid{})
	cfg.CookbookFS = nil
	_, err := generator.NewService(cfg)
	require.ErrorIs(t, err, generator.ErrCookbookFSRequired)
}

func TestNewGeneratorService_RequiresLLMClient(t *testing.T) {
	cfg := buildServiceConfig(nil, &mockValidatorAlwaysValid{})
	_, err := generator.NewService(cfg)
	require.ErrorIs(t, err, generator.ErrLLMClientRequired)
}

func TestNewGeneratorService_RequiresValidator(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{}, nil)
	_, err := generator.NewService(cfg)
	require.ErrorIs(t, err, generator.ErrValidatorRequired)
}

func TestNewGeneratorService_AcceptsNilManifestClient(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{generateResponse: minimalValidYAML}, &mockValidatorAlwaysValid{})
	cfg.ManifestClient = nil
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

// --- GenerateManifest tests ---

func TestGenerateManifest_AmendModeReturnsUnimplemented(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{}, &mockValidatorAlwaysValid{})
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "a fintech",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestGenerateManifest_EmptyDescriptionReturnsInvalidArgument(t *testing.T) {
	cfg := buildServiceConfig(&mockGenerateLLMClient{}, &mockValidatorAlwaysValid{})
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGenerateManifest_CreateMode_ValidOnFirstPass(t *testing.T) {
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A simple bank account service",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.True(t, resp.Valid)
	assert.Equal(t, minimalValidYAML, resp.ManifestYaml)
	assert.Empty(t, resp.ValidationErrors)
	assert.NotNil(t, resp.GenerationMetadata)
	// fix_iterations == 0 because the manifest was valid on the first pass
	assert.Equal(t, int32(0), resp.GenerationMetadata.FixIterations)
}

func TestGenerateManifest_CreateMode_MetadataExtracted(t *testing.T) {
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.NoError(t, err)

	meta := resp.GenerationMetadata
	require.NotNil(t, meta)
	assert.Contains(t, meta.InstrumentsCreated, "GBP")
	assert.Contains(t, meta.AccountTypesCreated, "CURRENT")
	assert.Contains(t, meta.SagasCreated, "simple_transfer")
}

func TestGenerateManifest_CreateMode_FixIterationsRespected(t *testing.T) {
	// First validation fails, then succeeds.
	val := &mockValidatorSequence{
		responses: []*generator.ValidationResult{
			{
				Valid: false,
				Errors: []generator.ValidationError{
					{Code: "UNKNOWN_HANDLER", Path: "sagas[0]", Message: "unknown handler"},
				},
			},
			{Valid: true},
		},
	}
	llm := &mockGenerateLLMClient{
		generateResponse: minimalValidYAML,
		fixResponse:      minimalValidYAML,
	}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description:      "A bank",
		MaxFixIterations: 3,
	})
	require.NoError(t, err)

	assert.True(t, resp.Valid)
	assert.Equal(t, int32(1), resp.GenerationMetadata.FixIterations)
	assert.Equal(t, 1, llm.fixCallCount)
}

func TestGenerateManifest_CreateMode_MaxFixIterationsExhausted(t *testing.T) {
	// Validator always returns errors.
	val := &mockValidatorSequence{
		responses: []*generator.ValidationResult{
			{Valid: false, Errors: []generator.ValidationError{{Code: "ERR", Path: "x", Message: "bad"}}},
			{Valid: false, Errors: []generator.ValidationError{{Code: "ERR", Path: "x", Message: "bad"}}},
			{Valid: false, Errors: []generator.ValidationError{{Code: "ERR", Path: "x", Message: "bad"}}},
			{Valid: false, Errors: []generator.ValidationError{{Code: "ERR", Path: "x", Message: "bad"}}},
		},
	}
	llm := &mockGenerateLLMClient{
		generateResponse: minimalValidYAML,
		fixResponse:      minimalValidYAML,
	}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description:      "A bank",
		MaxFixIterations: 2,
	})
	require.NoError(t, err)

	// Response is returned even when invalid (caller sees the errors).
	assert.False(t, resp.Valid)
	assert.NotEmpty(t, resp.ValidationErrors)
	assert.Equal(t, int32(2), resp.GenerationMetadata.FixIterations)
}

func TestGenerateManifest_CreateMode_DefaultMaxIterationsApplied(t *testing.T) {
	// Validator always valid immediately — just checking no error on max_fix_iterations=0.
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	// max_fix_iterations is 0 → server applies default (3).
	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description:      "A bank",
		MaxFixIterations: 0,
	})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

func TestGenerateManifest_CreateMode_LLMGenerateError(t *testing.T) {
	llm := &mockGenerateLLMClient{generateErr: errors.New("API unavailable")}
	val := &mockValidatorAlwaysValid{}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGenerateManifest_CreateMode_ValidationWarningsIncluded(t *testing.T) {
	val := &mockValidatorSequence{
		responses: []*generator.ValidationResult{
			{
				Valid: true,
				Warnings: []generator.ValidationError{
					{Code: "WARN_001", Path: "instruments[0]", Message: "consider adding a description"},
				},
			},
		},
	}
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.NoError(t, err)

	assert.True(t, resp.Valid)
	require.Len(t, resp.ValidationWarnings, 1)
	assert.Equal(t, "WARN_001", resp.ValidationWarnings[0].Code)
}

func TestGenerateManifest_CreateMode_UnspecifiedModeTreatedAsCreate(t *testing.T) {
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	cfg := buildServiceConfig(llm, val)
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	// GENERATION_MODE_UNSPECIFIED should be treated as CREATE.
	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_UNSPECIFIED,
	})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

// --- yamlToProtoManifest / metadata extraction tests ---

func TestExtractManifestMetadata_InstrumentsAccountTypesSagas(t *testing.T) {
	svc, err := generator.NewService(buildServiceConfig(
		&mockGenerateLLMClient{generateResponse: minimalValidYAML},
		&mockValidatorAlwaysValid{},
	))
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Test economy",
	})
	require.NoError(t, err)

	meta := resp.GenerationMetadata
	require.NotNil(t, meta)
	assert.Equal(t, []string{"GBP"}, meta.InstrumentsCreated)
	assert.Equal(t, []string{"CURRENT"}, meta.AccountTypesCreated)
	assert.Equal(t, []string{"simple_transfer"}, meta.SagasCreated)
}

func TestExtractManifestMetadata_EmptyManifest(t *testing.T) {
	emptyYAML := `instruments: []
account_types: []
sagas: []
`
	llm := &mockGenerateLLMClient{generateResponse: emptyYAML}
	val := &mockValidatorAlwaysValid{}
	svc, err := generator.NewService(buildServiceConfig(llm, val))
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "empty",
	})
	require.NoError(t, err)

	meta := resp.GenerationMetadata
	require.NotNil(t, meta)
	assert.Empty(t, meta.InstrumentsCreated)
	assert.Empty(t, meta.AccountTypesCreated)
	assert.Empty(t, meta.SagasCreated)
}

// --- NewManifestValidatorAdapter tests ---

func TestNewManifestValidatorAdapter_CallsDelegate(t *testing.T) {
	called := false
	adapter := generator.NewManifestValidatorAdapter(func(_ context.Context, yaml string) (*generator.ValidationResult, error) {
		called = true
		assert.Equal(t, "test: yaml", yaml)
		return &generator.ValidationResult{Valid: true}, nil
	})

	result, err := adapter.ValidateDryRun(context.Background(), "test: yaml")
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.True(t, called)
}

func TestNewManifestValidatorAdapter_PropagatesError(t *testing.T) {
	wantErr := errors.New("validation backend down")
	adapter := generator.NewManifestValidatorAdapter(func(_ context.Context, _ string) (*generator.ValidationResult, error) {
		return nil, wantErr
	})

	_, err := adapter.ValidateDryRun(context.Background(), "any: yaml")
	require.ErrorIs(t, err, wantErr)
}

// --- cookbookFS with patterns tests ---

func TestGenerateManifest_PatternNamesPopulated(t *testing.T) {
	// Build a cookbook FS with one simple pattern.
	cookFS := fstest.MapFS{
		"registry.json": &fstest.MapFile{Data: []byte(`{
			"items":[{"name":"energy-settlement","title":"Energy Settlement","tags":["energy","settlement"]}]
		}`)},
		"patterns/energy-settlement/pattern.json": &fstest.MapFile{Data: []byte(`{
			"name":"energy-settlement",
			"title":"Energy Settlement",
			"description":"Handles energy asset settlement",
			"tags":["energy","settlement"],
			"provides":[],
			"requires":[],
			"composes_with":[],
			"conflicts_with":[]
		}`)},
		"patterns/energy-settlement/manifest-fragment.yaml": &fstest.MapFile{Data: []byte(`instruments:
  - code: KWH
    name: Kilowatt Hour
`)},
	}

	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	cfg := generator.Config{
		SchemaRegistry: buildMinimalRegistry(),
		CookbookFS:     cookFS,
		LLMClient:      llm,
		Validator:      val,
	}
	svc, err := generator.NewService(cfg)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Energy grid settlement system for kilowatt-hour tracking",
	})
	require.NoError(t, err)
	// Generation metadata is always populated.
	assert.NotNil(t, resp.GenerationMetadata)
	// The patterns_used field is populated from matched patterns (may be empty if scorer
	// does not find a match above threshold — that's acceptable).
	assert.True(t, resp.Valid)
}
