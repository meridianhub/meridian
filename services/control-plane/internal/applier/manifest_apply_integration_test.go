package applier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

// noopMarketInformation implements MarketInformationService with no-op handlers.
type noopMarketInformation struct{}

func (n *noopMarketInformation) RegisterDataSource(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"code": p["code"], "status": "REGISTERED"}, nil
}

func (n *noopMarketInformation) RegisterDataSet(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"code": p["code"], "version": int32(1), "status": "DRAFT"}, nil
}

func (n *noopMarketInformation) ActivateDataSet(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"code": p["code"], "status": "ACTIVE"}, nil
}

// noopParty implements PartyService with no-op handlers.
type noopParty struct{}

func (n *noopParty) RegisterOrganization(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return map[string]any{"party_id": "party-uuid-1", "status": "ACTIVE"}, nil
}

// noopOperationalGateway implements OperationalGatewayService with no-op handlers.
type noopOperationalGateway struct{}

func (n *noopOperationalGateway) UpsertConnection(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return map[string]any{"status": "UPSERTED"}, nil
}

func (n *noopOperationalGateway) UpsertRoute(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return map[string]any{"status": "UPSERTED"}, nil
}

// TestApplyManifest_ExampleManifests validates that every example manifest in
// examples/manifests/ can be applied successfully through the full saga pipeline.
//
// This test catches:
//   - Missing required handler parameters (validation_expression, resolution_key_expression)
//   - Invalid enum values (DATA_CATEGORY_ prefix mismatches)
//   - Wrong result access patterns (.get vs getattr on Starlark structs)
//   - Field name mismatches between manifest proto and handler schema
//
// It uses noop service implementations so no database is needed — the test
// validates the entire conversion and execution path: proto → ApplyManifestInput
// → saga input map → Starlark execution → handler calls.
func TestApplyManifest_ExampleManifests(t *testing.T) {
	manifestDir := filepath.Join("..", "..", "..", "..", "examples", "manifests")
	entries, err := os.ReadDir(manifestDir)
	require.NoError(t, err, "should read examples/manifests/ directory")

	var manifests []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			manifests = append(manifests, e.Name())
		}
	}
	require.NotEmpty(t, manifests, "should find at least one example manifest")

	// Set up saga runner with all noop dependencies
	deps := &HandlerDependencies{
		ReferenceData:      &noopReferenceData{},
		InternalAccount:    &noopInternalAccount{},
		MarketInformation:  &noopMarketInformation{},
		Party:              &noopParty{},
		OperationalGateway: &noopOperationalGateway{},
	}

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterManifestHandlers(registry, deps))

	modules, err := schema.BuildServiceModules(registry)
	require.NoError(t, err, "BuildServiceModules should succeed")

	scriptBytes, err := os.ReadFile("defaults/apply_manifest/v1.3.0.star")
	require.NoError(t, err, "should read saga script")

	runtime, err := saga.NewRuntime(nil)
	require.NoError(t, err)

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: modules,
	})
	require.NoError(t, err)

	// Use a zero-value executor to access buildSagaInput
	executor := &ManifestExecutor{}

	for _, manifestFile := range manifests {
		t.Run(manifestFile, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(manifestDir, manifestFile))
			require.NoError(t, err)

			var manifest controlplanev1.Manifest
			require.NoError(t, protojson.Unmarshal(data, &manifest), "manifest should parse as valid proto")

			// Use the same conversion path as production:
			// proto → ApplyManifestInput → saga input map
			applyInput := buildExecutorInput(&manifest)
			sagaInput := executor.buildSagaInput(applyInput)

			runnerInput := saga.RunnerInput{
				SagaExecutionID: uuid.New(),
				CorrelationID:   uuid.New(),
				PartyScope:      &saga.PartyScope{TenantID: "test-tenant"},
				Input:           sagaInput,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			output, err := runner.ExecuteSaga(ctx, "apply_manifest", string(scriptBytes), runnerInput)
			require.NoError(t, err, "ExecuteSaga should not return error for %s", manifestFile)
			assert.True(t, output.Success, "saga should succeed for %s: %s", manifestFile, output.Error)
		})
	}
}
