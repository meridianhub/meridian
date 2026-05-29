// Package applier provides input-to-saga conversion helpers for the manifest executor.
package applier

func convertInstruments(instruments []InstrumentInput) []interface{} {
	result := make([]interface{}, len(instruments))
	for i, inst := range instruments {
		result[i] = map[string]interface{}{
			"code":           inst.Code,
			"display_name":   inst.DisplayName,
			"dimension":      inst.Dimension,
			"decimal_places": inst.DecimalPlaces,
			"description":    inst.Description,
			"action":         inst.Action,
		}
	}
	return result
}

func convertAccountTypes(accountTypes []AccountTypeInput) []interface{} {
	result := make([]interface{}, len(accountTypes))
	for i, at := range accountTypes {
		vmethods := make([]interface{}, len(at.ValuationMethods))
		for j, vm := range at.ValuationMethods {
			vmethods[j] = map[string]interface{}{
				"input_instrument": vm.InputInstrument,
				"method_name":      vm.MethodName,
			}
		}
		result[i] = map[string]interface{}{
			"code":                      at.Code,
			"display_name":              at.DisplayName,
			"description":               at.Description,
			"behavior_class":            at.BehaviorClass,
			"normal_balance":            at.NormalBalance,
			"instrument_code":           at.InstrumentCode,
			"account_type":              at.AccountType,
			"default_saga_prefix":       at.DefaultSagaPrefix,
			"default_conversion_method": at.DefaultConversionMethod,
			"validation_cel":            at.ValidationCEL,
			"eligibility_cel":           at.EligibilityCEL,
			"attribute_schema":          at.AttributeSchema,
			"valuation_methods":         vmethods,
			"action":                    at.Action,
		}
	}
	return result
}

func convertMarketDataSources(sources []MarketDataSourceInput) []interface{} {
	result := make([]interface{}, len(sources))
	for i, src := range sources {
		result[i] = map[string]interface{}{
			"code":        src.Code,
			"name":        src.Name,
			"description": src.Description,
			"trust_level": src.TrustLevel,
			"action":      src.Action,
		}
	}
	return result
}

func convertMarketDataSets(dataSets []MarketDataSetInput) []interface{} {
	result := make([]interface{}, len(dataSets))
	for i, ds := range dataSets {
		result[i] = map[string]interface{}{
			"code":                      ds.Code,
			"category":                  ds.Category,
			"unit":                      ds.Unit,
			"source_code":               ds.SourceCode,
			"display_name":              ds.DisplayName,
			"description":               ds.Description,
			"validation_expression":     ds.ValidationExpression,
			"resolution_key_expression": ds.ResolutionKeyExpression,
			"action":                    ds.Action,
		}
	}
	return result
}

func convertValuationRules(rules []ValuationRuleInput) []interface{} {
	result := make([]interface{}, len(rules))
	for i, vr := range rules {
		result[i] = map[string]interface{}{
			"from_instrument": vr.FromInstrument,
			"to_instrument":   vr.ToInstrument,
			"rule_type":       vr.RuleType,
			"expression":      vr.Expression,
			"description":     vr.Description,
			"action":          vr.Action,
		}
	}
	return result
}

func convertOrganizations(organizations []OrganizationInput) []interface{} {
	result := make([]interface{}, len(organizations))
	for i, org := range organizations {
		attrs := make(map[string]interface{}, len(org.Attributes))
		for k, v := range org.Attributes {
			attrs[k] = v
		}
		result[i] = map[string]interface{}{
			"code":                    org.Code,
			"name":                    org.Name,
			"legal_name":              org.LegalName,
			"display_name":            org.DisplayName,
			"external_reference":      org.ExternalReference,
			"external_reference_type": org.ExternalReferenceType,
			"party_type":              org.PartyType,
			"attributes":              attrs,
			"action":                  org.Action,
		}
	}
	return result
}

func convertInternalAccounts(accounts []InternalAccountInput) []interface{} {
	result := make([]interface{}, len(accounts))
	for i, ia := range accounts {
		result[i] = map[string]interface{}{
			"code":               ia.Code,
			"account_type":       ia.AccountType,
			"instrument_code":    ia.InstrumentCode,
			"owner_organization": ia.OwnerOrganization,
			"description":        ia.Description,
			"action":             ia.Action,
		}
	}
	return result
}

func convertSagaDefinitions(defs []SagaDefinitionInput) []interface{} {
	result := make([]interface{}, len(defs))
	for i, sd := range defs {
		result[i] = map[string]interface{}{
			"name":         sd.Name,
			"display_name": sd.DisplayName,
			"description":  sd.Description,
			"script":       sd.Script,
			"version":      sd.Version,
			"action":       sd.Action,
		}
	}
	return result
}

func convertProviderConnections(conns []ProviderConnectionInput) []interface{} {
	result := make([]interface{}, len(conns))
	for i, pc := range conns {
		result[i] = map[string]interface{}{
			"connection_id":     pc.ConnectionID,
			"provider_name":     pc.ProviderName,
			"provider_type":     pc.ProviderType,
			"protocol":          pc.Protocol,
			"base_url":          pc.BaseURL,
			"auth_type":         pc.AuthType,
			"auth_config":       pc.AuthConfig,
			"retry_policy":      pc.RetryPolicy,
			"rate_limit_config": pc.RateLimitConfig,
			"action":            pc.Action,
		}
	}
	return result
}

func convertInstructionRoutes(routes []InstructionRouteInput) []interface{} {
	result := make([]interface{}, len(routes))
	for i, r := range routes {
		result[i] = map[string]interface{}{
			"instruction_type":       r.InstructionType,
			"connection_id":          r.ConnectionID,
			"fallback_connection_id": r.FallbackConnectionID,
			"outbound_mapping":       r.OutboundMapping,
			"inbound_mapping":        r.InboundMapping,
			"http_method":            r.HTTPMethod,
			"path_template":          r.PathTemplate,
			"action":                 r.Action,
		}
	}
	return result
}

// parseManifestVersion extracts the leading numeric portion of a version string.
// For numeric strings ("42"), returns the number directly.
// For semver-like strings ("1.2.3"), returns only the major version (1).
// Returns 1 as default for empty or non-numeric strings.
// The full version string is preserved separately in ApplyManifestResult.Version.
func parseManifestVersion(version string) int {
	n := 0
	for _, c := range version {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}
