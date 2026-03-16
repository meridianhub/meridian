// Package applier provides the ApplyManifest gRPC handler that orchestrates
// manifest validation, diffing, planning, and execution.
package applier

import (
	"errors"
	"fmt"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"google.golang.org/protobuf/proto"
)

// ErrResourceTypeMismatch is returned when the resource payload does not
// match the declared resource_type in the request.
var ErrResourceTypeMismatch = errors.New("resource payload does not match declared resource_type")

// ErrNoCurrentManifest is returned when ApplyResource is called but no
// manifest has been applied yet (nothing to patch into).
var ErrNoCurrentManifest = errors.New("no current manifest exists; use ApplyManifest for the initial apply")

// ErrUnsupportedResourceType is returned when the resource type is not supported.
var ErrUnsupportedResourceType = errors.New("unsupported resource type")

// ErrCloneManifest is returned when proto.Clone fails to produce a *Manifest.
var ErrCloneManifest = errors.New("failed to clone manifest")

// resourceID extracts the stable identifier from the resource payload in the
// request. Returns the code/name/key used by the differ to identify the resource.
func resourceID(req *controlplanev1.ApplyResourceRequest) string {
	switch v := req.GetResource().(type) {
	case *controlplanev1.ApplyResourceRequest_Instrument:
		return v.Instrument.GetCode()
	case *controlplanev1.ApplyResourceRequest_AccountType:
		return v.AccountType.GetCode()
	case *controlplanev1.ApplyResourceRequest_ValuationRule:
		return valRuleKey(v.ValuationRule.GetFromInstrument(), v.ValuationRule.GetToInstrument())
	case *controlplanev1.ApplyResourceRequest_Saga:
		return v.Saga.GetName()
	case *controlplanev1.ApplyResourceRequest_PartyType:
		return partyTypeKey(v.PartyType.GetTenantId(), v.PartyType.GetPartyType())
	case *controlplanev1.ApplyResourceRequest_Mapping:
		return mappingKey(v.Mapping.GetName(), v.Mapping.GetVersion())
	case *controlplanev1.ApplyResourceRequest_ProviderConnection:
		return v.ProviderConnection.GetConnectionId()
	case *controlplanev1.ApplyResourceRequest_InstructionRoute:
		return v.InstructionRoute.GetInstructionType()
	case *controlplanev1.ApplyResourceRequest_MarketDataSource:
		return v.MarketDataSource.GetCode()
	case *controlplanev1.ApplyResourceRequest_MarketDataSet:
		return v.MarketDataSet.GetCode()
	case *controlplanev1.ApplyResourceRequest_Organization:
		return v.Organization.GetCode()
	case *controlplanev1.ApplyResourceRequest_InternalAccount:
		return v.InternalAccount.GetCode()
	default:
		return ""
	}
}

// valRuleKey produces a stable identifier for a valuation rule (from->to pair).
// Mirrors differ.valRuleKey — kept local to avoid an import cycle.
// Applies strings.ToUpper to match differ's normalization.
func valRuleKey(from, to string) string {
	return strings.ToUpper(from) + "->" + strings.ToUpper(to)
}

// partyTypeKey produces a stable identifier for a party type definition.
// Mirrors differ.partyTypeKey — kept local to avoid an import cycle.
func partyTypeKey(tenantID, partyType string) string {
	return tenantID + ":" + partyType
}

// mappingKey produces a stable identifier for a mapping definition.
// Mirrors differ.mappingKey — kept local to avoid an import cycle.
func mappingKey(name string, version int32) string {
	return fmt.Sprintf("%s:%d", name, version)
}

// patchResource creates a deep copy of the base manifest and patches the
// single resource from the request into it. If a resource with the same
// identifier already exists, it is replaced; otherwise it is appended.
//
// Returns the patched manifest. The base manifest is never modified.
func patchResource(base *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) (*controlplanev1.Manifest, error) {
	// Deep-copy so the caller's base is untouched.
	patched, ok := proto.Clone(base).(*controlplanev1.Manifest)
	if !ok {
		return nil, ErrCloneManifest
	}

	// resourcePatchers maps each resource type to its patching function.
	// This avoids a monolithic switch that triggers cognitive-complexity linters.
	patcher, exists := resourcePatchers[req.GetResourceType()]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedResourceType, req.GetResourceType())
	}
	if err := patcher(patched, req); err != nil {
		return nil, err
	}
	return patched, nil
}

// resourcePatcherFunc applies a single resource from the request into the manifest.
type resourcePatcherFunc func(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error

// resourcePatchers maps manifest resource types to their patching functions.
var resourcePatchers = map[controlplanev1.ManifestResourceType]resourcePatcherFunc{ //nolint:exhaustive // UNSPECIFIED is rejected by proto validation
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT:          patchInstrument,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE:        patchAccountType,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE:      patchValuationRule,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_SAGA:                patchSaga,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PARTY_TYPE:          patchPartyType,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MAPPING:             patchMapping,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PROVIDER_CONNECTION: patchProviderConnection,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUCTION_ROUTE:   patchInstructionRoute,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SOURCE:  patchMarketDataSource,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SET:     patchMarketDataSet,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ORGANIZATION:        patchOrganization,
	controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INTERNAL_ACCOUNT:    patchInternalAccount,
}

func patchInstrument(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	inst, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_Instrument)
	if !ok || inst.Instrument == nil {
		return ErrResourceTypeMismatch
	}
	patched.Instruments = upsertInSlice(patched.Instruments, inst.Instrument,
		func(a *controlplanev1.InstrumentDefinition) string { return a.GetCode() },
		inst.Instrument.GetCode())
	return nil
}

func patchAccountType(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	at, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_AccountType)
	if !ok || at.AccountType == nil {
		return ErrResourceTypeMismatch
	}
	patched.AccountTypes = upsertInSlice(patched.AccountTypes, at.AccountType,
		func(a *controlplanev1.AccountTypeDefinition) string { return a.GetCode() },
		at.AccountType.GetCode())
	return nil
}

func patchValuationRule(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	vr, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_ValuationRule)
	if !ok || vr.ValuationRule == nil {
		return ErrResourceTypeMismatch
	}
	key := valRuleKey(vr.ValuationRule.GetFromInstrument(), vr.ValuationRule.GetToInstrument())
	patched.ValuationRules = upsertInSlice(patched.ValuationRules, vr.ValuationRule,
		func(a *controlplanev1.ValuationRule) string {
			return valRuleKey(a.GetFromInstrument(), a.GetToInstrument())
		}, key)
	return nil
}

func patchSaga(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	s, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_Saga)
	if !ok || s.Saga == nil {
		return ErrResourceTypeMismatch
	}
	patched.Sagas = upsertInSlice(patched.Sagas, s.Saga,
		func(a *controlplanev1.SagaDefinition) string { return a.GetName() },
		s.Saga.GetName())
	return nil
}

func patchPartyType(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	pt, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_PartyType)
	if !ok || pt.PartyType == nil {
		return ErrResourceTypeMismatch
	}
	key := partyTypeKey(pt.PartyType.GetTenantId(), pt.PartyType.GetPartyType())
	patched.PartyTypes = upsertInSlice(patched.PartyTypes, pt.PartyType,
		func(a *partyv1.PartyTypeDefinition) string { return partyTypeKey(a.GetTenantId(), a.GetPartyType()) },
		key)
	return nil
}

func patchMapping(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	m, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_Mapping)
	if !ok || m.Mapping == nil {
		return ErrResourceTypeMismatch
	}
	key := mappingKey(m.Mapping.GetName(), m.Mapping.GetVersion())
	patched.Mappings = upsertInSlice(patched.Mappings, m.Mapping,
		func(a *mappingv1.MappingDefinition) string { return mappingKey(a.GetName(), a.GetVersion()) },
		key)
	return nil
}

func patchProviderConnection(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	pc, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_ProviderConnection)
	if !ok || pc.ProviderConnection == nil {
		return ErrResourceTypeMismatch
	}
	if patched.OperationalGateway == nil {
		patched.OperationalGateway = &controlplanev1.OperationalGatewayConfig{}
	}
	patched.OperationalGateway.ProviderConnections = upsertInSlice(
		patched.OperationalGateway.ProviderConnections, pc.ProviderConnection,
		func(a *controlplanev1.ProviderConnectionConfig) string { return a.GetConnectionId() },
		pc.ProviderConnection.GetConnectionId())
	return nil
}

func patchInstructionRoute(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	ir, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_InstructionRoute)
	if !ok || ir.InstructionRoute == nil {
		return ErrResourceTypeMismatch
	}
	if patched.OperationalGateway == nil {
		patched.OperationalGateway = &controlplanev1.OperationalGatewayConfig{}
	}
	patched.OperationalGateway.InstructionRoutes = upsertInSlice(
		patched.OperationalGateway.InstructionRoutes, ir.InstructionRoute,
		func(a *controlplanev1.InstructionRouteConfig) string { return a.GetInstructionType() },
		ir.InstructionRoute.GetInstructionType())
	return nil
}

func patchMarketDataSource(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	mds, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_MarketDataSource)
	if !ok || mds.MarketDataSource == nil {
		return ErrResourceTypeMismatch
	}
	if patched.MarketData == nil {
		patched.MarketData = &controlplanev1.MarketDataConfig{}
	}
	patched.MarketData.Sources = upsertInSlice(patched.MarketData.Sources, mds.MarketDataSource,
		func(a *controlplanev1.MarketDataSourceDefinition) string { return a.GetCode() },
		mds.MarketDataSource.GetCode())
	return nil
}

func patchMarketDataSet(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	mds, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_MarketDataSet)
	if !ok || mds.MarketDataSet == nil {
		return ErrResourceTypeMismatch
	}
	if patched.MarketData == nil {
		patched.MarketData = &controlplanev1.MarketDataConfig{}
	}
	patched.MarketData.Datasets = upsertInSlice(patched.MarketData.Datasets, mds.MarketDataSet,
		func(a *controlplanev1.MarketDataSetDefinition) string { return a.GetCode() },
		mds.MarketDataSet.GetCode())
	return nil
}

func patchOrganization(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	org, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_Organization)
	if !ok || org.Organization == nil {
		return ErrResourceTypeMismatch
	}
	patched.Organizations = upsertInSlice(patched.Organizations, org.Organization,
		func(a *controlplanev1.OrganizationDefinition) string { return a.GetCode() },
		org.Organization.GetCode())
	return nil
}

func patchInternalAccount(patched *controlplanev1.Manifest, req *controlplanev1.ApplyResourceRequest) error {
	ia, ok := req.GetResource().(*controlplanev1.ApplyResourceRequest_InternalAccount)
	if !ok || ia.InternalAccount == nil {
		return ErrResourceTypeMismatch
	}
	patched.InternalAccounts = upsertInSlice(patched.InternalAccounts, ia.InternalAccount,
		func(a *controlplanev1.InternalAccountDefinition) string { return a.GetCode() },
		ia.InternalAccount.GetCode())
	return nil
}

// upsertInSlice replaces the element with matching key, or appends if not found.
func upsertInSlice[T any](slice []T, newElem T, keyFn func(T) string, key string) []T {
	for i, elem := range slice {
		if keyFn(elem) == key {
			slice[i] = newElem
			return slice
		}
	}
	return append(slice, newElem)
}
