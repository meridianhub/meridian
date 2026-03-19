package generator_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
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

// mockManifestHistorian is a test double for ManifestHistorian.
type mockManifestHistorian struct {
	manifest *controlplanev1.Manifest
	err      error
}

func (m *mockManifestHistorian) GetCurrentManifest(_ context.Context) (*controlplanev1.Manifest, error) {
	return m.manifest, m.err
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

// buildService creates a Service with the given LLM client and validator using functional options.
func buildService(t *testing.T, llm generator.LLMClient, val generator.ManifestValidator) *generator.Service {
	t.Helper()
	opts := []generator.ServiceOption{}
	if llm != nil {
		opts = append(opts, generator.WithLLMClient(llm))
	}
	if val != nil {
		opts = append(opts, generator.WithValidator(val))
	}
	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), nil, emptyFS(), nil, opts...)
	require.NoError(t, err)
	return svc
}

// --- Constructor tests (NewGeneratorService) ---

func TestNewGeneratorService_NilRegistry_ReturnsError(t *testing.T) {
	_, err := generator.NewGeneratorService(nil, nil, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, generator.ErrNilSchemaRegistry)
}

func TestNewGeneratorService_ValidRegistry_ReturnsService(t *testing.T) {
	reg := buildMinimalRegistry()
	svc, err := generator.NewGeneratorService(reg, nil, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewGeneratorService_NilLLMAndValidator_ReturnsService(t *testing.T) {
	// llmClient and validator may be nil when only GetGenerationContext is used.
	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), nil, emptyFS(), nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// --- GetGenerationContext tests ---

func TestGetGenerationContext_MissingDescription_ReturnsError(t *testing.T) {
	reg := buildMinimalRegistry()
	svc, err := generator.NewGeneratorService(reg, nil, emptyFS(), nil)
	require.NoError(t, err)

	_, err = svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description is required")
}

func TestGetGenerationContext_BasicRequest_ReturnsContext(t *testing.T) {
	reg := buildMinimalRegistry()

	svc, err := generator.NewGeneratorService(reg, nil, emptyFS(), nil)
	require.NoError(t, err)

	resp, err := svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description: "An energy trading platform that manages electricity contracts",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetHandlerReferenceCard())
	assert.NotEmpty(t, resp.GetTopicList())
	assert.NotEmpty(t, resp.GetManifestSchemaSummary())
	assert.Empty(t, resp.GetCurrentEconomyYaml())
}

func TestGetGenerationContext_ExcludePatterns_ReturnsNoPatterns(t *testing.T) {
	reg := buildMinimalRegistry()
	cookbookFS := cookbookWithOnePattern()

	svc, err := generator.NewGeneratorService(reg, nil, cookbookFS, nil)
	require.NoError(t, err)

	resp, err := svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description:     "energy trading",
		ExcludePatterns: true,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetMatchedPatterns())
}

func TestGetGenerationContext_IncludePatterns_ReturnsMatchedPatterns(t *testing.T) {
	reg := buildMinimalRegistry()
	cookbookFS := cookbookWithOnePattern()

	svc, err := generator.NewGeneratorService(reg, nil, cookbookFS, nil)
	require.NoError(t, err)

	resp, err := svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description:     "energy trading platform",
		ExcludePatterns: false,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetMatchedPatterns())
	p := resp.GetMatchedPatterns()[0]
	assert.NotEmpty(t, p.GetName())
	assert.NotEmpty(t, p.GetTitle())
}

func TestGetGenerationContext_IncludeCurrentEconomy_NoHistorian_ReturnsError(t *testing.T) {
	reg := buildMinimalRegistry()
	svc, err := generator.NewGeneratorService(reg, nil, emptyFS(), nil)
	require.NoError(t, err)

	_, err = svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description:           "energy trading",
		IncludeCurrentEconomy: true,
		TenantId:              "tenant-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest history is not available")
}

func TestGetGenerationContext_IncludeCurrentEconomy_HistorianError_ReturnsError(t *testing.T) {
	reg := buildMinimalRegistry()
	historian := &mockManifestHistorian{err: errors.New("db down")}

	svc, err := generator.NewGeneratorService(reg, historian, emptyFS(), nil)
	require.NoError(t, err)

	_, err = svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description:           "energy trading",
		IncludeCurrentEconomy: true,
		TenantId:              "tenant-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load current manifest")
}

func TestGetGenerationContext_IncludeCurrentEconomy_ReturnsYAML(t *testing.T) {
	reg := buildMinimalRegistry()
	manifest := &controlplanev1.Manifest{
		Version: "1.0.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name: "test-economy",
		},
	}
	historian := &mockManifestHistorian{manifest: manifest}

	svc, err := generator.NewGeneratorService(reg, historian, emptyFS(), nil)
	require.NoError(t, err)

	resp, err := svc.GetGenerationContext(context.Background(), &controlplanev1.GetGenerationContextRequest{
		Description:           "energy trading",
		IncludeCurrentEconomy: true,
		TenantId:              "tenant-1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetCurrentEconomyYaml())
}

// --- GenerateManifest tests ---

// --- Amend mode tests ---

func TestGenerateManifest_AmendMode_NoHistorian_ReturnsFailedPrecondition(t *testing.T) {
	// Service created without manifestHistory.
	svc := buildService(t, &mockGenerateLLMClient{generateResponse: minimalValidYAML}, &mockValidatorAlwaysValid{})

	_, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "add carbon credits",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "manifest history is not available")
}

func TestGenerateManifest_AmendMode_HistorianError_ReturnsInternal(t *testing.T) {
	historian := &mockManifestHistorian{err: errors.New("db unavailable")}
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}

	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), historian, emptyFS(), nil,
		generator.WithLLMClient(llm),
		generator.WithValidator(val),
	)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "add carbon credits",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGenerateManifest_AmendMode_LoadsCurrentManifest(t *testing.T) {
	// Original manifest has GBP instrument.
	manifest := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "test-economy"},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account", AllowedInstruments: []string{"GBP"}},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "simple_transfer", Trigger: "api:/v1/transfers", Script: "result = {}"},
		},
	}
	historian := &mockManifestHistorian{manifest: manifest}

	// LLM returns amended manifest with additional CARBON_CREDIT instrument.
	amendedYAML := `
instruments:
  - code: GBP
    name: British Pound
    asset_class: CURRENCY
  - code: CARBON_CREDIT
    name: Carbon Credit
    asset_class: COMMODITY
account_types:
  - code: CURRENT
    name: Current Account
    allowed_instruments:
      - GBP
  - code: CARBON_INVENTORY
    name: Carbon Inventory Account
    allowed_instruments:
      - CARBON_CREDIT
sagas:
  - name: simple_transfer
    trigger:
      topic: payment_order.created
    script: |
      result = {}
  - name: carbon_offset_flow
    trigger:
      topic: carbon.offset.created
    script: |
      result = {}
`
	llm := &mockGenerateLLMClient{generateResponse: amendedYAML}
	val := &mockValidatorAlwaysValid{}

	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), historian, emptyFS(), nil,
		generator.WithLLMClient(llm),
		generator.WithValidator(val),
	)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Add carbon credit tracking to existing economy",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.True(t, resp.Valid)
	assert.Equal(t, amendedYAML, resp.ManifestYaml)

	// Metadata should reflect the amended manifest contents.
	meta := resp.GenerationMetadata
	require.NotNil(t, meta)
	assert.Contains(t, meta.InstrumentsCreated, "GBP")
	assert.Contains(t, meta.InstrumentsCreated, "CARBON_CREDIT")
	assert.Contains(t, meta.SagasCreated, "carbon_offset_flow")

	// Decisions should record the impact analysis.
	assert.NotEmpty(t, meta.Decisions)
}

func TestGenerateManifest_AmendMode_PreservesExistingResources(t *testing.T) {
	// Original has GBP + CURRENT + simple_transfer.
	manifest := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "test"},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account"},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "simple_transfer", Trigger: "api:/v1/transfers", Script: "result = {}"},
		},
	}
	historian := &mockManifestHistorian{manifest: manifest}

	// LLM preserves all originals and adds EUR.
	amendedYAML := `
instruments:
  - code: GBP
    name: British Pound
    asset_class: CURRENCY
  - code: EUR
    name: Euro
    asset_class: CURRENCY
account_types:
  - code: CURRENT
    name: Current Account
    allowed_instruments:
      - GBP
      - EUR
sagas:
  - name: simple_transfer
    trigger:
      topic: payment_order.created
    script: |
      result = {}
`
	llm := &mockGenerateLLMClient{generateResponse: amendedYAML}
	val := &mockValidatorAlwaysValid{}

	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), historian, emptyFS(), nil,
		generator.WithLLMClient(llm),
		generator.WithValidator(val),
	)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Add EUR support",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.NoError(t, err)

	meta := resp.GenerationMetadata
	require.NotNil(t, meta)

	// EUR should be reported as added.
	hasAdded := false
	for _, d := range meta.Decisions {
		if d == "Added instrument:EUR" {
			hasAdded = true
		}
	}
	assert.True(t, hasAdded, "expected 'Added instrument:EUR' in decisions, got: %v", meta.Decisions)

	// No removal warnings.
	for _, w := range resp.ValidationWarnings {
		assert.NotEqual(t, "AMEND_RESOURCE_REMOVED", w.Code, "unexpected removal warning: %s", w.Path)
	}
}

func TestGenerateManifest_AmendMode_DestructiveChangeDetection(t *testing.T) {
	// Original has GBP + USD, CURRENT + SAVINGS, simple_transfer.
	manifest := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "test"},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
			{Code: "USD", Name: "US Dollar"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "CURRENT", Name: "Current Account"},
			{Code: "SAVINGS", Name: "Savings Account"},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "simple_transfer", Trigger: "api:/v1/transfers", Script: "result = {}"},
		},
	}
	historian := &mockManifestHistorian{manifest: manifest}

	// LLM returns manifest with USD removed (destructive change).
	amendedYAML := `
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
	llm := &mockGenerateLLMClient{generateResponse: amendedYAML}
	val := &mockValidatorAlwaysValid{}

	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), historian, emptyFS(), nil,
		generator.WithLLMClient(llm),
		generator.WithValidator(val),
	)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Simplify to GBP only",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_AMEND,
		TenantId:    "tenant-1",
	})
	require.NoError(t, err)

	// Should flag removal of USD and SAVINGS as warnings.
	removedCodes := map[string]bool{}
	for _, w := range resp.ValidationWarnings {
		if w.Code == "AMEND_RESOURCE_REMOVED" {
			removedCodes[w.Path] = true
		}
	}
	assert.True(t, removedCodes["instrument:USD"], "expected removal warning for instrument:USD")
	assert.True(t, removedCodes["account_type:SAVINGS"], "expected removal warning for account_type:SAVINGS")

	// Decisions should also mention removals.
	hasRemovalDecision := false
	for _, d := range resp.GenerationMetadata.Decisions {
		if d == "Warning: Removed instrument:USD (was present in original manifest)" {
			hasRemovalDecision = true
		}
	}
	assert.True(t, hasRemovalDecision, "expected removal decision for USD, got: %v", resp.GenerationMetadata.Decisions)
}

func TestGenerateManifest_EmptyDescriptionReturnsInvalidArgument(t *testing.T) {
	svc := buildService(t, &mockGenerateLLMClient{}, &mockValidatorAlwaysValid{})

	_, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGenerateManifest_NilLLMClient_ReturnsFailedPrecondition(t *testing.T) {
	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), nil, emptyFS(), nil,
		generator.WithValidator(&mockValidatorAlwaysValid{}),
	)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestGenerateManifest_NilValidator_ReturnsFailedPrecondition(t *testing.T) {
	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), nil, emptyFS(), nil,
		generator.WithLLMClient(&mockGenerateLLMClient{}),
	)
	require.NoError(t, err)

	_, err = svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestGenerateManifest_CreateMode_ValidOnFirstPass(t *testing.T) {
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	val := &mockValidatorAlwaysValid{}
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

	_, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
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
	svc := buildService(t, llm, val)

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
	svc := buildService(t, llm, val)

	// GENERATION_MODE_UNSPECIFIED should be treated as CREATE.
	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
		Mode:        controlplanev1.GenerationMode_GENERATION_MODE_UNSPECIFIED,
	})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

// --- Metadata extraction tests ---

func TestExtractManifestMetadata_InstrumentsAccountTypesSagas(t *testing.T) {
	svc := buildService(t,
		&mockGenerateLLMClient{generateResponse: minimalValidYAML},
		&mockValidatorAlwaysValid{},
	)

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
	svc := buildService(t, llm, val)

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
	svc, err := generator.NewGeneratorService(buildMinimalRegistry(), nil, cookFS, nil,
		generator.WithLLMClient(llm),
		generator.WithValidator(val),
	)
	require.NoError(t, err)

	resp, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "Energy grid settlement system for kilowatt-hour tracking",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp.GenerationMetadata)
	assert.True(t, resp.Valid)
}

// --- extractManifestMetadata tests (via export_test.go) ---

func TestExtractManifestMetadata_ValidYAML(t *testing.T) {
	yamlStr := `
instruments:
  - code: GBP
    name: British Pound
  - code: EUR
    name: Euro
account_types:
  - code: CURRENT
    name: Current Account
  - code: SAVINGS
    name: Savings Account
sagas:
  - name: transfer_funds
  - name: settle_trade
`
	meta, err := generator.ExtractManifestMetadata(yamlStr)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, []string{"GBP", "EUR"}, meta.InstrumentsCreated)
	assert.Equal(t, []string{"CURRENT", "SAVINGS"}, meta.AccountTypesCreated)
	assert.Equal(t, []string{"transfer_funds", "settle_trade"}, meta.SagasCreated)
}

func TestExtractManifestMetadata_EmptyYAML(t *testing.T) {
	yamlStr := `
instruments: []
account_types: []
sagas: []
`
	meta, err := generator.ExtractManifestMetadata(yamlStr)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Empty(t, meta.InstrumentsCreated)
	assert.Empty(t, meta.AccountTypesCreated)
	assert.Empty(t, meta.SagasCreated)
}

func TestExtractManifestMetadata_InvalidYAML(t *testing.T) {
	_, err := generator.ExtractManifestMetadata("{{{{not valid yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse manifest YAML")
}

// --- patternNames tests ---

func TestPatternNames_Empty(t *testing.T) {
	result := generator.PatternNames(nil)
	assert.Nil(t, result)

	result = generator.PatternNames([]generator.PatternMatch{})
	assert.Nil(t, result)
}

func TestPatternNames_WithItems(t *testing.T) {
	patterns := []generator.PatternMatch{
		{Name: "energy-settlement"},
		{Name: "carbon-offset"},
		{Name: "neobank-payments"},
	}
	result := generator.PatternNames(patterns)
	assert.Equal(t, []string{"energy-settlement", "carbon-offset", "neobank-payments"}, result)
}

// --- toProtoValidationErrors tests ---

func TestToProtoValidationErrors_Empty(t *testing.T) {
	result := generator.ToProtoValidationErrors(nil)
	assert.Nil(t, result)

	result = generator.ToProtoValidationErrors([]generator.ValidationError{})
	assert.Nil(t, result)
}

func TestToProtoValidationErrors_WithItems(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:         "ERR_001",
			Path:         "instruments[0].code",
			Message:      "invalid code",
			Suggestion:   "use uppercase",
			ResourceType: "instrument",
			ResourceID:   "gbp",
		},
		{
			Code:    "ERR_002",
			Path:    "sagas[0]",
			Message: "unknown handler",
		},
	}
	result := generator.ToProtoValidationErrors(errs)
	require.Len(t, result, 2)
	assert.Equal(t, "ERR_001", result[0].Code)
	assert.Equal(t, "instruments[0].code", result[0].Path)
	assert.Equal(t, "invalid code", result[0].Message)
	assert.Equal(t, "use uppercase", result[0].Suggestion)
	assert.Equal(t, "instrument", result[0].ResourceType)
	assert.Equal(t, "gbp", result[0].ResourceId)

	assert.Equal(t, "ERR_002", result[1].Code)
	assert.Equal(t, "sagas[0]", result[1].Path)
	assert.Equal(t, "unknown handler", result[1].Message)
}

// --- convertYAMLToJSONCompatible tests ---

func TestConvertYAMLToJSONCompatible(t *testing.T) {
	t.Run("map[string]interface{}", func(t *testing.T) {
		input := map[string]interface{}{
			"key": "value",
			"nested": map[string]interface{}{
				"inner": "data",
			},
		}
		result := generator.ConvertYAMLToJSONCompatible(input)
		m, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "value", m["key"])
		nested, ok := m["nested"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "data", nested["inner"])
	})

	t.Run("map[interface{}]interface{}", func(t *testing.T) {
		input := map[interface{}]interface{}{
			"key":   "value",
			42:      "numeric-key",
			"nested": map[interface{}]interface{}{
				"inner": "data",
			},
		}
		result := generator.ConvertYAMLToJSONCompatible(input)
		m, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "value", m["key"])
		assert.Equal(t, "numeric-key", m["42"])
		nested, ok := m["nested"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "data", nested["inner"])
	})

	t.Run("[]interface{}", func(t *testing.T) {
		input := []interface{}{
			"a",
			map[string]interface{}{"b": "c"},
			42,
		}
		result := generator.ConvertYAMLToJSONCompatible(input)
		arr, ok := result.([]interface{})
		require.True(t, ok)
		require.Len(t, arr, 3)
		assert.Equal(t, "a", arr[0])
		m, ok := arr[1].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "c", m["b"])
		assert.Equal(t, 42, arr[2])
	})

	t.Run("scalar values pass through", func(t *testing.T) {
		assert.Equal(t, "hello", generator.ConvertYAMLToJSONCompatible("hello"))
		assert.Equal(t, 42, generator.ConvertYAMLToJSONCompatible(42))
		assert.Equal(t, true, generator.ConvertYAMLToJSONCompatible(true))
		assert.Nil(t, generator.ConvertYAMLToJSONCompatible(nil))
	})
}

// --- yamlToProtoManifest tests ---

func TestYamlToProtoManifest_Valid(t *testing.T) {
	yamlStr := `
version: "1.0"
metadata:
  name: test-economy
instruments:
  - code: GBP
    name: British Pound
`
	m, err := generator.YAMLToProtoManifest(yamlStr)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "1.0", m.Version)
	assert.Equal(t, "test-economy", m.Metadata.GetName())
	require.Len(t, m.Instruments, 1)
	assert.Equal(t, "GBP", m.Instruments[0].Code)
}

func TestYamlToProtoManifest_InvalidYAML(t *testing.T) {
	_, err := generator.YAMLToProtoManifest("{{{{not valid yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse manifest YAML")
}

// --- applyAmendImpact tests ---

func TestApplyAmendImpact_AdditionsAndRemovals(t *testing.T) {
	originalYAML := `
instruments:
  - code: GBP
  - code: USD
account_types:
  - code: CURRENT
sagas:
  - name: transfer
`
	amendedYAML := `
instruments:
  - code: GBP
  - code: EUR
account_types:
  - code: CURRENT
  - code: SAVINGS
sagas:
  - name: transfer
`
	meta := &controlplanev1.GenerationMetadata{}
	warnings := generator.ApplyAmendImpactFn(originalYAML, amendedYAML, meta)

	// USD was removed, so we expect a removal warning.
	var removedPaths []string
	for _, w := range warnings {
		if w.Code == "AMEND_RESOURCE_REMOVED" {
			removedPaths = append(removedPaths, w.Path)
		}
	}
	assert.Contains(t, removedPaths, "instrument:USD")

	// Decisions should be populated.
	assert.NotEmpty(t, meta.Decisions)
}

func TestApplyAmendImpact_NoRemovals(t *testing.T) {
	originalYAML := `
instruments:
  - code: GBP
`
	amendedYAML := `
instruments:
  - code: GBP
  - code: EUR
`
	meta := &controlplanev1.GenerationMetadata{}
	warnings := generator.ApplyAmendImpactFn(originalYAML, amendedYAML, meta)

	// No removals, so no AMEND_RESOURCE_REMOVED warnings.
	for _, w := range warnings {
		assert.NotEqual(t, "AMEND_RESOURCE_REMOVED", w.Code)
	}
	assert.NotEmpty(t, meta.Decisions)
}

// --- GenerateManifest error path: LLM fix error ---

func TestGenerateManifest_CreateMode_LLMFixError(t *testing.T) {
	val := &mockValidatorSequence{
		responses: []*generator.ValidationResult{
			{
				Valid: false,
				Errors: []generator.ValidationError{
					{Code: "ERR", Path: "x", Message: "bad"},
				},
			},
		},
	}
	llm := &mockGenerateLLMClient{
		generateResponse: minimalValidYAML,
		fixErr:           errors.New("fix API down"),
	}
	svc := buildService(t, llm, val)

	_, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description:      "A bank",
		MaxFixIterations: 1,
	})
	// The validate-fix loop should propagate the fix error.
	require.Error(t, err)
}

// --- GenerateManifest: validator error on initial pass ---

func TestGenerateManifest_CreateMode_ValidatorError(t *testing.T) {
	val := &mockValidatorSequence{
		errs: []error{errors.New("validator backend down")},
	}
	llm := &mockGenerateLLMClient{generateResponse: minimalValidYAML}
	svc := buildService(t, llm, val)

	_, err := svc.GenerateManifest(context.Background(), &controlplanev1.GenerateManifestRequest{
		Description: "A bank",
	})
	require.Error(t, err)
}

// cookbookWithOnePattern returns a minimal cookbook FS with one energy-related pattern.
func cookbookWithOnePattern() fstest.MapFS {
	return fstest.MapFS{
		"registry.json": &fstest.MapFile{Data: []byte(`{
			"items": [
				{"name": "energy-settlement", "type": "registry:pattern", "title": "Energy Settlement"}
			]
		}`)},
		"patterns/energy-settlement/pattern.json": &fstest.MapFile{Data: []byte(`{
			"name": "energy-settlement",
			"type": "registry:pattern",
			"title": "Energy Settlement",
			"description": "Settles energy trades between counterparties",
			"meta": {
				"industries": ["energy"],
				"provides": {
					"instruments": ["kWh"],
					"sagas": ["settle_energy_trade"]
				}
			}
		}`)},
		"patterns/energy-settlement/manifest-fragment.yaml": &fstest.MapFile{Data: []byte(`instruments:
  - code: kWh
    name: Kilowatt Hour
`)},
	}
}
