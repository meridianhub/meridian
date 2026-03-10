package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// --- Mock ---

type mockEconomyGeneratorClient struct {
	generateFn func(ctx context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error)
	contextFn  func(ctx context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error)
}

func (m *mockEconomyGeneratorClient) GenerateManifest(ctx context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
	return m.generateFn(ctx, req)
}

func (m *mockEconomyGeneratorClient) GetGenerationContext(ctx context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
	return m.contextFn(ctx, req)
}

// --- RegisterEconomyGeneratorTools ---

func TestRegisterEconomyGeneratorTools_NilClient_NoRegistration(t *testing.T) {
	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, nil)
	assert.Empty(t, reg.List())
}

func TestRegisterEconomyGeneratorTools_RegistersBothTools(t *testing.T) {
	reg := tools.NewRegistry()
	mock := &mockEconomyGeneratorClient{}
	tools.RegisterEconomyGeneratorTools(reg, mock)

	names := make(map[string]bool)
	for _, t := range reg.List() {
		names[t.Name] = true
	}
	assert.True(t, names["meridian_economy_generate_context"])
	assert.True(t, names["meridian_economy_generate"])
}

// --- meridian_economy_generate_context ---

func TestEconomyGenerateContext_MapsParamsToRequest(t *testing.T) {
	var capturedReq *controlplanev1.GetGenerationContextRequest
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			capturedReq = req
			return &controlplanev1.GetGenerationContextResponse{
				HandlerReferenceCard:  "handlers",
				TopicList:             "topics",
				ManifestSchemaSummary: "schema",
			}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	params := json.RawMessage(`{
		"description": "energy trading platform",
		"include_patterns": false,
		"include_current_economy": true,
		"tenant_id": "tenant-abc"
	}`)
	result, err := reg.Call(context.Background(), "meridian_economy_generate_context", params)
	require.NoError(t, err)

	assert.Equal(t, "energy trading platform", capturedReq.Description)
	assert.True(t, capturedReq.ExcludePatterns)
	assert.True(t, capturedReq.IncludeCurrentEconomy)
	assert.Equal(t, "tenant-abc", capturedReq.TenantId)

	m := result.(map[string]interface{})
	assert.Equal(t, "handlers", m["handler_reference_card"])
	assert.Equal(t, "topics", m["topic_list"])
	assert.Equal(t, "schema", m["manifest_schema_summary"])
}

func TestEconomyGenerateContext_IncludePatternsDefault_ExcludeFalse(t *testing.T) {
	var capturedReq *controlplanev1.GetGenerationContextRequest
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			capturedReq = req
			return &controlplanev1.GetGenerationContextResponse{}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	params := json.RawMessage(`{"description": "fintech ledger"}`)
	_, err := reg.Call(context.Background(), "meridian_economy_generate_context", params)
	require.NoError(t, err)

	// When include_patterns is not specified, exclude_patterns should be false (default = include).
	assert.False(t, capturedReq.ExcludePatterns)
}

func TestEconomyGenerateContext_MatchedPatterns_Included(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, _ *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			return &controlplanev1.GetGenerationContextResponse{
				MatchedPatterns: []*controlplanev1.PatternContext{
					{
						Name:             "energy_settlement",
						Title:            "Energy Settlement",
						Score:            0.92,
						ManifestFragment: "instruments:\n  - code: kWh",
						SagaScript:       "def run(): pass",
					},
				},
			}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate_context", json.RawMessage(`{"description": "energy"}`))
	require.NoError(t, err)

	m := result.(map[string]interface{})
	patterns, ok := m["matched_patterns"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, patterns, 1)
	assert.Equal(t, "energy_settlement", patterns[0]["name"])
	assert.Equal(t, "Energy Settlement", patterns[0]["title"])
	assert.Equal(t, 0.92, patterns[0]["score"])
	assert.Equal(t, "instruments:\n  - code: kWh", patterns[0]["manifest_fragment"])
	assert.Equal(t, "def run(): pass", patterns[0]["saga_script"])
}

func TestEconomyGenerateContext_CurrentEconomyYaml_IncludedWhenPresent(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, _ *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			return &controlplanev1.GetGenerationContextResponse{
				CurrentEconomyYaml: "version: 1.0\n",
			}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate_context", json.RawMessage(`{"description": "amend"}`))
	require.NoError(t, err)

	m := result.(map[string]interface{})
	assert.Equal(t, "version: 1.0\n", m["current_economy_yaml"])
}

func TestEconomyGenerateContext_IncludeCurrentEconomy_WithoutTenantID_ReturnsError(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, _ *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			t.Error("should not call RPC when tenant_id is missing")
			return nil, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate_context", json.RawMessage(`{
		"description": "energy",
		"include_current_economy": true
	}`))
	require.NoError(t, err)

	m, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, m["error"], "tenant_id")
}

func TestEconomyGenerateContext_GRPCError_ReturnsFormattedError(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		contextFn: func(_ context.Context, _ *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "description too short")
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate_context", json.RawMessage(`{"description": "x"}`))
	require.NoError(t, err)

	// Result should be non-nil (a FormattedError from mcperrors).
	assert.NotNil(t, result)
}

// --- meridian_economy_generate ---

func TestEconomyGenerate_CreateMode_MapsParamsToRequest(t *testing.T) {
	var capturedReq *controlplanev1.GenerateManifestRequest
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			capturedReq = req
			return &controlplanev1.GenerateManifestResponse{
				ManifestYaml: "version: 1.0\n",
				Valid:        true,
				GenerationMetadata: &controlplanev1.GenerationMetadata{
					PatternsUsed:       []string{"payment"},
					InstrumentsCreated: []string{"GBP"},
					SagasCreated:       []string{"transfer"},
					FixIterations:      1,
					Decisions:          []string{"chose payment pattern"},
				},
			}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	params := json.RawMessage(`{
		"description": "simple payment system",
		"mode": "create",
		"preferences": {
			"industry": "fintech",
			"instruments": ["GBP", "USD"],
			"patterns": ["payment"]
		},
		"max_fix_iterations": 2
	}`)

	result, err := reg.Call(context.Background(), "meridian_economy_generate", params)
	require.NoError(t, err)

	assert.Equal(t, "simple payment system", capturedReq.Description)
	assert.Equal(t, controlplanev1.GenerationMode_GENERATION_MODE_CREATE, capturedReq.Mode)
	assert.Equal(t, int32(2), capturedReq.MaxFixIterations)
	require.NotNil(t, capturedReq.Preferences)
	assert.Equal(t, "fintech", capturedReq.Preferences.Industry)
	assert.Equal(t, []string{"GBP", "USD"}, capturedReq.Preferences.Instruments)
	assert.Equal(t, []string{"payment"}, capturedReq.Preferences.Patterns)

	m := result.(map[string]interface{})
	assert.Equal(t, "version: 1.0\n", m["manifest_yaml"])
	assert.Equal(t, true, m["valid"])

	meta, ok := m["generation_metadata"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, int32(1), meta["fix_iterations"])
	assert.Equal(t, []string{"payment"}, meta["patterns_used"])
	assert.Equal(t, []string{"GBP"}, meta["instruments_created"])
	assert.Equal(t, []string{"transfer"}, meta["sagas_created"])
	assert.Equal(t, []string{"chose payment pattern"}, meta["decisions"])
}

func TestEconomyGenerate_AmendMode_SetsCorrectProtoMode(t *testing.T) {
	var capturedReq *controlplanev1.GenerateManifestRequest
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			capturedReq = req
			return &controlplanev1.GenerateManifestResponse{Valid: true}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	params := json.RawMessage(`{
		"description": "add carbon credits",
		"mode": "amend",
		"tenant_id": "tenant-xyz"
	}`)

	_, err := reg.Call(context.Background(), "meridian_economy_generate", params)
	require.NoError(t, err)

	assert.Equal(t, controlplanev1.GenerationMode_GENERATION_MODE_AMEND, capturedReq.Mode)
	assert.Equal(t, "tenant-xyz", capturedReq.TenantId)
}

func TestEconomyGenerate_AmendMode_WithoutTenantID_ReturnsError(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, _ *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			t.Error("should not call RPC when tenant_id is missing for amend")
			return nil, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{
		"description": "add carbon credits",
		"mode": "amend"
	}`))
	require.NoError(t, err)

	m, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, m["error"], "tenant_id")
}

func TestEconomyGenerate_UnknownMode_ReturnsError(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, _ *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			t.Error("should not call RPC for unknown mode")
			return nil, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	// JSON schema enum validation rejects invalid mode values before the handler runs.
	_, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{
		"description": "test",
		"mode": "replace"
	}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestEconomyGenerate_DefaultMode_IsCreate(t *testing.T) {
	var capturedReq *controlplanev1.GenerateManifestRequest
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			capturedReq = req
			return &controlplanev1.GenerateManifestResponse{Valid: true}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	_, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{"description": "no mode specified"}`))
	require.NoError(t, err)

	assert.Equal(t, controlplanev1.GenerationMode_GENERATION_MODE_CREATE, capturedReq.Mode)
}

func TestEconomyGenerate_ValidationErrors_IncludedInResponse(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, _ *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			return &controlplanev1.GenerateManifestResponse{
				ManifestYaml: "",
				Valid:        false,
				ValidationErrors: []*controlplanev1.ValidationError{
					{Message: "missing instruments section", Path: "instruments", Code: "REQUIRED"},
				},
				ValidationWarnings: []*controlplanev1.ValidationError{
					{Message: "no sagas defined", Severity: "warning"},
				},
			}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{"description": "incomplete"}`))
	require.NoError(t, err)

	m := result.(map[string]interface{})
	assert.Equal(t, false, m["valid"])

	errs, ok := m["validation_errors"].([]interface{})
	require.True(t, ok)
	require.Len(t, errs, 1)
	e := errs[0].(map[string]interface{})
	assert.Equal(t, "missing instruments section", e["message"])
	assert.Equal(t, "instruments", e["path"])
	assert.Equal(t, "REQUIRED", e["code"])

	warns, ok := m["validation_warnings"].([]interface{})
	require.True(t, ok)
	require.Len(t, warns, 1)
}

func TestEconomyGenerate_NoPreferences_NilPreferencesInRequest(t *testing.T) {
	var capturedReq *controlplanev1.GenerateManifestRequest
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			capturedReq = req
			return &controlplanev1.GenerateManifestResponse{Valid: true}, nil
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	_, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{"description": "minimal"}`))
	require.NoError(t, err)

	assert.Nil(t, capturedReq.Preferences)
}

func TestEconomyGenerate_GRPCError_ReturnsFormattedError(t *testing.T) {
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, _ *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			return nil, status.Error(codes.Internal, "LLM unavailable")
		},
	}

	reg := tools.NewRegistry()
	tools.RegisterEconomyGeneratorTools(reg, mock)

	result, err := reg.Call(context.Background(), "meridian_economy_generate", json.RawMessage(`{"description": "error case"}`))
	require.NoError(t, err)

	// Result should be non-nil (a FormattedError from mcperrors).
	assert.NotNil(t, result)
}
