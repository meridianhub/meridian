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
)

// mockManifestHistorian is a test double for ManifestHistorian.
type mockManifestHistorian struct {
	manifest *controlplanev1.Manifest
	err      error
}

func (m *mockManifestHistorian) GetCurrentManifest(_ context.Context) (*controlplanev1.Manifest, error) {
	return m.manifest, m.err
}

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
	cookbookFS := emptyFS()

	svc, err := generator.NewGeneratorService(reg, nil, cookbookFS, nil)
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
