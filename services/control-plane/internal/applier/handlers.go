package applier

import (
	"embed"
	"errors"
	"fmt"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// ErrNoValuationMethodService is returned when default_conversion_method is provided
// but no ValuationMethodService was configured in HandlerDependencies.
var ErrNoValuationMethodService = errors.New("no ValuationMethodService configured")

//go:embed handlers.yaml
var handlersYAMLFS embed.FS

// RegisterManifestHandlers registers all Starlark service bindings needed by
// the apply_manifest saga. These handlers adapt Starlark parameters to the
// Control Plane's downstream service calls.
//
// The apply_manifest saga calls handlers in four service namespaces:
//   - reference_data: RegisterInstrument, RegisterAccountType, RegisterValuationRule, RegisterSagaDefinition
//   - internal_account: Initiate
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
				Description:         "Register a new instrument definition in Reference Data service",
				Compensate:          "reference_data.delete_instrument",
				HasAutoCompensation: true,
				ProducesInstruments: []string{},
				ProtoRequestType:    (*referencedatav1.RegisterInstrumentRequest)(nil),
				ProtoResponseType:   (*referencedatav1.RegisterInstrumentResponse)(nil),
				Version:             1,
			},
		},
		// Reference Data - Account type registration (idempotent: CreateDraft + Activate)
		"reference_data.register_account_type": {
			handler: registerAccountTypeHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				Description:         "Register a new account type definition in Reference Data service",
				Compensate:          "reference_data.delete_account_type",
				HasAutoCompensation: true,
				ProducesInstruments: []string{},
				ProtoRequestType:    (*referencedatav1.CreateDraftRequest)(nil),
				ProtoResponseType:   (*referencedatav1.ActivateAccountTypeResponse)(nil),
				Version:             1,
			},
		},
		// Reference Data - Valuation rule registration
		"reference_data.register_valuation_rule": {
			handler: registerValuationRuleHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Register a valuation rule for cross-instrument conversion",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				Version:              1,
			},
		},
		// Reference Data - Saga definition registration
		"reference_data.register_saga_definition": {
			handler: registerSagaDefinitionHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Register a saga definition in the saga registry",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				Version:              1,
			},
		},
		// Internal Account - Account initiation
		"internal_account.initiate": {
			handler: initiateAccountHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Initiate a new internal account",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*internalaccountv1.InitiateInternalAccountRequest)(nil),
				ProtoResponseType:    (*internalaccountv1.InitiateInternalAccountResponse)(nil),
				Version:              1,
			},
		},
		// Compensation handlers
		"reference_data.delete_instrument": {
			handler: deleteInstrumentHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Delete an instrument (compensation for register)",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				Version:              1,
			},
		},
		"reference_data.delete_account_type": {
			handler: deleteAccountTypeHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Delete an account type (compensation for register)",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				Version:              1,
			},
		},
		// Operational Gateway - Provider Connection Management
		"operational_gateway.upsert_connection": {
			handler: upsertConnectionHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Create or update a provider connection configuration",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*opgatewayv1.UpsertConnectionRequest)(nil),
				ProtoResponseType:    (*opgatewayv1.UpsertConnectionResponse)(nil),
				Version:              1,
			},
		},
		// Operational Gateway - Instruction Route Management
		"operational_gateway.upsert_route": {
			handler: upsertRouteHandler(deps),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Create or update an instruction route configuration",
				CompensationStrategy: "none",
				ProducesInstruments:  []string{},
				ProtoRequestType:     (*opgatewayv1.UpsertRouteRequest)(nil),
				ProtoResponseType:    (*opgatewayv1.UpsertRouteResponse)(nil),
				Version:              1,
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
	// InternalAccount provides account provisioning.
	InternalAccount InternalAccountService
	// ValuationMethod provides UUID resolution for named valuation methods.
	// May be nil if no default_conversion_method resolution is needed.
	ValuationMethod ValuationMethodService
	// OperationalGateway provides provider connection and instruction route management.
	// May be nil if no operational_gateway section is present in the manifest.
	OperationalGateway OperationalGatewayService
}

// ReferenceDataService abstracts the Reference Data gRPC client for testing.
type ReferenceDataService interface {
	RegisterInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	DeleteInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	// RegisterAccountType creates an account type draft (idempotent via ON CONFLICT DO NOTHING)
	// and then activates it. Returns the registered code and version.
	RegisterAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	DeleteAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	RegisterValuationRule(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	RegisterSagaDefinition(ctx *saga.StarlarkContext, params map[string]any) (any, error)
}

// InternalAccountService abstracts the Internal Account gRPC client for testing.
type InternalAccountService interface {
	InitiateAccount(ctx *saga.StarlarkContext, params map[string]any) (any, error)
}

// ValuationMethodService resolves named valuation methods to their UUID and version.
// The manifest references methods by human-readable name (e.g., "forex-spot-v1");
// this service translates those to the UUID+version required by the AccountTypeRegistry.
type ValuationMethodService interface {
	// ResolveMethod looks up a valuation method by name and returns its UUID string and version.
	// Returns (uuid, version, suggestions, error) where suggestions is populated on miss.
	ResolveMethod(ctx *saga.StarlarkContext, name string) (id string, version int, suggestions []string, err error)
}

// OperationalGatewayService abstracts the Operational Gateway gRPC client for manifest apply.
// It provides idempotent upsert operations for provider connections and instruction routes.
type OperationalGatewayService interface {
	// UpsertConnection creates or updates a provider connection configuration.
	UpsertConnection(ctx *saga.StarlarkContext, params map[string]any) (any, error)
	// UpsertRoute creates or updates an instruction route configuration.
	UpsertRoute(ctx *saga.StarlarkContext, params map[string]any) (any, error)
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

// registerAccountTypeHandler creates a handler that idempotently registers an account type.
//
// Idempotency semantics:
//  1. Call CreateDraft on the AccountTypeRegistry (ON CONFLICT DO NOTHING if already exists).
//  2. Call ActivateAccountType on the result (idempotent if already ACTIVE).
//
// If default_conversion_method is provided as a string name, it is resolved to a UUID+version
// via the ValuationMethodService before calling CreateDraft. Unresolvable names produce a
// structured ValidationError with Levenshtein suggestions.
func registerAccountTypeHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// Resolve default_conversion_method name → UUID if provided
		if methodName, ok := params["default_conversion_method"].(string); ok && methodName != "" {
			if deps.ValuationMethod == nil {
				return nil, fmt.Errorf("default_conversion_method %q: %w", methodName, ErrNoValuationMethodService)
			}
			id, version, suggestions, err := deps.ValuationMethod.ResolveMethod(ctx, methodName)
			if err != nil {
				if len(suggestions) > 0 {
					return nil, fmt.Errorf("unresolvable default_conversion_method %q (did you mean: %v?): %w", methodName, suggestions, err)
				}
				return nil, fmt.Errorf("unresolvable default_conversion_method %q: %w", methodName, err)
			}
			// Replace the string name with resolved UUID and version in params copy
			params = copyParams(params)
			params["default_conversion_method_id"] = id
			params["default_conversion_method_version"] = version
			delete(params, "default_conversion_method")
		}

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

// initiateAccountHandler creates a handler that initiates an internal account.
func initiateAccountHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return deps.InternalAccount.InitiateAccount(ctx, params)
	}
}

// upsertConnectionHandler creates a handler that upserts a provider connection.
// Returns an error if OperationalGateway is nil to prevent silent skipping of
// gateway configuration during manifest apply.
func upsertConnectionHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		if deps.OperationalGateway == nil {
			return nil, ErrOperationalGatewayNotConfigured
		}
		return deps.OperationalGateway.UpsertConnection(ctx, params)
	}
}

// upsertRouteHandler creates a handler that upserts an instruction route.
// Returns an error if OperationalGateway is nil to prevent silent skipping of
// route configuration during manifest apply.
func upsertRouteHandler(deps *HandlerDependencies) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		if deps.OperationalGateway == nil {
			return nil, ErrOperationalGatewayNotConfigured
		}
		return deps.OperationalGateway.UpsertRoute(ctx, params)
	}
}

// copyParams creates a shallow copy of a params map to avoid mutating the original.
func copyParams(original map[string]any) map[string]any {
	cp := make(map[string]any, len(original))
	for k, v := range original {
		cp[k] = v
	}
	return cp
}
