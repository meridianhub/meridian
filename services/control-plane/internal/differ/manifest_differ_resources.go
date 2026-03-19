package differ

import (
	"fmt"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/protobuf/proto"
)

func (d *ManifestDiffer) diffInstruments(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := instrumentMap(getInstruments(lastApplied))
	newMap := instrumentMap(newManifest.GetInstruments())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create instrument %s (%s)", code, updated.GetName()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeInstrumentChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstrument,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete instrument %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffAccountTypes(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := accountTypeMap(getAccountTypes(lastApplied))
	newMap := accountTypeMap(newManifest.GetAccountTypes())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create account type %s (%s)", code, updated.GetName()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeAccountTypeChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceAccountType,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete account type %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffValuationRules(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := valuationRuleMap(getValuationRules(lastApplied))
	newMap := valuationRuleMap(newManifest.GetValuationRules())

	for key, updated := range newMap {
		prev, exists := oldMap[key]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceValuationRule,
				ResourceCode: key,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create valuation rule %s", key),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceValuationRule,
				ResourceCode: key,
				Action:       ActionUpdate,
				Description:  fmt.Sprintf("Update valuation rule %s", key),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceValuationRule,
				ResourceCode: key,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Valuation rule %s unchanged", key),
			})
		}
	}

	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceValuationRule,
				ResourceCode: key,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete valuation rule %s", key),
			})
		}
	}
}

func (d *ManifestDiffer) diffSagas(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := sagaMap(getSagas(lastApplied))
	newMap := sagaMap(newManifest.GetSagas())

	for name, updated := range newMap {
		prev, exists := oldMap[name]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create saga %s (trigger: %s)", name, updated.GetTrigger()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionUpdate,
				Description:  describeSagaChanges(name, prev, updated),
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

	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceSaga,
				ResourceCode: name,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete saga %s", name),
			})
		}
	}
}

func (d *ManifestDiffer) diffPartyTypes(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := partyTypeMap(getPartyTypes(lastApplied))
	newMap := partyTypeMap(newManifest.GetPartyTypes())

	for key, updated := range newMap {
		prev, exists := oldMap[key]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourcePartyType,
				ResourceCode: key,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create party type %s", key),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourcePartyType,
				ResourceCode: key,
				Action:       ActionUpdate,
				Description:  describePartyTypeChanges(key, prev, updated),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourcePartyType,
				ResourceCode: key,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Party type %s unchanged", key),
			})
		}
	}

	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourcePartyType,
				ResourceCode: key,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete party type %s", key),
			})
		}
	}
}

func (d *ManifestDiffer) diffMappings(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := mappingMap(getMappings(lastApplied))
	newMap := mappingMap(newManifest.GetMappings())

	for key, updated := range newMap {
		prev, exists := oldMap[key]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMapping,
				ResourceCode: key,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create mapping %s (version: %d)", updated.GetName(), updated.GetVersion()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMapping,
				ResourceCode: key,
				Action:       ActionUpdate,
				Description:  describeMappingChanges(key, prev, updated),
			})
		} else {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMapping,
				ResourceCode: key,
				Action:       ActionNoChange,
				Description:  fmt.Sprintf("Mapping %s unchanged", key),
			})
		}
	}

	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMapping,
				ResourceCode: key,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete mapping %s", key),
			})
		}
	}
}

func (d *ManifestDiffer) diffOperationalGateway(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	d.diffProviderConnections(lastApplied, newManifest, plan)
	d.diffInstructionRoutes(lastApplied, newManifest, plan)
}

func (d *ManifestDiffer) diffProviderConnections(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := providerConnectionMap(getProviderConnections(lastApplied))
	newMap := providerConnectionMap(newManifest.GetOperationalGateway().GetProviderConnections())

	for id, updated := range newMap {
		prev, exists := oldMap[id]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create provider connection %s (%s)", id, updated.GetProviderName()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
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

	for id := range oldMap {
		if _, exists := newMap[id]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceProviderConnection,
				ResourceCode: id,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete provider connection %s", id),
			})
		}
	}
}

func (d *ManifestDiffer) diffInstructionRoutes(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := instructionRouteMap(getInstructionRoutes(lastApplied))
	newMap := instructionRouteMap(newManifest.GetOperationalGateway().GetInstructionRoutes())

	for key, updated := range newMap {
		prev, exists := oldMap[key]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create instruction route %s → %s", key, updated.GetConnectionId()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
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

	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInstructionRoute,
				ResourceCode: key,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete instruction route %s", key),
			})
		}
	}
}

func (d *ManifestDiffer) diffMarketDataSources(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := marketDataSourceMap(getMarketDataSources(lastApplied))
	newMap := marketDataSourceMap(newManifest.GetMarketData().GetSources())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create market data source %s (%s)", code, updated.GetName()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeMarketDataSourceChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSource,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete market data source %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffMarketDataSets(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := marketDataSetMap(getMarketDataSets(lastApplied))
	newMap := marketDataSetMap(newManifest.GetMarketData().GetDatasets())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create market data set %s (%s)", code, updated.GetUnit()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeMarketDataSetChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceMarketDataSet,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete market data set %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffOrganizations(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := organizationMap(getOrganizations(lastApplied))
	newMap := organizationMap(newManifest.GetOrganizations())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create organization %s (%s)", code, updated.GetName()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeOrganizationChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceOrganization,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete organization %s", code),
			})
		}
	}
}

func (d *ManifestDiffer) diffInternalAccounts(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	oldMap := internalAccountMap(getInternalAccounts(lastApplied))
	newMap := internalAccountMap(newManifest.GetInternalAccounts())

	for code, updated := range newMap {
		prev, exists := oldMap[code]
		if !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionCreate,
				Description:  fmt.Sprintf("Create internal account %s (type: %s, instrument: %s)", code, updated.GetAccountType(), updated.GetInstrument()),
			})
			continue
		}
		if !proto.Equal(prev, updated) {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionUpdate,
				Description:  describeInternalAccountChanges(code, prev, updated),
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

	for code := range oldMap {
		if _, exists := newMap[code]; !exists {
			plan.Actions = append(plan.Actions, PlannedAction{
				ResourceType: ResourceInternalAccount,
				ResourceCode: code,
				Action:       ActionDelete,
				Description:  fmt.Sprintf("Delete internal account %s", code),
			})
		}
	}
}
