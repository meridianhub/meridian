package differ

import (
	"fmt"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"google.golang.org/protobuf/proto"
)

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

func getInternalAccounts(m *controlplanev1.Manifest) []*controlplanev1.InternalAccountDefinition {
	if m == nil {
		return nil
	}
	return m.GetInternalAccounts()
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

// partyTypeKeyT is a collision-safe composite key for party type definitions.
// Using a struct key instead of string concatenation prevents collisions when
// either field contains the separator character.
type partyTypeKeyT struct {
	TenantID  string
	PartyType string
}

// String returns a human-readable representation for display purposes.
func (k partyTypeKeyT) String() string {
	return k.TenantID + ":" + k.PartyType
}

// partyTypeKey produces a collision-safe identifier for a party type definition.
func partyTypeKey(tenantID, partyType string) partyTypeKeyT {
	return partyTypeKeyT{TenantID: tenantID, PartyType: partyType}
}

func partyTypeMap(defs []*partyv1.PartyTypeDefinition) map[partyTypeKeyT]*partyv1.PartyTypeDefinition {
	m := make(map[partyTypeKeyT]*partyv1.PartyTypeDefinition, len(defs))
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
