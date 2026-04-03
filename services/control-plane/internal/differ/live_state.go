package differ

import (
	"context"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// LiveState holds the current state of all resource types queried from downstream services.
// It is the input to the differ's three-way comparison (last-applied vs desired vs live).
type LiveState struct {
	Instruments         []*controlplanev1.InstrumentDefinition
	AccountTypes        []*controlplanev1.AccountTypeDefinition
	Sagas               []*controlplanev1.SagaDefinition
	MarketDataSources   []*controlplanev1.MarketDataSourceDefinition
	MarketDataSets      []*controlplanev1.MarketDataSetDefinition
	Organizations       []*controlplanev1.OrganizationDefinition
	InternalAccounts    []*controlplanev1.InternalAccountDefinition
	ProviderConnections []*controlplanev1.ProviderConnectionConfig
	InstructionRoutes   []*controlplanev1.InstructionRouteConfig

	// NonActiveInstruments tracks instrument codes that exist but are not ACTIVE
	// (e.g., DEPRECATED). The diff uses this to force UPDATE instead of NO_CHANGE
	// so the saga re-activates them. The proto comparison doesn't include status,
	// so without this, DEPRECATED instruments appear as NO_CHANGE.
	NonActiveInstruments map[string]bool

	// SystemCodes tracks resources that are system-managed (is_system=true).
	// Outer key is ResourceType, inner key is the resource code/name.
	// Resources in this set are excluded from diff planning by filterTenantOwned.
	SystemCodes map[ResourceType]map[string]bool

	// PlatformRefs tracks resources that are tenant overrides of platform defaults
	// (is_system=false with a non-nil platform_ref in the service layer).
	// These are tenant-owned resources and must be included in diff planning.
	// Outer key is ResourceType, inner key is the resource code/name.
	PlatformRefs map[ResourceType]map[string]bool
}

// filterTenantOwned returns a copy of live with platform default resources removed.
// Resources present in SystemCodes (is_system=true) are excluded from all resource slices.
// Resources present in PlatformRefs (is_system=false with platform_ref) are tenant overrides
// and are naturally retained - they are not in SystemCodes and pass through unchanged.
// This function must be called before DiffAgainstLiveState to ensure system resources
// do not appear as CREATE or UPDATE actions.
func filterTenantOwned(live *LiveState) *LiveState {
	if live == nil {
		return nil
	}
	return &LiveState{
		SystemCodes:  live.SystemCodes,
		PlatformRefs: live.PlatformRefs,
		Instruments: filterBySystemCodes(live.Instruments, live.SystemCodes, ResourceInstrument,
			func(r *controlplanev1.InstrumentDefinition) string { return r.GetCode() }),
		AccountTypes: filterBySystemCodes(live.AccountTypes, live.SystemCodes, ResourceAccountType,
			func(r *controlplanev1.AccountTypeDefinition) string { return r.GetCode() }),
		Sagas: filterBySystemCodes(live.Sagas, live.SystemCodes, ResourceSaga,
			func(r *controlplanev1.SagaDefinition) string { return r.GetName() }),
		MarketDataSources: filterBySystemCodes(live.MarketDataSources, live.SystemCodes, ResourceMarketDataSource,
			func(r *controlplanev1.MarketDataSourceDefinition) string { return r.GetCode() }),
		MarketDataSets: filterBySystemCodes(live.MarketDataSets, live.SystemCodes, ResourceMarketDataSet,
			func(r *controlplanev1.MarketDataSetDefinition) string { return r.GetCode() }),
		Organizations: filterBySystemCodes(live.Organizations, live.SystemCodes, ResourceOrganization,
			func(r *controlplanev1.OrganizationDefinition) string { return r.GetCode() }),
		InternalAccounts: filterBySystemCodes(live.InternalAccounts, live.SystemCodes, ResourceInternalAccount,
			func(r *controlplanev1.InternalAccountDefinition) string { return r.GetCode() }),
		ProviderConnections: filterBySystemCodes(live.ProviderConnections, live.SystemCodes, ResourceProviderConnection,
			func(r *controlplanev1.ProviderConnectionConfig) string { return r.GetConnectionId() }),
		InstructionRoutes: filterBySystemCodes(live.InstructionRoutes, live.SystemCodes, ResourceInstructionRoute,
			func(r *controlplanev1.InstructionRouteConfig) string { return r.GetInstructionType() }),
	}
}

// filterBySystemCodes returns a slice with system-managed resources removed.
// Items whose code appears in systemCodes[rt] are excluded.
func filterBySystemCodes[T any](items []T, systemCodes map[ResourceType]map[string]bool, rt ResourceType, codeOf func(T) string) []T {
	if len(systemCodes) == 0 {
		return items
	}
	codes := systemCodes[rt]
	if len(codes) == 0 {
		return items
	}
	result := make([]T, 0, len(items))
	for _, item := range items {
		if !codes[codeOf(item)] {
			result = append(result, item)
		}
	}
	return result
}

// LiveStateProvider queries downstream services and returns the current live state
// for all resource types. Implementations must return an error if any service query
// fails - partial state is not acceptable for diff correctness.
type LiveStateProvider interface {
	QueryLiveState(ctx context.Context, tenantID string) (*LiveState, error)
}
