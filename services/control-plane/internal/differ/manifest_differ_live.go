package differ

import (
	"context"
	"errors"
	"fmt"
	"sort"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/protobuf/proto"
)

// ErrNoLiveStateProvider is returned when DiffAgainstLiveState is called without a LiveStateProvider.
var ErrNoLiveStateProvider = errors.New("live state provider is required for DiffAgainstLiveState")

// DiffAgainstLiveState queries live state from downstream services and computes a diff plan
// by comparing live state against the desired manifest. Resources with is_system=true in the
// live state are excluded from the diff entirely. Resources in live but not in the manifest
// receive a DEPRECATE action instead of DELETE.
func (d *ManifestDiffer) DiffAgainstLiveState(ctx context.Context, tenantID string, manifest *controlplanev1.Manifest) (*DiffPlan, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	if d.liveState == nil {
		return nil, ErrNoLiveStateProvider
	}

	live, err := d.liveState.QueryLiveState(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query live state: %w", err)
	}

	live = filterTenantOwned(live)

	plan := &DiffPlan{}

	d.diffInstrumentsAgainstLive(live, manifest, plan)
	d.diffAccountTypesAgainstLive(live, manifest, plan)
	d.diffSagasAgainstLive(live, manifest, plan)
	d.diffMarketDataSourcesAgainstLive(live, manifest, plan)
	d.diffMarketDataSetsAgainstLive(live, manifest, plan)
	d.diffOrganizationsAgainstLive(live, manifest, plan)
	d.diffInternalAccountsAgainstLive(live, manifest, plan)
	d.diffProviderConnectionsAgainstLive(live, manifest, plan)
	d.diffInstructionRoutesAgainstLive(live, manifest, plan)

	sort.Slice(plan.Actions, func(i, j int) bool {
		if plan.Actions[i].ResourceType != plan.Actions[j].ResourceType {
			return plan.Actions[i].ResourceType < plan.Actions[j].ResourceType
		}
		return plan.Actions[i].ResourceCode < plan.Actions[j].ResourceCode
	})

	return plan, nil
}


func (d *ManifestDiffer) diffInstrumentsAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.InstrumentDefinition)
	for _, inst := range live.Instruments {
		liveMap[inst.GetCode()] = inst
	}
	desiredMap := instrumentMap(manifest.GetInstruments())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create instrument %s (%s)", code, desired.GetName()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeInstrumentChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Instrument %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate instrument %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffAccountTypesAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.AccountTypeDefinition)
	for _, at := range live.AccountTypes {
		liveMap[at.GetCode()] = at
	}
	desiredMap := accountTypeMap(manifest.GetAccountTypes())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create account type %s (%s)", code, desired.GetName()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeAccountTypeChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Account type %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate account type %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffSagasAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.SagaDefinition)
	for _, s := range live.Sagas {
		liveMap[s.GetName()] = s
	}
	desiredMap := sagaMap(manifest.GetSagas())

	for name, desired := range desiredMap {
		existing, exists := liveMap[name]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create saga %s (trigger: %s)", name, desired.GetTrigger()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionUpdate,
				Description:  describeSagaChanges(name, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Saga %s unchanged", name),
			})
		}
	}

	for name := range liveMap {
		if _, exists := desiredMap[name]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate saga %s", name),
			})
		}
	}
}

func (d *ManifestDiffer) diffMarketDataSourcesAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.MarketDataSourceDefinition)
	for _, s := range live.MarketDataSources {
		liveMap[s.GetCode()] = s
	}
	desiredMap := marketDataSourceMap(manifest.GetMarketData().GetSources())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create market data source %s (%s)", code, desired.GetName()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeMarketDataSourceChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Market data source %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate market data source %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffMarketDataSetsAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.MarketDataSetDefinition)
	for _, ds := range live.MarketDataSets {
		liveMap[ds.GetCode()] = ds
	}
	desiredMap := marketDataSetMap(manifest.GetMarketData().GetDatasets())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create market data set %s (%s)", code, desired.GetUnit()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeMarketDataSetChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Market data set %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate market data set %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffOrganizationsAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.OrganizationDefinition)
	for _, o := range live.Organizations {
		liveMap[o.GetCode()] = o
	}
	desiredMap := organizationMap(manifest.GetOrganizations())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create organization %s (%s)", code, desired.GetName()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeOrganizationChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Organization %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate organization %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffInternalAccountsAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.InternalAccountDefinition)
	for _, a := range live.InternalAccounts {
		liveMap[a.GetCode()] = a
	}
	desiredMap := internalAccountMap(manifest.GetInternalAccounts())

	for code, desired := range desiredMap {
		existing, exists := liveMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create internal account %s (type: %s, instrument: %s)", code, desired.GetAccountType(), desired.GetInstrument()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeInternalAccountChanges(code, existing, desired),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Internal account %s unchanged", code),
			})
		}
	}

	for code := range liveMap {
		if _, exists := desiredMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate internal account %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffProviderConnectionsAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.ProviderConnectionConfig)
	for _, c := range live.ProviderConnections {
		liveMap[c.GetConnectionId()] = c
	}
	desiredMap := providerConnectionMap(manifest.GetOperationalGateway().GetProviderConnections())

	for id, desired := range desiredMap {
		existing, exists := liveMap[id]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create provider connection %s (%s)", id, desired.GetProviderName()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionUpdate,
				Description:  fmt.Sprintf("Update provider connection %s", id),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Provider connection %s unchanged", id),
			})
		}
	}

	for id := range liveMap {
		if _, exists := desiredMap[id]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate provider connection %s", id),
			})
		}
	}
}

func (d *ManifestDiffer) diffInstructionRoutesAgainstLive(live *LiveState, manifest *controlplanev1.Manifest, plan *DiffPlan) {
	liveMap := make(map[string]*controlplanev1.InstructionRouteConfig)
	for _, r := range live.InstructionRoutes {
		liveMap[r.GetInstructionType()] = r
	}
	desiredMap := instructionRouteMap(manifest.GetOperationalGateway().GetInstructionRoutes())

	for key, desired := range desiredMap {
		existing, exists := liveMap[key]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create instruction route %s → %s", key, desired.GetConnectionId()),
			})
			continue
		}
		if !proto.Equal(existing, desired) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionUpdate,
				Description:  fmt.Sprintf("Update instruction route %s", key),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Instruction route %s unchanged", key),
			})
		}
	}

	for key := range liveMap {
		if _, exists := desiredMap[key]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionDeprecate,
				Description:  fmt.Sprintf("Deprecate instruction route %s", key),
			})
		}
	}
}
