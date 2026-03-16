package differ

import (
	"context"
	"fmt"
	"sort"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"google.golang.org/protobuf/proto"
)

// ManifestDiffer compares a last-applied manifest against a new manifest
// to produce a plan of CREATE/UPDATE/DELETE/NO_CHANGE actions.
//
// It follows Kubernetes apply semantics:
//   - Codes are immutable primary keys (like metadata.name in k8s)
//   - Resources present in new but not in last-applied -> CREATE
//   - Resources present in both with field changes -> UPDATE
//   - Resources present in last-applied but not in new -> DELETE (with safety checks)
//   - Resources identical in both -> NO_CHANGE
type ManifestDiffer struct {
	safety SafetyChecker
	drift  DriftDetector
}

// New creates a ManifestDiffer with the given safety checker and drift detector.
func New(safety SafetyChecker, drift DriftDetector) *ManifestDiffer {
	if safety == nil {
		safety = &NoOpSafetyChecker{}
	}
	if drift == nil {
		drift = &NoOpDriftDetector{}
	}
	return &ManifestDiffer{
		safety: safety,
		drift:  drift,
	}
}

// DiffOption configures optional behavior of a Diff call.
type DiffOption func(*diffConfig)

type diffConfig struct {
	skipSafetyChecks bool
}

// WithSkipSafetyChecks skips safety checks (blocked deletions) and breaking
// change flagging. Use when validating a manifest for a new tenant that has
// no existing state.
func WithSkipSafetyChecks() DiffOption {
	return func(c *diffConfig) {
		c.skipSafetyChecks = true
	}
}

// Diff compares lastApplied against newManifest and returns a plan.
// If lastApplied is nil, all resources in newManifest are treated as CREATE.
func (d *ManifestDiffer) Diff(ctx context.Context, lastApplied, newManifest *controlplanev1.Manifest, opts ...DiffOption) (*DiffPlan, error) {
	if newManifest == nil {
		return nil, ErrNilManifest
	}

	cfg := &diffConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	plan := &DiffPlan{}

	// Diff each resource type independently
	d.diffInstruments(lastApplied, newManifest, plan)
	d.diffAccountTypes(lastApplied, newManifest, plan)
	d.diffValuationRules(lastApplied, newManifest, plan)
	d.diffSagas(lastApplied, newManifest, plan)
	d.diffPartyTypes(lastApplied, newManifest, plan)
	d.diffMappings(lastApplied, newManifest, plan)
	d.diffOperationalGateway(lastApplied, newManifest, plan)
	d.diffMarketDataSources(lastApplied, newManifest, plan)
	d.diffMarketDataSets(lastApplied, newManifest, plan)
	d.diffOrganizations(lastApplied, newManifest, plan)
	d.diffInternalAccounts(lastApplied, newManifest, plan)

	// Run safety checks on all DELETE actions (skip when validating for a new tenant)
	if !cfg.skipSafetyChecks {
		if err := d.runSafetyChecks(ctx, plan); err != nil {
			return nil, fmt.Errorf("safety check failed: %w", err)
		}
	}

	// Flag breaking changes (skip when validating for a new tenant)
	if !cfg.skipSafetyChecks {
		for i := range plan.Actions {
			if plan.Actions[i].Action == ActionDelete {
				plan.Actions[i].Breaking = true
				plan.HasBreakingChanges = true
			}
		}
	}

	// Detect drift if we have a last-applied manifest
	if lastApplied != nil {
		warnings, err := d.drift.DetectDrift(ctx, lastApplied)
		if err != nil {
			return nil, fmt.Errorf("drift detection failed: %w", err)
		}
		plan.DriftWarnings = warnings
	}

	// Sort actions for deterministic output
	sort.Slice(plan.Actions, func(i, j int) bool {
		if plan.Actions[i].ResourceType != plan.Actions[j].ResourceType {
			return plan.Actions[i].ResourceType < plan.Actions[j].ResourceType
		}
		return plan.Actions[i].ResourceCode < plan.Actions[j].ResourceCode
	})

	return plan, nil
}

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

func (d *ManifestDiffer) runSafetyChecks(ctx context.Context, plan *DiffPlan) error {
	for _, action := range plan.Actions {
		if action.Action != ActionDelete {
			continue
		}
		var blocked *BlockedDeletion
		var err error

		switch action.ResourceType {
		case ResourceAccountType:
			blocked, err = d.safety.CheckAccountTypeDeletion(ctx, action.ResourceCode)
		case ResourceInstrument:
			blocked, err = d.safety.CheckInstrumentDeletion(ctx, action.ResourceCode)
		case ResourceSaga:
			blocked, err = d.safety.CheckSagaDeletion(ctx, action.ResourceCode)
		case ResourceValuationRule,
			ResourcePartyType,
			ResourceMapping,
			ResourceProviderConnection,
			ResourceInstructionRoute,
			ResourceMarketDataSource,
			ResourceMarketDataSet,
			ResourceOrganization,
			ResourceInternalAccount:
			// No downstream dependency checks for these resource types.
		}
		if err != nil {
			return fmt.Errorf("safety check for %s %s: %w", action.ResourceType, action.ResourceCode, err)
		}
		if blocked != nil {
			plan.BlockedDeletions = append(plan.BlockedDeletions, *blocked)
		}
	}
	return nil
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

// Helper functions to safely extract slices from possibly-nil manifests.

func getInstruments(m *controlplanev1.Manifest) []*controlplanev1.InstrumentDefinition {
	if m == nil {
		return nil
	}
	return m.GetInstruments()
}

func getAccountTypes(m *controlplanev1.Manifest) []*controlplanev1.AccountTypeDefinition {
	if m == nil {
		return nil
	}
	return m.GetAccountTypes()
}

func getValuationRules(m *controlplanev1.Manifest) []*controlplanev1.ValuationRule {
	if m == nil {
		return nil
	}
	return m.GetValuationRules()
}

func getSagas(m *controlplanev1.Manifest) []*controlplanev1.SagaDefinition {
	if m == nil {
		return nil
	}
	return m.GetSagas()
}

func getPartyTypes(m *controlplanev1.Manifest) []*partyv1.PartyTypeDefinition {
	if m == nil {
		return nil
	}
	return m.GetPartyTypes()
}

func getMappings(m *controlplanev1.Manifest) []*mappingv1.MappingDefinition {
	if m == nil {
		return nil
	}
	return m.GetMappings()
}

func getProviderConnections(m *controlplanev1.Manifest) []*controlplanev1.ProviderConnectionConfig {
	if m == nil {
		return nil
	}
	return m.GetOperationalGateway().GetProviderConnections()
}

func getInstructionRoutes(m *controlplanev1.Manifest) []*controlplanev1.InstructionRouteConfig {
	if m == nil {
		return nil
	}
	return m.GetOperationalGateway().GetInstructionRoutes()
}

// Map-building helpers keyed by stable identifiers.

func instrumentMap(instruments []*controlplanev1.InstrumentDefinition) map[string]*controlplanev1.InstrumentDefinition {
	m := make(map[string]*controlplanev1.InstrumentDefinition, len(instruments))
	for _, inst := range instruments {
		m[inst.GetCode()] = inst
	}
	return m
}

func accountTypeMap(types []*controlplanev1.AccountTypeDefinition) map[string]*controlplanev1.AccountTypeDefinition {
	m := make(map[string]*controlplanev1.AccountTypeDefinition, len(types))
	for _, at := range types {
		m[at.GetCode()] = at
	}
	return m
}

func valuationRuleMap(rules []*controlplanev1.ValuationRule) map[string]*controlplanev1.ValuationRule {
	m := make(map[string]*controlplanev1.ValuationRule, len(rules))
	for _, r := range rules {
		m[valRuleKey(r.GetFromInstrument(), r.GetToInstrument())] = r
	}
	return m
}

func sagaMap(sagas []*controlplanev1.SagaDefinition) map[string]*controlplanev1.SagaDefinition {
	m := make(map[string]*controlplanev1.SagaDefinition, len(sagas))
	for _, s := range sagas {
		m[s.GetName()] = s
	}
	return m
}

// partyTypeKey produces a stable identifier for a party type definition.
// The key is (tenant_id, party_type) to match uniqueness constraints.
func partyTypeKey(tenantID, partyType string) string {
	return tenantID + ":" + partyType
}

func partyTypeMap(defs []*partyv1.PartyTypeDefinition) map[string]*partyv1.PartyTypeDefinition {
	m := make(map[string]*partyv1.PartyTypeDefinition, len(defs))
	for _, d := range defs {
		m[partyTypeKey(d.GetTenantId(), d.GetPartyType())] = d
	}
	return m
}

// mappingKey produces a stable identifier for a mapping definition (name:version).
func mappingKey(name string, version int32) string {
	return fmt.Sprintf("%s:%d", name, version)
}

func mappingMap(mappings []*mappingv1.MappingDefinition) map[string]*mappingv1.MappingDefinition {
	m := make(map[string]*mappingv1.MappingDefinition, len(mappings))
	for _, mp := range mappings {
		m[mappingKey(mp.GetName(), mp.GetVersion())] = mp
	}
	return m
}

func providerConnectionMap(conns []*controlplanev1.ProviderConnectionConfig) map[string]*controlplanev1.ProviderConnectionConfig {
	m := make(map[string]*controlplanev1.ProviderConnectionConfig, len(conns))
	for _, c := range conns {
		m[c.GetConnectionId()] = c
	}
	return m
}

func instructionRouteMap(routes []*controlplanev1.InstructionRouteConfig) map[string]*controlplanev1.InstructionRouteConfig {
	m := make(map[string]*controlplanev1.InstructionRouteConfig, len(routes))
	for _, r := range routes {
		m[r.GetInstructionType()] = r
	}
	return m
}

func getMarketDataSources(m *controlplanev1.Manifest) []*controlplanev1.MarketDataSourceDefinition {
	if m == nil {
		return nil
	}
	return m.GetMarketData().GetSources()
}

func getMarketDataSets(m *controlplanev1.Manifest) []*controlplanev1.MarketDataSetDefinition {
	if m == nil {
		return nil
	}
	return m.GetMarketData().GetDatasets()
}

func getOrganizations(m *controlplanev1.Manifest) []*controlplanev1.OrganizationDefinition {
	if m == nil {
		return nil
	}
	return m.GetOrganizations()
}

func marketDataSourceMap(sources []*controlplanev1.MarketDataSourceDefinition) map[string]*controlplanev1.MarketDataSourceDefinition {
	m := make(map[string]*controlplanev1.MarketDataSourceDefinition, len(sources))
	for _, s := range sources {
		m[s.GetCode()] = s
	}
	return m
}

func marketDataSetMap(datasets []*controlplanev1.MarketDataSetDefinition) map[string]*controlplanev1.MarketDataSetDefinition {
	m := make(map[string]*controlplanev1.MarketDataSetDefinition, len(datasets))
	for _, ds := range datasets {
		m[ds.GetCode()] = ds
	}
	return m
}

func organizationMap(orgs []*controlplanev1.OrganizationDefinition) map[string]*controlplanev1.OrganizationDefinition {
	m := make(map[string]*controlplanev1.OrganizationDefinition, len(orgs))
	for _, o := range orgs {
		m[o.GetCode()] = o
	}
	return m
}

func getInternalAccounts(m *controlplanev1.Manifest) []*controlplanev1.InternalAccountDefinition {
	if m == nil {
		return nil
	}
	return m.GetInternalAccounts()
}

func internalAccountMap(accounts []*controlplanev1.InternalAccountDefinition) map[string]*controlplanev1.InternalAccountDefinition {
	m := make(map[string]*controlplanev1.InternalAccountDefinition, len(accounts))
	for _, a := range accounts {
		m[a.GetCode()] = a
	}
	return m
}

// Change description helpers.

func describeInstrumentChanges(code string, prev, updated *controlplanev1.InstrumentDefinition) string {
	var changes []string
	if prev.GetName() != updated.GetName() {
		changes = append(changes, fmt.Sprintf("name: %q -> %q", prev.GetName(), updated.GetName()))
	}
	if prev.GetType() != updated.GetType() {
		changes = append(changes, fmt.Sprintf("type: %s -> %s", prev.GetType(), updated.GetType()))
	}
	if !proto.Equal(prev.GetDimensions(), updated.GetDimensions()) {
		changes = append(changes, "dimensions changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update instrument %s", code)
	}
	return fmt.Sprintf("Update instrument %s (%s)", code, strings.Join(changes, "; "))
}

func describeAccountTypeChanges(code string, prev, updated *controlplanev1.AccountTypeDefinition) string {
	var changes []string
	if prev.GetName() != updated.GetName() {
		changes = append(changes, fmt.Sprintf("name: %q -> %q", prev.GetName(), updated.GetName()))
	}
	if prev.GetNormalBalance() != updated.GetNormalBalance() {
		changes = append(changes, fmt.Sprintf("normal_balance: %s -> %s", prev.GetNormalBalance(), updated.GetNormalBalance()))
	}
	if !proto.Equal(prev.GetPolicies(), updated.GetPolicies()) {
		changes = append(changes, "policies changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update account type %s", code)
	}
	return fmt.Sprintf("Update account type %s (%s)", code, strings.Join(changes, "; "))
}

func describeSagaChanges(name string, prev, updated *controlplanev1.SagaDefinition) string {
	var changes []string
	if prev.GetTrigger() != updated.GetTrigger() {
		changes = append(changes, fmt.Sprintf("trigger: %q -> %q", prev.GetTrigger(), updated.GetTrigger()))
	}
	if prev.GetScript() != updated.GetScript() {
		changes = append(changes, "script changed")
	}
	if prev.GetFilter() != updated.GetFilter() {
		changes = append(changes, "filter changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update saga %s", name)
	}
	return fmt.Sprintf("Update saga %s (%s)", name, strings.Join(changes, "; "))
}

func describePartyTypeChanges(key string, prev, updated *partyv1.PartyTypeDefinition) string {
	var changes []string
	if prev.GetAttributeSchema() != updated.GetAttributeSchema() {
		changes = append(changes, "attribute_schema changed")
	}
	if prev.GetValidationCel() != updated.GetValidationCel() {
		changes = append(changes, "validation_cel changed")
	}
	if prev.GetEligibilityCel() != updated.GetEligibilityCel() {
		changes = append(changes, "eligibility_cel changed")
	}
	if prev.GetErrorMessageCel() != updated.GetErrorMessageCel() {
		changes = append(changes, "error_message_cel changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update party type %s", key)
	}
	return fmt.Sprintf("Update party type %s (%s)", key, strings.Join(changes, "; "))
}

func describeMappingChanges(key string, prev, updated *mappingv1.MappingDefinition) string {
	var changes []string
	if prev.GetTargetService() != updated.GetTargetService() {
		changes = append(changes, fmt.Sprintf("target_service: %q -> %q", prev.GetTargetService(), updated.GetTargetService()))
	}
	if prev.GetTargetRpc() != updated.GetTargetRpc() {
		changes = append(changes, fmt.Sprintf("target_rpc: %q -> %q", prev.GetTargetRpc(), updated.GetTargetRpc()))
	}
	if prev.GetStatus() != updated.GetStatus() {
		changes = append(changes, fmt.Sprintf("status: %s -> %s", prev.GetStatus(), updated.GetStatus()))
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update mapping %s", key)
	}
	return fmt.Sprintf("Update mapping %s (%s)", key, strings.Join(changes, "; "))
}

func describeMarketDataSourceChanges(code string, prev, updated *controlplanev1.MarketDataSourceDefinition) string {
	var changes []string
	if prev.GetName() != updated.GetName() {
		changes = append(changes, fmt.Sprintf("name: %q -> %q", prev.GetName(), updated.GetName()))
	}
	if prev.GetTrustLevel() != updated.GetTrustLevel() {
		changes = append(changes, fmt.Sprintf("trust_level: %d -> %d", prev.GetTrustLevel(), updated.GetTrustLevel()))
	}
	if prev.GetDescription() != updated.GetDescription() {
		changes = append(changes, "description changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update market data source %s", code)
	}
	return fmt.Sprintf("Update market data source %s (%s)", code, strings.Join(changes, "; "))
}

func describeMarketDataSetChanges(code string, prev, updated *controlplanev1.MarketDataSetDefinition) string {
	var changes []string
	if prev.GetCategory() != updated.GetCategory() {
		changes = append(changes, fmt.Sprintf("category: %s -> %s", prev.GetCategory(), updated.GetCategory()))
	}
	if prev.GetUnit() != updated.GetUnit() {
		changes = append(changes, fmt.Sprintf("unit: %q -> %q", prev.GetUnit(), updated.GetUnit()))
	}
	if prev.GetSourceCode() != updated.GetSourceCode() {
		changes = append(changes, fmt.Sprintf("source_code: %q -> %q", prev.GetSourceCode(), updated.GetSourceCode()))
	}
	if prev.GetDisplayName() != updated.GetDisplayName() {
		changes = append(changes, fmt.Sprintf("display_name: %q -> %q", prev.GetDisplayName(), updated.GetDisplayName()))
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update market data set %s", code)
	}
	return fmt.Sprintf("Update market data set %s (%s)", code, strings.Join(changes, "; "))
}

func describeOrganizationChanges(code string, prev, updated *controlplanev1.OrganizationDefinition) string {
	var changes []string
	if prev.GetName() != updated.GetName() {
		changes = append(changes, fmt.Sprintf("name: %q -> %q", prev.GetName(), updated.GetName()))
	}
	if prev.GetPartyType() != updated.GetPartyType() {
		changes = append(changes, fmt.Sprintf("party_type: %q -> %q", prev.GetPartyType(), updated.GetPartyType()))
	}
	if !attributesEqual(prev.GetAttributes(), updated.GetAttributes()) {
		changes = append(changes, "attributes changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update organization %s", code)
	}
	return fmt.Sprintf("Update organization %s (%s)", code, strings.Join(changes, "; "))
}

func describeInternalAccountChanges(code string, prev, updated *controlplanev1.InternalAccountDefinition) string {
	var changes []string
	if prev.GetAccountType() != updated.GetAccountType() {
		changes = append(changes, fmt.Sprintf("account_type: %q -> %q", prev.GetAccountType(), updated.GetAccountType()))
	}
	if prev.GetInstrument() != updated.GetInstrument() {
		changes = append(changes, fmt.Sprintf("instrument: %q -> %q", prev.GetInstrument(), updated.GetInstrument()))
	}
	if prev.GetOwnerOrganization() != updated.GetOwnerOrganization() {
		changes = append(changes, fmt.Sprintf("owner_organization: %q -> %q", prev.GetOwnerOrganization(), updated.GetOwnerOrganization()))
	}
	if prev.GetDescription() != updated.GetDescription() {
		changes = append(changes, "description changed")
	}
	if len(changes) == 0 {
		return fmt.Sprintf("Update internal account %s", code)
	}
	return fmt.Sprintf("Update internal account %s (%s)", code, strings.Join(changes, "; "))
}

// attributesEqual compares two string-keyed maps for equality.
func attributesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || bv != v {
			return false
		}
	}
	return true
}
