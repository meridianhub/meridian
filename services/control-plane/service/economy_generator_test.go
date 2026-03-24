package service

import (
	"context"
	"testing"
	"testing/fstest"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// EconomyGeneratorConfig field tests
// ---------------------------------------------------------------------------

func TestEconomyGeneratorConfig_Defaults(t *testing.T) {
	cfg := EconomyGeneratorConfig{}
	assert.Nil(t, cfg.SchemaRegistry)
	assert.Nil(t, cfg.ManifestHistory)
	assert.Nil(t, cfg.CookbookFS)
	assert.Nil(t, cfg.LLMClient)
	assert.Nil(t, cfg.Validator)
	assert.Nil(t, cfg.Logger)
}

func TestEconomyGeneratorConfig_FieldAssignment(t *testing.T) {
	reg := schema.NewRegistry()
	llm := &stubLLMClient{}
	val := &stubManifestValidator{}
	historian := &stubManifestHistorian{}
	cookbookFS := fstest.MapFS{}

	cfg := EconomyGeneratorConfig{
		SchemaRegistry:  reg,
		LLMClient:       llm,
		Validator:       val,
		ManifestHistory: historian,
		CookbookFS:      cookbookFS,
	}

	assert.Equal(t, reg, cfg.SchemaRegistry)
	assert.Equal(t, llm, cfg.LLMClient)
	assert.Equal(t, val, cfg.Validator)
	assert.Equal(t, historian, cfg.ManifestHistory)
	assert.Equal(t, cookbookFS, cfg.CookbookFS)
}

// ---------------------------------------------------------------------------
// RegisterEconomyGeneratorService - configuration combinations
// ---------------------------------------------------------------------------

func TestRegisterEconomyGeneratorService_NilRegistryError(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema registry is required")
}

func TestRegisterEconomyGeneratorService_OnlyRegistry(t *testing.T) {
	// All optional fields nil - should succeed with a valid registry.
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_WithManifestHistory(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry:  schema.NewRegistry(),
		ManifestHistory: &stubManifestHistorian{},
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_WithCookbookFS(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	cookbookFS := fstest.MapFS{
		"patterns/simple.yaml": &fstest.MapFile{Data: []byte("name: simple\n")},
	}

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
		CookbookFS:     cookbookFS,
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_LLMClientWithoutValidator(t *testing.T) {
	// LLMClient provided but Validator is nil - valid at registration time;
	// GenerateManifest will fail at call time but registration succeeds.
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
		LLMClient:      &stubLLMClient{},
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_ValidatorWithoutLLMClient(t *testing.T) {
	// Validator provided but LLMClient is nil - also valid at registration.
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
		Validator:      &stubManifestValidator{},
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_AllFields(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry:  schema.NewRegistry(),
		ManifestHistory: &stubManifestHistorian{},
		CookbookFS:      fstest.MapFS{},
		LLMClient:       &stubLLMClient{},
		Validator:       &stubManifestValidator{},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Stub types (local to economy_generator_test.go)
// ---------------------------------------------------------------------------

// stubLLMClient satisfies generator.LLMClient.
type stubLLMClient struct{}

func (s *stubLLMClient) Generate(_ context.Context, _ string) (string, error) { return "", nil }
func (s *stubLLMClient) Fix(_ context.Context, _ string, _ []generator.ValidationError) (string, error) {
	return "", nil
}

// stubManifestValidator satisfies generator.ManifestValidator.
type stubManifestValidator struct{}

func (s *stubManifestValidator) ValidateDryRun(_ context.Context, _ string) (*generator.ValidationResult, error) {
	return &generator.ValidationResult{Valid: true}, nil
}

// stubManifestHistorian satisfies generator.ManifestHistorian.
type stubManifestHistorian struct {
	manifest *controlplanev1.Manifest
	err      error
}

func (s *stubManifestHistorian) GetCurrentManifest(_ context.Context) (*controlplanev1.Manifest, error) {
	return s.manifest, s.err
}
