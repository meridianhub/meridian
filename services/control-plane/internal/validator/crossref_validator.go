package validator

import (
	"fmt"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// validateDuplicates checks for duplicate codes within the manifest.
func (v *ManifestValidator) validateDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate instrument codes
	instrumentCodes := make(map[string]int)
	for i, inst := range manifest.GetInstruments() {
		if prev, exists := instrumentCodes[inst.GetCode()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("instruments[%d].code", i),
				Code:         "DUPLICATE_CODE",
				Message:      fmt.Sprintf("duplicate instrument code %q (first defined at instruments[%d])", inst.GetCode(), prev),
				ResourceType: "instrument",
				ResourceID:   inst.GetCode(),
			})
		} else {
			instrumentCodes[inst.GetCode()] = i
		}
	}

	// Check duplicate account type codes
	accountTypeCodes := make(map[string]int)
	for i, acct := range manifest.GetAccountTypes() {
		if prev, exists := accountTypeCodes[acct.GetCode()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("account_types[%d].code", i),
				Code:         "DUPLICATE_CODE",
				Message:      fmt.Sprintf("duplicate account type code %q (first defined at account_types[%d])", acct.GetCode(), prev),
				ResourceType: "account_type",
				ResourceID:   acct.GetCode(),
			})
		} else {
			accountTypeCodes[acct.GetCode()] = i
		}
	}

	// Check duplicate saga names and event trigger filter requirements
	sagaNames := make(map[string]int)
	for i, saga := range manifest.GetSagas() {
		if prev, exists := sagaNames[saga.GetName()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("sagas[%d].name", i),
				Code:         "DUPLICATE_NAME",
				Message:      fmt.Sprintf("duplicate saga name %q (first defined at sagas[%d])", saga.GetName(), prev),
				ResourceType: "saga",
				ResourceID:   saga.GetName(),
			})
		} else {
			sagaNames[saga.GetName()] = i
		}
		// Warn when an event-triggered saga has no filter; all events will trigger the saga
		if strings.HasPrefix(saga.GetTrigger(), "event:") && saga.Filter == nil {
			addError(result, ValidationError{
				Severity:     SeverityWarning,
				Path:         fmt.Sprintf("sagas[%d].filter", i),
				Code:         "MISSING_EVENT_FILTER",
				Message:      fmt.Sprintf("saga %q subscribes to event trigger %q without a filter; the saga will execute for every matching event", saga.GetName(), saga.GetTrigger()),
				Suggestion:   `Add a CEL filter expression, e.g. filter: 'event.amount > 0'`,
				ResourceType: "saga",
				ResourceID:   saga.GetName(),
			})
		}
	}

	// Check duplicate mapping (name, version) pairs
	type mappingKey struct {
		name    string
		version int32
	}
	mappingKeys := make(map[mappingKey]int)
	for i, mp := range manifest.GetMappings() {
		key := mappingKey{name: mp.GetName(), version: mp.GetVersion()}
		if prev, exists := mappingKeys[key]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("mappings[%d]", i),
				Code:         "DUPLICATE_MAPPING",
				Message:      fmt.Sprintf("duplicate mapping name=%q version=%d (first defined at mappings[%d])", mp.GetName(), mp.GetVersion(), prev),
				ResourceType: "mapping",
				ResourceID:   fmt.Sprintf("%s:v%d", mp.GetName(), mp.GetVersion()),
			})
		} else {
			mappingKeys[key] = i
		}
	}

	// Check duplicate operational_gateway connection_ids and instruction_types
	v.validateOperationalGatewayDuplicates(manifest, result)

	// Check duplicate market data and organization codes
	v.validateMarketDataAndOrgDuplicates(manifest, result)
}

// validateMarketDataAndOrgDuplicates checks for duplicate codes in market data and organization sections.
func (v *ManifestValidator) validateMarketDataAndOrgDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate market data source codes
	mdSourceCodes := make(map[string]int)
	for i, src := range manifest.GetMarketData().GetSources() {
		if prev, exists := mdSourceCodes[src.GetCode()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("market_data.sources[%d].code", i),
				Code:         "DUPLICATE_CODE",
				Message:      fmt.Sprintf("duplicate market data source code %q (first defined at market_data.sources[%d])", src.GetCode(), prev),
				ResourceType: "market_data_source",
				ResourceID:   src.GetCode(),
			})
		} else {
			mdSourceCodes[src.GetCode()] = i
		}
	}

	// Check duplicate market data set codes
	mdSetCodes := make(map[string]int)
	for i, ds := range manifest.GetMarketData().GetDatasets() {
		if prev, exists := mdSetCodes[ds.GetCode()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("market_data.datasets[%d].code", i),
				Code:         "DUPLICATE_CODE",
				Message:      fmt.Sprintf("duplicate market data set code %q (first defined at market_data.datasets[%d])", ds.GetCode(), prev),
				ResourceType: "market_data_dataset",
				ResourceID:   ds.GetCode(),
			})
		} else {
			mdSetCodes[ds.GetCode()] = i
		}
	}

	// Check duplicate organization codes
	orgCodes := make(map[string]int)
	for i, org := range manifest.GetOrganizations() {
		if prev, exists := orgCodes[org.GetCode()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("organizations[%d].code", i),
				Code:         "DUPLICATE_CODE",
				Message:      fmt.Sprintf("duplicate organization code %q (first defined at organizations[%d])", org.GetCode(), prev),
				ResourceType: "organization",
				ResourceID:   org.GetCode(),
			})
		} else {
			orgCodes[org.GetCode()] = i
		}
	}

	// Check duplicate internal account codes
	iaCodes := make(map[string]int)
	for i, ia := range manifest.GetInternalAccounts() {
		if prev, exists := iaCodes[ia.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("internal_accounts[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate internal account code %q (first defined at internal_accounts[%d])", ia.GetCode(), prev),
			})
		} else {
			iaCodes[ia.GetCode()] = i
		}
	}
}

// validateOperationalGatewayDuplicates checks for duplicate connection_ids and instruction_types.
func (v *ManifestValidator) validateOperationalGatewayDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	gw := manifest.GetOperationalGateway()
	if gw == nil {
		return
	}

	connectionIDs := make(map[string]int)
	for i, conn := range gw.GetProviderConnections() {
		if prev, exists := connectionIDs[conn.GetConnectionId()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("operational_gateway.provider_connections[%d].connection_id", i),
				Code:         "DUPLICATE_CONNECTION_ID",
				Message:      fmt.Sprintf("duplicate connection_id %q (first defined at provider_connections[%d])", conn.GetConnectionId(), prev),
				ResourceType: "provider_connection",
				ResourceID:   conn.GetConnectionId(),
			})
		} else {
			connectionIDs[conn.GetConnectionId()] = i
		}
	}

	instructionTypes := make(map[string]int)
	for i, route := range gw.GetInstructionRoutes() {
		if prev, exists := instructionTypes[route.GetInstructionType()]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         fmt.Sprintf("operational_gateway.instruction_routes[%d].instruction_type", i),
				Code:         "DUPLICATE_INSTRUCTION_TYPE",
				Message:      fmt.Sprintf("duplicate instruction_type %q (first defined at instruction_routes[%d])", route.GetInstructionType(), prev),
				ResourceType: "instruction_route",
				ResourceID:   route.GetInstructionType(),
			})
		} else {
			instructionTypes[route.GetInstructionType()] = i
		}
	}
}

// validateCrossReferences checks that all references between manifest sections are valid.
func (v *ManifestValidator) validateCrossReferences(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	instrumentCodes := make(map[string]bool)
	for _, inst := range manifest.GetInstruments() {
		instrumentCodes[inst.GetCode()] = true
	}
	codeList := mapKeys(instrumentCodes)

	for i, acctType := range manifest.GetAccountTypes() {
		for j, instrCode := range acctType.GetAllowedInstruments() {
			checkInstrumentRef(
				instrCode, instrumentCodes, codeList,
				fmt.Sprintf("account_types[%d].allowed_instruments[%d]", i, j),
				result,
				"account_type",
				acctType.GetCode(),
			)
		}
	}

	for i, rule := range manifest.GetValuationRules() {
		checkInstrumentRef(
			rule.GetFromInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].from_instrument", i),
			result,
			"valuation_rule",
			fmt.Sprintf("%s->%s", rule.GetFromInstrument(), rule.GetToInstrument()),
		)
		checkInstrumentRef(
			rule.GetToInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].to_instrument", i),
			result,
			"valuation_rule",
			fmt.Sprintf("%s->%s", rule.GetFromInstrument(), rule.GetToInstrument()),
		)
	}

	// Validate operational_gateway cross-references
	v.validateOperationalGatewayCrossRefs(manifest, result)

	// Validate market data set source_code references valid market data source
	mdSourceCodes := make(map[string]bool)
	for _, src := range manifest.GetMarketData().GetSources() {
		mdSourceCodes[src.GetCode()] = true
	}
	mdSourceCodeList := mapKeys(mdSourceCodes)
	for i, ds := range manifest.GetMarketData().GetDatasets() {
		sourceCode := ds.GetSourceCode()
		if sourceCode != "" && !mdSourceCodes[sourceCode] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("market_data.datasets[%d].source_code", i),
				Code:            "INVALID_REFERENCE",
				Message:         fmt.Sprintf("market data set %q references unknown source code %q", ds.GetCode(), sourceCode),
				AvailableFields: mdSourceCodeList,
				ResourceType:    "market_data_dataset",
				ResourceID:      ds.GetCode(),
			}
			if suggestion := findClosestMatch(sourceCode, mdSourceCodeList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}

	// Validate organization party_type references
	v.validateOrganizationCrossRefs(manifest, result)

	// Validate internal account cross-references
	v.validateInternalAccountCrossRefs(manifest, result)
}

// validateOrganizationCrossRefs validates that organizations reference valid party types.
func (v *ManifestValidator) validateOrganizationCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Built-in party types from the PartyType enum plus manifest-defined party types
	validPartyTypes := map[string]bool{
		"PERSON":       true,
		"ORGANIZATION": true,
	}
	for _, pt := range manifest.GetPartyTypes() {
		if ptCode := pt.GetPartyType(); ptCode != "" {
			validPartyTypes[ptCode] = true
		}
	}
	partyTypeList := mapKeys(validPartyTypes)
	for i, org := range manifest.GetOrganizations() {
		partyType := org.GetPartyType()
		if partyType != "" && !validPartyTypes[partyType] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("organizations[%d].party_type", i),
				Code:            "INVALID_REFERENCE",
				Message:         fmt.Sprintf("organization %q references unknown party type %q", org.GetCode(), partyType),
				AvailableFields: partyTypeList,
				ResourceType:    "organization",
				ResourceID:      org.GetCode(),
			}
			if suggestion := findClosestMatch(partyType, partyTypeList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// validateInternalAccountCrossRefs validates that internal accounts reference valid
// account types, instruments, and organizations.
func (v *ManifestValidator) validateInternalAccountCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	accountTypeCodes := make(map[string]bool)
	for _, at := range manifest.GetAccountTypes() {
		accountTypeCodes[at.GetCode()] = true
	}

	instrumentCodes := make(map[string]bool)
	for _, inst := range manifest.GetInstruments() {
		instrumentCodes[inst.GetCode()] = true
	}

	orgCodes := make(map[string]bool)
	for _, org := range manifest.GetOrganizations() {
		orgCodes[org.GetCode()] = true
	}

	for i, ia := range manifest.GetInternalAccounts() {
		code := ia.GetCode()
		checkRef(ia.GetAccountType(), accountTypeCodes,
			fmt.Sprintf("internal_accounts[%d].account_type", i),
			fmt.Sprintf("internal account %q references unknown account type", code),
			result)
		checkRef(ia.GetInstrument(), instrumentCodes,
			fmt.Sprintf("internal_accounts[%d].instrument", i),
			fmt.Sprintf("internal account %q references unknown instrument", code),
			result)
		checkRef(ia.GetOwnerOrganization(), orgCodes,
			fmt.Sprintf("internal_accounts[%d].owner_organization", i),
			fmt.Sprintf("internal account %q references unknown organization", code),
			result)
	}
}

// checkRef validates that value exists in validCodes. If value is empty, no check is performed.
func checkRef(value string, validCodes map[string]bool, path, msgPrefix string, result *ValidationResult) {
	if value == "" || validCodes[value] {
		return
	}
	codeList := mapKeys(validCodes)
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "INVALID_REFERENCE",
		Message:         fmt.Sprintf("%s %q", msgPrefix, value),
		AvailableFields: codeList,
	}
	if suggestion := findClosestMatch(value, codeList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// validateOperationalGatewayCrossRefs validates referential integrity for the operational_gateway section.
// It checks that:
// - instruction_route.connection_id references an existing provider_connection
// - instruction_route.fallback_connection_id (if set) references an existing provider_connection
// - instruction_route.outbound_mapping_id (if set) references an existing mapping
// - instruction_route.inbound_mapping_id (if set) references an existing mapping
// - inbound_route.handler_saga references an existing saga
// - inbound_route.mapping_id (if set) references an existing mapping
func (v *ManifestValidator) validateOperationalGatewayCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	gw := manifest.GetOperationalGateway()
	if gw == nil {
		return
	}

	// Build lookup sets for valid connection_ids, mapping names, and saga names
	connectionIDs := make(map[string]bool)
	for _, conn := range gw.GetProviderConnections() {
		if id := conn.GetConnectionId(); id != "" {
			connectionIDs[id] = true
		}
	}

	mappingNames := make(map[string]bool)
	for _, mp := range manifest.GetMappings() {
		if name := mp.GetName(); name != "" {
			mappingNames[name] = true
		}
	}

	sagaNames := make(map[string]bool)
	for _, saga := range manifest.GetSagas() {
		if name := saga.GetName(); name != "" {
			sagaNames[name] = true
		}
	}

	for i, route := range gw.GetInstructionRoutes() {
		v.validateInstructionRouteRefs(route, i, connectionIDs, mappingNames, result)
	}

	for i, route := range gw.GetInboundRoutes() {
		v.validateInboundRouteRefs(route, i, sagaNames, mappingNames, result)
	}
}

// validateInstructionRouteRefs checks connection and mapping references for a single InstructionRouteConfig.
func (v *ManifestValidator) validateInstructionRouteRefs(
	route *controlplanev1.InstructionRouteConfig,
	idx int,
	connectionIDs map[string]bool,
	mappingNames map[string]bool,
	result *ValidationResult,
) {
	basePath := fmt.Sprintf("operational_gateway.instruction_routes[%d]", idx)
	connectionIDList := mapKeys(connectionIDs)
	mappingNameList := mapKeys(mappingNames)

	checkConnectionRef(route.GetConnectionId(), basePath+".connection_id", connectionIDs, connectionIDList, result)
	if fallbackID := route.GetFallbackConnectionId(); fallbackID != "" {
		checkConnectionRef(fallbackID, basePath+".fallback_connection_id", connectionIDs, connectionIDList, result)
	}
	if id := route.GetOutboundMappingId(); id != "" {
		checkMappingRef(id, basePath+".outbound_mapping_id", mappingNames, mappingNameList, result)
	}
	if id := route.GetInboundMappingId(); id != "" {
		checkMappingRef(id, basePath+".inbound_mapping_id", mappingNames, mappingNameList, result)
	}
}

// validateInboundRouteRefs checks saga and mapping references for a single InboundRouteConfig.
func (v *ManifestValidator) validateInboundRouteRefs(
	route *controlplanev1.InboundRouteConfig,
	idx int,
	sagaNames map[string]bool,
	mappingNames map[string]bool,
	result *ValidationResult,
) {
	basePath := fmt.Sprintf("operational_gateway.inbound_routes[%d]", idx)
	sagaNameList := mapKeys(sagaNames)
	mappingNameList := mapKeys(mappingNames)

	if sagaName := route.GetHandlerSaga(); sagaName != "" && !sagaNames[sagaName] {
		ve := ValidationError{
			Severity:        SeverityError,
			Path:            basePath + ".handler_saga",
			Code:            "UNDEFINED_SAGA_REFERENCE",
			Message:         fmt.Sprintf("handler_saga %q is not defined in sagas[]", sagaName),
			AvailableFields: sagaNameList,
		}
		if suggestion := findClosestMatch(sagaName, sagaNameList); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}
	if id := route.GetMappingId(); id != "" {
		checkMappingRef(id, basePath+".mapping_id", mappingNames, mappingNameList, result)
	}
}

// checkConnectionRef validates that a connection ID string references an existing provider connection.
func checkConnectionRef(
	connID string,
	path string,
	validIDs map[string]bool,
	idList []string,
	result *ValidationResult,
) {
	if connID == "" || validIDs[connID] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_CONNECTION_REFERENCE",
		Message:         fmt.Sprintf("connection_id %q is not defined in operational_gateway.provider_connections", connID),
		AvailableFields: idList,
	}
	if suggestion := findClosestMatch(connID, idList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// checkMappingRef validates that a mapping name string references an existing mapping.
func checkMappingRef(
	mappingID string,
	path string,
	validNames map[string]bool,
	nameList []string,
	result *ValidationResult,
) {
	if mappingID == "" || validNames[mappingID] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_MAPPING_REFERENCE",
		Message:         fmt.Sprintf("mapping %q is not defined in mappings[]", mappingID),
		AvailableFields: nameList,
	}
	if suggestion := findClosestMatch(mappingID, nameList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// checkInstrumentRef validates that a referenced instrument code exists in the manifest.
// resourceType and resourceID identify the referencing resource (e.g., "account_type", "SETTLEMENT").
func checkInstrumentRef(
	code string,
	validCodes map[string]bool,
	codeList []string,
	path string,
	result *ValidationResult,
	resourceType string,
	resourceID string,
) {
	if validCodes[code] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_INSTRUMENT_REFERENCE",
		Message:         fmt.Sprintf("instrument code %q is not defined in instruments[]", code),
		AvailableFields: codeList,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
	}
	if suggestion := findClosestMatch(code, codeList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}
