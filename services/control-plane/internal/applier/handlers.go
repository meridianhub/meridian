package applier

import (
	"embed"
	"fmt"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

//go:embed handlers.yaml
var handlersYAMLFS embed.FS

// RegisterManifestHandlers registers all Starlark service bindings needed by
// the apply_manifest saga. These handlers adapt Starlark parameters to the
// Control Plane's downstream service calls.
//
// The apply_manifest saga calls handlers in four service namespaces:
//   - reference_data: RegisterInstrument, RegisterAccountType, RegisterValuationRule, RegisterSagaDefinition
//   - internal_bank_account: Initiate
//
// Each handler is registered with metadata for compensation support.
func RegisterManifestHandlers(registry *saga.HandlerRegistry, deps *HandlerDependencies) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		// Reference Data - Instrument registration
		"reference_data.register_instrument": {
			handler: registerInstrumentHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		// Reference Data - Account type registration
		"reference_data.register_account_type": {
			handler: registerAccountTypeHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		// Reference Data - Valuation rule registration
		"reference_data.register_valuation_rule": {
			handler: registerValuationRuleHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		// Reference Data - Saga definition registration
		"reference_data.register_saga_definition": {
			handler: registerSagaDefinitionHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		// Internal Bank Account - Account initiation
		"internal_bank_account.initiate": {
			handler: initiateAccountHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		// Compensation handlers
		"reference_data.delete_instrument": {
			handler: deleteInstrumentHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		"reference_data.delete_account_type": {
			handler: deleteAccountTypeHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
	}

	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

// HandlerDependencies holds the service clients needed by manifest handlers.
// These are injected at startup and provide gRPC connectivity to downstream services.
type HandlerDependencies struct {
	// ReferenceData provides instrument, account type, valuation rule, and saga management.
	ReferenceData ReferenceDataService
	// InternalBankAccount provides account provisioning.
	InternalBankAccount InternalBankAccountService
}

// ReferenceDataService abstracts the Reference Data gRPC client for testing.
type ReferenceDataService interface {
	RegisterInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	DeleteInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	RegisterAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	DeleteAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	RegisterValuationRule(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	RegisterSagaDefinition(ctx *saga.StarlarkContext, params map[string]any) (any, error)
}

// InternalBankAccountService abstracts the Internal Bank Account gRPC client for testing.
type InternalBankAccountService interface {
	InitiateAccount(ctx *saga.StarlarkContext, params map[string]any) (any, error)
}

// registerInstrumentHandler creates a handler that registers an instrument via Reference Data.
func registerInstrumentHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.RegisterInstrument(ctx, params)
	}
}

// deleteInstrumentHandler creates a compensation handler that removes an instrument.
func deleteInstrumentHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.DeleteInstrument(ctx, params)
	}
}

// registerAccountTypeHandler creates a handler that registers an account type.
func registerAccountTypeHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.RegisterAccountType(ctx, params)
	}
}

// deleteAccountTypeHandler creates a compensation handler that removes an account type.
func deleteAccountTypeHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.DeleteAccountType(ctx, params)
	}
}

// registerValuationRuleHandler creates a handler that registers a valuation rule.
func registerValuationRuleHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.RegisterValuationRule(ctx, params)
	}
}

// registerSagaDefinitionHandler creates a handler that registers a saga definition.
func registerSagaDefinitionHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.ReferenceData.RegisterSagaDefinition(ctx, params)
	}
}

// initiateAccountHandler creates a handler that initiates an internal bank account.
func initiateAccountHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.InternalBankAccount.InitiateAccount(ctx, params)
	}
}
