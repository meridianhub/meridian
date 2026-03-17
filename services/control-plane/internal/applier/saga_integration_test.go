package applier

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/require"
)

// noopReferenceData implements ReferenceDataService with no-op handlers.
type noopReferenceData struct{}

func (n *noopReferenceData) RegisterInstrument(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"instrument_code": p["instrument_code"], "version": int32(1), "status": "DRAFT"}, nil
}

func (n *noopReferenceData) ActivateInstrument(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"instrument_code": p["instrument_code"], "status": "ACTIVE"}, nil
}

func (n *noopReferenceData) DeleteInstrument(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return nil, nil
}

func (n *noopReferenceData) RegisterAccountType(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"code": p["code"], "version": int32(1), "status": "ACTIVE"}, nil
}

func (n *noopReferenceData) DeleteAccountType(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return nil, nil
}

func (n *noopReferenceData) RegisterValuationRule(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return map[string]any{"status": "REGISTERED"}, nil
}

func (n *noopReferenceData) RegisterSagaDefinition(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
	return map[string]any{"status": "REGISTERED"}, nil
}

// noopInternalAccount implements InternalAccountService with no-op handlers.
type noopInternalAccount struct{}

func (n *noopInternalAccount) InitiateAccount(_ *saga.StarlarkContext, p map[string]any) (any, error) {
	return map[string]any{"account_id": "ia-uuid-1", "account_code": p["account_code"], "status": "ACTIVE"}, nil
}

// TestSagaScriptExecution verifies that the embedded apply_manifest saga script
// executes successfully with the registered manifest handlers and typed service modules.
func TestSagaScriptExecution(t *testing.T) {
	deps := &HandlerDependencies{
		ReferenceData:   &noopReferenceData{},
		InternalAccount: &noopInternalAccount{},
	}

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterManifestHandlers(registry, deps))

	modules, err := schema.BuildServiceModules(registry)
	require.NoError(t, err, "BuildServiceModules should succeed")

	t.Logf("Service modules: %v", modules.Keys())

	// Load the embedded saga script
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

	input := saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		PartyScope:      &saga.PartyScope{TenantID: "test-tenant"},
		Input: map[string]any{
			"manifest_version": "1.0",
			"instruments": []any{
				map[string]any{"code": "GBP", "display_name": "British Pound", "dimension": "CURRENCY", "decimal_places": 2},
				map[string]any{"code": "KWH", "display_name": "Kilowatt Hour", "dimension": "CURRENCY", "decimal_places": 3},
				map[string]any{"code": "CARBON_CREDIT", "display_name": "Carbon Credit", "dimension": "CURRENCY", "decimal_places": 4},
			},
			"account_types":        []any{},
			"valuation_rules":      []any{},
			"organizations":        []any{},
			"internal_accounts":    []any{},
			"saga_definitions":     []any{},
			"provider_connections": []any{},
			"instruction_routes":   []any{},
			"market_data_sources":  []any{},
			"market_data_sets":     []any{},
		},
	}

	output, err := runner.ExecuteSaga(context.Background(), "apply_manifest", string(scriptBytes), input)
	require.NoError(t, err, "ExecuteSaga should not return error")
	require.True(t, output.Success, "saga should succeed: %s", output.Error)
}
