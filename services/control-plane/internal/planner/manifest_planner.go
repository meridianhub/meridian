package planner

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
)

// ErrNilDiffPlan is returned when the diff plan is nil.
var ErrNilDiffPlan = errors.New("diff plan cannot be nil")

// ErrNoMethodMapping is returned when no gRPC method mapping exists for a resource type/action combination.
var ErrNoMethodMapping = errors.New("no gRPC method mapping")

// ErrDeleteNotSupportedForPartyType is returned when a DELETE action is attempted on a party type.
// Party types are managed through schema updates; deletion via manifest apply is not supported.
var ErrDeleteNotSupportedForPartyType = errors.New("delete not supported for party types: update the schema instead")

// ManifestPlanner transforms a DiffPlan into a dependency-ordered
// ExecutionPlan of gRPC calls. It assigns each action to a phase
// based on resource type dependencies and maps actions to the
// appropriate gRPC service methods.
type ManifestPlanner struct{}

// NewManifestPlanner creates a new ManifestPlanner.
func NewManifestPlanner() *ManifestPlanner {
	return &ManifestPlanner{}
}

// Plan transforms a DiffPlan into an ordered ExecutionPlan.
// Only actionable changes (CREATE/UPDATE/DELETE) are included;
// NO_CHANGE actions are filtered out.
func (p *ManifestPlanner) Plan(diffPlan *differ.DiffPlan, tenantID, manifestVersion string, dryRun bool) (*ExecutionPlan, error) {
	if diffPlan == nil {
		return nil, ErrNilDiffPlan
	}

	plan := &ExecutionPlan{
		TenantID:        tenantID,
		ManifestVersion: manifestVersion,
		DryRun:          dryRun,
	}

	for _, action := range diffPlan.Actions {
		if action.Action == differ.ActionNoChange {
			continue
		}

		call, err := p.mapToCall(action, tenantID, manifestVersion, dryRun)
		if err != nil {
			return nil, fmt.Errorf("mapping action %s %s: %w", action.ResourceType, action.ResourceCode, err)
		}

		plan.Calls = append(plan.Calls, call)
	}

	// Sort by phase, then resource type, then resource code for determinism.
	sort.Slice(plan.Calls, func(i, j int) bool {
		if plan.Calls[i].Phase != plan.Calls[j].Phase {
			return plan.Calls[i].Phase < plan.Calls[j].Phase
		}
		if plan.Calls[i].ResourceType != plan.Calls[j].ResourceType {
			return plan.Calls[i].ResourceType < plan.Calls[j].ResourceType
		}
		return plan.Calls[i].ResourceID < plan.Calls[j].ResourceID
	})

	return plan, nil
}

// mapToCall converts a single PlannedAction from the differ into a PlannedCall
// with the correct phase assignment and gRPC method mapping.
func (p *ManifestPlanner) mapToCall(action differ.PlannedAction, tenantID, manifestVersion string, dryRun bool) (PlannedCall, error) {
	phase := phaseForResource(action.ResourceType)
	method, err := grpcMethodFor(action.ResourceType, action.Action)
	if err != nil {
		return PlannedCall{}, err
	}

	return PlannedCall{
		Phase:          phase,
		ResourceType:   action.ResourceType,
		ResourceID:     action.ResourceCode,
		Action:         action.Action,
		GRPCMethod:     method,
		IdempotencyKey: GenerateIdempotencyKey(tenantID, manifestVersion, action.ResourceType, action.ResourceCode, action.Action),
		Description:    action.Description,
		DryRun:         dryRun,
	}, nil
}

// phaseForResource returns the execution phase for a given resource type.
func phaseForResource(rt differ.ResourceType) Phase {
	switch rt {
	case differ.ResourceInstrument:
		return PhaseInstruments
	case differ.ResourceAccountType:
		return PhaseAccountTypes
	case differ.ResourceValuationRule:
		return PhaseValuationRules
	case differ.ResourceSaga:
		return PhaseSagas
	case differ.ResourcePartyType:
		return PhasePartyTypes
	case differ.ResourceMapping:
		return PhaseMappings
	case differ.ResourceProviderConnection:
		return PhaseOperationalGateway
	case differ.ResourceInstructionRoute:
		return PhaseOperationalGateway
	case differ.ResourceMarketDataSource:
		return PhaseMarketDataSources
	case differ.ResourceMarketDataSet:
		return PhaseMarketDataSets
	case differ.ResourceOrganization:
		return PhaseOrganizations
	case differ.ResourceInternalAccount:
		return PhaseInternalAccounts
	default:
		return PhaseSeedData
	}
}

// grpcMethodFor returns the gRPC method for a resource type and action.
func grpcMethodFor(rt differ.ResourceType, action differ.ActionType) (GRPCMethod, error) {
	if rt == differ.ResourcePartyType && action == differ.ActionDelete {
		return "", ErrDeleteNotSupportedForPartyType
	}
	key := methodKey{rt, action}
	method, ok := grpcMethodMap[key]
	if !ok {
		return "", fmt.Errorf("%w for %s/%s", ErrNoMethodMapping, rt, action)
	}
	return method, nil
}

type methodKey struct {
	resourceType differ.ResourceType
	actionType   differ.ActionType
}

// grpcMethodMap maps (resource type, action) pairs to gRPC methods.
var grpcMethodMap = map[methodKey]GRPCMethod{
	// Instruments
	{differ.ResourceInstrument, differ.ActionCreate}: MethodRegisterInstrument,
	{differ.ResourceInstrument, differ.ActionUpdate}: MethodUpdateInstrument,
	{differ.ResourceInstrument, differ.ActionDelete}: MethodDeprecateInstrument,

	// Account Types: mapped to Reference Data AccountTypeRegistryService.
	// CREATE → CreateDraft (the handler calls Activate internally for idempotent flow).
	// UPDATE → UpdateDefinition.
	// DELETE → DeprecateAccountType.
	{differ.ResourceAccountType, differ.ActionCreate}: MethodCreateAccountTypeDraft,
	{differ.ResourceAccountType, differ.ActionUpdate}: MethodUpdateAccountTypeDefinition,
	{differ.ResourceAccountType, differ.ActionDelete}: MethodDeprecateAccountType,

	// Valuation Rules: mapped to Reference Data Service instrument operations
	// (valuation rules are registered alongside instrument definitions).
	{differ.ResourceValuationRule, differ.ActionCreate}: MethodRegisterInstrument,
	{differ.ResourceValuationRule, differ.ActionUpdate}: MethodUpdateInstrument,
	{differ.ResourceValuationRule, differ.ActionDelete}: MethodDeprecateInstrument,

	// Sagas
	{differ.ResourceSaga, differ.ActionCreate}: MethodCreateSagaDraft,
	{differ.ResourceSaga, differ.ActionUpdate}: MethodUpdateSagaDefinition,
	{differ.ResourceSaga, differ.ActionDelete}: MethodDeprecateSaga,

	// Party Types
	{differ.ResourcePartyType, differ.ActionCreate}: MethodRegisterPartyType,
	{differ.ResourcePartyType, differ.ActionUpdate}: MethodUpdatePartyType,
	// DELETE for party types is not supported (party types are managed through schema updates)
	// No delete method registered intentionally.

	// Mappings
	{differ.ResourceMapping, differ.ActionCreate}: MethodCreateMapping,
	{differ.ResourceMapping, differ.ActionUpdate}: MethodUpdateMapping,
	{differ.ResourceMapping, differ.ActionDelete}: MethodDeprecateMapping,

	// Provider Connections (Operational Gateway)
	{differ.ResourceProviderConnection, differ.ActionCreate}: MethodUpsertProviderConnection,
	{differ.ResourceProviderConnection, differ.ActionUpdate}: MethodUpsertProviderConnection,
	// No delete method: proto does not define DeleteConnection RPC.

	// Instruction Routes (Operational Gateway)
	{differ.ResourceInstructionRoute, differ.ActionCreate}: MethodUpsertInstructionRoute,
	{differ.ResourceInstructionRoute, differ.ActionUpdate}: MethodUpsertInstructionRoute,
	// No delete method: proto does not define DeleteRoute RPC.

	// Market Data Sources
	{differ.ResourceMarketDataSource, differ.ActionCreate}: MethodRegisterDataSource,
	{differ.ResourceMarketDataSource, differ.ActionUpdate}: MethodUpdateDataSource,
	{differ.ResourceMarketDataSource, differ.ActionDelete}: MethodDeactivateDataSource,

	// Market Data Sets
	{differ.ResourceMarketDataSet, differ.ActionCreate}: MethodRegisterDataSet,
	{differ.ResourceMarketDataSet, differ.ActionUpdate}: MethodUpdateDataSet,
	{differ.ResourceMarketDataSet, differ.ActionDelete}: MethodDeprecateDataSet,

	// Organizations
	{differ.ResourceOrganization, differ.ActionCreate}: MethodRegisterOrganization,
	{differ.ResourceOrganization, differ.ActionUpdate}: MethodRegisterOrganization,
	{differ.ResourceOrganization, differ.ActionDelete}: MethodControlOrganization,

	// Internal Accounts
	{differ.ResourceInternalAccount, differ.ActionCreate}: MethodInitiateAccount,
	{differ.ResourceInternalAccount, differ.ActionUpdate}: MethodUpdateInternalAccount,
	{differ.ResourceInternalAccount, differ.ActionDelete}: MethodControlInternalAccount,
}

// GenerateIdempotencyKey produces a deterministic SHA-256 based idempotency key.
// The key is derived from: tenant_id + manifest_version + resource_type + resource_id + action.
// This ensures the same operation on the same resource produces the same key,
// enabling safe retry without duplication.
func GenerateIdempotencyKey(tenantID, manifestVersion string, resourceType differ.ResourceType, resourceID string, action differ.ActionType) string {
	input := fmt.Sprintf("%s|%s|%s|%s|%s", tenantID, manifestVersion, resourceType, resourceID, action)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}
