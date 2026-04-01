package validator

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Orphan detection ────────────────────────────────────────────────────────

func TestValidateOperationalGatewayOrphans_NilGateway(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{}
	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)
	assert.Empty(t, result.Warnings)
}

func TestDetectOrphanProviderConnections_ReferencedByRoute(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "CHARGE", ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)
	// Provider connection is referenced by the instruction route, so no ORPHAN_PROVIDER_CONNECTION warning.
	orphanConns := filterValidationErrors(result.Warnings, "ORPHAN_PROVIDER_CONNECTION")
	assert.Empty(t, orphanConns)
}

func TestDetectOrphanProviderConnections_ReferencedByWebhook(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "on_payment", Trigger: "webhook:stripe_connect"},
		},
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)
	assert.Empty(t, result.Warnings)
}

func TestDetectOrphanProviderConnections_Orphan(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "unused_provider"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)

	require.Len(t, result.Warnings, 1)
	assert.Equal(t, "ORPHAN_PROVIDER_CONNECTION", result.Warnings[0].Code)
	assert.Contains(t, result.Warnings[0].Message, "unused_provider")
}

func TestDetectOrphanProviderConnections_FallbackConnection(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "primary"},
				{ConnectionId: "fallback"},
			},
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "CHARGE", ConnectionId: "primary", FallbackConnectionId: "fallback"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)
	// Both provider connections are referenced (primary + fallback), so no ORPHAN_PROVIDER_CONNECTION warnings.
	orphanConns := filterValidationErrors(result.Warnings, "ORPHAN_PROVIDER_CONNECTION")
	assert.Empty(t, orphanConns)
}

func TestDetectOrphanInstructionRoutes_UsedInSaga(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "charge_customer",
				Trigger: "api:/v1/charge",
				Script:  `def execute(ctx):\n    dispatch_instruction("CHARGE", {})\n`,
			},
		},
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "CHARGE", ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)
	assert.Empty(t, result.Warnings)
}

func TestDetectOrphanInstructionRoutes_NotUsed(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_order",
				Trigger: "api:/v1/orders",
				Script:  "def execute(ctx):\n    return {}\n",
			},
		},
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
			InstructionRoutes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "REFUND", ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateOperationalGatewayOrphans(manifest, result)

	// Expect one warning: unused provider connection + one warning: unused instruction route
	// The provider connection is referenced by the instruction route, so only instruction route is orphan
	orphanRoutes := filterValidationErrors(result.Warnings, "ORPHAN_INSTRUCTION_ROUTE")
	require.Len(t, orphanRoutes, 1)
	assert.Contains(t, orphanRoutes[0].Message, "REFUND")
}

// ─── Immutability checks (now a no-op; removals caught by destructive changes) ─

func TestValidateImmutability_IsNoOp(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// validateImmutability is intentionally a no-op. Different instrument/account
	// compositions between manifests should not produce false "code changed" errors.
	// Removals are caught by validateDestructiveChanges instead.
	previous := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
			{Code: "EUR", Name: "Euro"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SETTLEMENT", Name: "Settlement"},
		},
	}
	current := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound"},
			{Code: "KWH", Name: "Kilowatt Hour"},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "ENERGY_TRADING", Name: "Energy Trading"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateImmutability(current, previous, result)
	assert.Empty(t, result.Errors, "validateImmutability should be a no-op")
}

// ─── dispatchInstructionRegex ────────────────────────────────────────────────

func TestDispatchInstructionRegex_PositionalArg(t *testing.T) {
	script := `dispatch_instruction("CHARGE", {})`
	matches := dispatchInstructionRegex.FindAllStringSubmatch(script, -1)
	require.Len(t, matches, 1)
	assert.Equal(t, "CHARGE", matches[0][1])
}

func TestDispatchInstructionRegex_KeywordArg(t *testing.T) {
	script := `dispatch_instruction(instruction_type="REFUND", payload={})`
	matches := dispatchInstructionRegex.FindAllStringSubmatch(script, -1)
	require.Len(t, matches, 1)
	assert.Equal(t, "REFUND", matches[0][1])
}

func TestDispatchInstructionRegex_MultipleInstructions(t *testing.T) {
	script := `
def execute(ctx):
    dispatch_instruction('CHARGE', {})
    dispatch_instruction('NOTIFY', {})
`
	matches := dispatchInstructionRegex.FindAllStringSubmatch(script, -1)
	require.Len(t, matches, 2)
	types := []string{matches[0][1], matches[1][1]}
	assert.Contains(t, types, "CHARGE")
	assert.Contains(t, types, "NOTIFY")
}

func TestDispatchInstructionRegex_NoMatch(t *testing.T) {
	script := `def execute(ctx):\n    return {}\n`
	matches := dispatchInstructionRegex.FindAllStringSubmatch(script, -1)
	assert.Empty(t, matches)
}

// filterValidationErrors filters warnings by error code.
func filterValidationErrors(errs []ValidationError, code string) []ValidationError {
	var out []ValidationError
	for _, e := range errs {
		if e.Code == code {
			out = append(out, e)
		}
	}
	return out
}
