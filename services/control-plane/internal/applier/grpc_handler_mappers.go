package applier

import (
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
)

// defaultDimension is the fallback dimension for instruments when the type cannot be derived.
const defaultDimension = "CURRENCY"

// buildExecutorInput converts a Manifest proto into the ApplyManifestInput
// consumed by the saga-based ManifestExecutor.
func buildExecutorInput(mf *controlplanev1.Manifest) *ApplyManifestInput {
	input := &ApplyManifestInput{
		ManifestVersion: mf.GetVersion(),
	}

	for _, inst := range mf.GetInstruments() {
		dim := instrumentTypeToDimension(inst.GetType(), inst.GetDimensions().GetUnit())
		if dim == "" {
			// Fallback: the Starlark script's .get("dimension", "CURRENCY") only
			// kicks in when the key is absent, not when it's empty. Use CURRENCY
			// as a safe default so the saga can proceed. A future manifest proto
			// change should add an explicit dimension field.
			dim = defaultDimension
		}
		input.Instruments = append(input.Instruments, InstrumentInput{
			Code:          inst.GetCode(),
			DisplayName:   inst.GetName(),
			Dimension:     dim,
			DecimalPlaces: int(inst.GetDimensions().GetPrecision()),
		})
	}

	for _, acct := range mf.GetAccountTypes() {
		nb := stripEnumPrefix(acct.GetNormalBalance().String(), "NORMAL_BALANCE_")
		if nb == "UNSPECIFIED" {
			nb = "DEBIT"
		}
		// Use the first allowed instrument as the account type's instrument code.
		var instrumentCode string
		if instruments := acct.GetAllowedInstruments(); len(instruments) > 0 {
			instrumentCode = instruments[0]
		}
		input.AccountTypes = append(input.AccountTypes, AccountTypeInput{
			Code:           acct.GetCode(),
			DisplayName:    acct.GetName(),
			NormalBalance:  nb,
			BehaviorClass:  "HOLDING",
			InstrumentCode: instrumentCode,
			AccountType:    acct.GetCode(), // used by saga auto-derivation for internal accounts
		})
	}

	for _, vr := range mf.GetValuationRules() {
		input.ValuationRules = append(input.ValuationRules, ValuationRuleInput{
			FromInstrument: vr.GetFromInstrument(),
			ToInstrument:   vr.GetToInstrument(),
			RuleType:       vr.GetMethod().String(),
		})
	}

	extractMarketData(mf, input)
	extractPartyAndAccounts(mf, input)

	for _, saga := range mf.GetSagas() {
		input.SagaDefinitions = append(input.SagaDefinitions, SagaDefinitionInput{
			Name:   saga.GetName(),
			Script: saga.GetScript(),
		})
	}

	extractOperationalGateway(mf, input)

	return input
}

// buildExecutorInputFromPlan converts a Manifest proto into ApplyManifestInput using
// a DiffPlan to filter resources. Only resources with actionable changes (CREATE,
// UPDATE, DEPRECATE) are included. NO_CHANGE and DELETE resources are excluded.
// Each included resource carries its Action field so handlers can behave differently.
func buildExecutorInputFromPlan(mf *controlplanev1.Manifest, plan *differ.DiffPlan) *ApplyManifestInput {
	actionable := buildActionableMap(plan)

	input := &ApplyManifestInput{
		ManifestVersion: mf.GetVersion(),
	}

	extractInstrumentsFromPlan(mf, input, actionable)
	extractAccountTypesFromPlan(mf, input, actionable)
	extractValuationRulesFromPlan(mf, input, actionable)
	extractMarketDataFromPlan(mf, input, actionable)
	extractPartyAndAccountsFromPlan(mf, input, actionable)
	extractSagasFromPlan(mf, input, actionable)
	extractOperationalGatewayFromPlan(mf, input, actionable)

	return input
}

// buildActionableMap builds a lookup of resources with actionable changes from a DiffPlan.
// Only CREATE, UPDATE, and DEPRECATE actions are included; NO_CHANGE and DELETE are excluded.
func buildActionableMap(plan *differ.DiffPlan) map[string]differ.ActionType {
	actionable := make(map[string]differ.ActionType, len(plan.Actions))
	for _, a := range plan.Actions {
		if a.Action == differ.ActionNoChange || a.Action == differ.ActionDelete {
			continue
		}
		actionable[resourceKey(a.ResourceType, a.ResourceCode)] = a.Action
	}
	return actionable
}

// extractInstrumentsFromPlan adds instruments with actionable changes to the input.
func extractInstrumentsFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	for _, inst := range mf.GetInstruments() {
		action, ok := actionable[resourceKey(differ.ResourceInstrument, inst.GetCode())]
		if !ok {
			continue
		}
		dim := instrumentTypeToDimension(inst.GetType(), inst.GetDimensions().GetUnit())
		if dim == "" {
			dim = defaultDimension
		}
		input.Instruments = append(input.Instruments, InstrumentInput{
			Code:          inst.GetCode(),
			DisplayName:   inst.GetName(),
			Dimension:     dim,
			DecimalPlaces: int(inst.GetDimensions().GetPrecision()),
			Action:        string(action),
		})
	}
}

// extractAccountTypesFromPlan adds account types with actionable changes to the input.
func extractAccountTypesFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	for _, acct := range mf.GetAccountTypes() {
		action, ok := actionable[resourceKey(differ.ResourceAccountType, acct.GetCode())]
		if !ok {
			continue
		}
		nb := stripEnumPrefix(acct.GetNormalBalance().String(), "NORMAL_BALANCE_")
		if nb == "UNSPECIFIED" {
			nb = "DEBIT"
		}
		var instrumentCode string
		if instruments := acct.GetAllowedInstruments(); len(instruments) > 0 {
			instrumentCode = instruments[0]
		}
		input.AccountTypes = append(input.AccountTypes, AccountTypeInput{
			Code:           acct.GetCode(),
			DisplayName:    acct.GetName(),
			NormalBalance:  nb,
			BehaviorClass:  "HOLDING",
			InstrumentCode: instrumentCode,
			AccountType:    acct.GetCode(),
			Action:         string(action),
		})
	}
}

// extractValuationRulesFromPlan adds valuation rules with actionable changes to the input.
func extractValuationRulesFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	for _, vr := range mf.GetValuationRules() {
		key := valRuleKeyForMapper(vr.GetFromInstrument(), vr.GetToInstrument())
		action, ok := actionable[resourceKey(differ.ResourceValuationRule, key)]
		if !ok {
			continue
		}
		input.ValuationRules = append(input.ValuationRules, ValuationRuleInput{
			FromInstrument: vr.GetFromInstrument(),
			ToInstrument:   vr.GetToInstrument(),
			RuleType:       vr.GetMethod().String(),
			Action:         string(action),
		})
	}
}

// extractSagasFromPlan adds saga definitions with actionable changes to the input.
func extractSagasFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	for _, saga := range mf.GetSagas() {
		action, ok := actionable[resourceKey(differ.ResourceSaga, saga.GetName())]
		if !ok {
			continue
		}
		input.SagaDefinitions = append(input.SagaDefinitions, SagaDefinitionInput{
			Name:   saga.GetName(),
			Script: saga.GetScript(),
			Action: string(action),
		})
	}
}

// resourceKey builds a lookup key for matching diff actions to manifest resources.
func resourceKey(rt differ.ResourceType, code string) string {
	return string(rt) + ":" + code
}

// extractMarketDataFromPlan converts market data resources filtered by the action map.
func extractMarketDataFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	md := mf.GetMarketData()
	if md == nil {
		return
	}
	for _, src := range md.GetSources() {
		action, ok := actionable[resourceKey(differ.ResourceMarketDataSource, src.GetCode())]
		if !ok {
			continue
		}
		input.MarketDataSources = append(input.MarketDataSources, MarketDataSourceInput{
			Code:        src.GetCode(),
			Name:        src.GetName(),
			Description: src.GetDescription(),
			TrustLevel:  int(src.GetTrustLevel()),
			Action:      string(action),
		})
	}
	for _, ds := range md.GetDatasets() {
		action, ok := actionable[resourceKey(differ.ResourceMarketDataSet, ds.GetCode())]
		if !ok {
			continue
		}
		input.MarketDataSets = append(input.MarketDataSets, MarketDataSetInput{
			Code:                    ds.GetCode(),
			Category:                stripEnumPrefix(ds.GetCategory().String(), "DATA_CATEGORY_"),
			Unit:                    ds.GetUnit(),
			SourceCode:              ds.GetSourceCode(),
			DisplayName:             ds.GetDisplayName(),
			Description:             ds.GetDescription(),
			ValidationExpression:    ds.GetValidationExpression(),
			ResolutionKeyExpression: ds.GetResolutionKeyExpression(),
			Action:                  string(action),
		})
	}
}

// extractPartyAndAccountsFromPlan converts organizations and internal accounts filtered by the action map.
func extractPartyAndAccountsFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	for _, org := range mf.GetOrganizations() {
		action, ok := actionable[resourceKey(differ.ResourceOrganization, org.GetCode())]
		if !ok {
			continue
		}
		legalName := org.GetLegalName()
		if legalName == "" {
			legalName = org.GetName()
		}
		if legalName == "" {
			legalName = org.GetCode()
		}
		displayName := org.GetDisplayName()
		if displayName == "" {
			displayName = legalName
		}
		extRef := org.GetExternalReference()
		if extRef == "" {
			extRef = org.GetCode()
		}
		input.Organizations = append(input.Organizations, OrganizationInput{
			Code:                  org.GetCode(),
			Name:                  org.GetName(),
			LegalName:             legalName,
			DisplayName:           displayName,
			ExternalReference:     extRef,
			ExternalReferenceType: org.GetExternalReferenceType(),
			PartyType:             org.GetPartyType(),
			Attributes:            org.GetAttributes(),
			Action:                string(action),
		})
	}
	for _, ia := range mf.GetInternalAccounts() {
		action, ok := actionable[resourceKey(differ.ResourceInternalAccount, ia.GetCode())]
		if !ok {
			continue
		}
		input.InternalAccounts = append(input.InternalAccounts, InternalAccountInput{
			Code:              ia.GetCode(),
			AccountType:       ia.GetAccountType(),
			InstrumentCode:    ia.GetInstrument(),
			OwnerOrganization: ia.GetOwnerOrganization(),
			Description:       ia.GetDescription(),
			Action:            string(action),
		})
	}
}

// extractOperationalGatewayFromPlan converts operational gateway resources filtered by the action map.
func extractOperationalGatewayFromPlan(mf *controlplanev1.Manifest, input *ApplyManifestInput, actionable map[string]differ.ActionType) {
	gw := mf.GetOperationalGateway()
	if gw == nil {
		return
	}
	for _, conn := range gw.GetProviderConnections() {
		action, ok := actionable[resourceKey(differ.ResourceProviderConnection, conn.GetConnectionId())]
		if !ok {
			continue
		}
		pc := ProviderConnectionInput{
			ConnectionID: conn.GetConnectionId(),
			ProviderName: conn.GetProviderName(),
			ProviderType: conn.GetProviderType(),
			Protocol:     conn.GetProtocol().String(),
			BaseURL:      conn.GetBaseUrl(),
			Action:       string(action),
		}
		pc.AuthType, pc.AuthConfig = extractAuthConfig(conn.GetAuth())
		if rp := conn.GetRetryPolicy(); rp != nil {
			pc.RetryPolicy = map[string]any{
				"max_attempts":            rp.GetMaxAttempts(),
				"initial_backoff_seconds": rp.GetInitialBackoffSeconds(),
				"max_backoff_seconds":     rp.GetMaxBackoffSeconds(),
				"backoff_multiplier":      rp.GetBackoffMultiplier(),
			}
		}
		if rl := conn.GetRateLimit(); rl != nil {
			pc.RateLimitConfig = map[string]any{
				"requests_per_second": rl.GetRequestsPerSecond(),
				"burst_size":          rl.GetBurstSize(),
			}
		}
		input.ProviderConnections = append(input.ProviderConnections, pc)
	}
	for _, route := range gw.GetInstructionRoutes() {
		action, ok := actionable[resourceKey(differ.ResourceInstructionRoute, route.GetInstructionType())]
		if !ok {
			continue
		}
		input.InstructionRoutes = append(input.InstructionRoutes, InstructionRouteInput{
			InstructionType:      route.GetInstructionType(),
			ConnectionID:         route.GetConnectionId(),
			FallbackConnectionID: route.GetFallbackConnectionId(),
			OutboundMapping:      route.GetOutboundMappingId(),
			InboundMapping:       route.GetInboundMappingId(),
			HTTPMethod:           route.GetHttpMethod(),
			PathTemplate:         route.GetPathTemplate(),
			Action:               string(action),
		})
	}
}

// valRuleKey is imported from differ but we need a local version for the mapper.
// It produces a stable identifier for a valuation rule (FROM->TO pair).
func valRuleKeyForMapper(from, to string) string {
	return strings.ToUpper(from) + "->" + strings.ToUpper(to)
}

// extractMarketData converts market data sources and data sets from the manifest proto.
func extractMarketData(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	md := mf.GetMarketData()
	if md == nil {
		return
	}
	for _, src := range md.GetSources() {
		input.MarketDataSources = append(input.MarketDataSources, MarketDataSourceInput{
			Code:        src.GetCode(),
			Name:        src.GetName(),
			Description: src.GetDescription(),
			TrustLevel:  int(src.GetTrustLevel()),
		})
	}
	for _, ds := range md.GetDatasets() {
		input.MarketDataSets = append(input.MarketDataSets, MarketDataSetInput{
			Code:                    ds.GetCode(),
			Category:                stripEnumPrefix(ds.GetCategory().String(), "DATA_CATEGORY_"),
			Unit:                    ds.GetUnit(),
			SourceCode:              ds.GetSourceCode(),
			DisplayName:             ds.GetDisplayName(),
			Description:             ds.GetDescription(),
			ValidationExpression:    ds.GetValidationExpression(),
			ResolutionKeyExpression: ds.GetResolutionKeyExpression(),
		})
	}
}

// extractPartyAndAccounts converts organizations and internal accounts from the manifest proto.
func extractPartyAndAccounts(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	for _, org := range mf.GetOrganizations() {
		// Resolve legal_name with fallback chain: legal_name -> name -> code
		legalName := org.GetLegalName()
		if legalName == "" {
			legalName = org.GetName()
		}
		if legalName == "" {
			legalName = org.GetCode()
		}

		// Resolve display_name with fallback chain: display_name -> legal_name
		displayName := org.GetDisplayName()
		if displayName == "" {
			displayName = legalName
		}

		// Resolve external_reference with fallback: external_reference -> code
		extRef := org.GetExternalReference()
		if extRef == "" {
			extRef = org.GetCode()
		}

		input.Organizations = append(input.Organizations, OrganizationInput{
			Code:                  org.GetCode(),
			Name:                  org.GetName(),
			LegalName:             legalName,
			DisplayName:           displayName,
			ExternalReference:     extRef,
			ExternalReferenceType: org.GetExternalReferenceType(),
			PartyType:             org.GetPartyType(),
			Attributes:            org.GetAttributes(),
		})
	}
	for _, ia := range mf.GetInternalAccounts() {
		input.InternalAccounts = append(input.InternalAccounts, InternalAccountInput{
			Code:              ia.GetCode(),
			AccountType:       ia.GetAccountType(),
			InstrumentCode:    ia.GetInstrument(),
			OwnerOrganization: ia.GetOwnerOrganization(),
			Description:       ia.GetDescription(),
		})
	}
}

// extractOperationalGateway converts operational gateway config from the manifest proto.
func extractOperationalGateway(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	gw := mf.GetOperationalGateway()
	if gw == nil {
		return
	}
	for _, conn := range gw.GetProviderConnections() {
		pc := ProviderConnectionInput{
			ConnectionID: conn.GetConnectionId(),
			ProviderName: conn.GetProviderName(),
			ProviderType: conn.GetProviderType(),
			Protocol:     conn.GetProtocol().String(),
			BaseURL:      conn.GetBaseUrl(),
		}
		pc.AuthType, pc.AuthConfig = extractAuthConfig(conn.GetAuth())
		if rp := conn.GetRetryPolicy(); rp != nil {
			pc.RetryPolicy = map[string]any{
				"max_attempts":            rp.GetMaxAttempts(),
				"initial_backoff_seconds": rp.GetInitialBackoffSeconds(),
				"max_backoff_seconds":     rp.GetMaxBackoffSeconds(),
				"backoff_multiplier":      rp.GetBackoffMultiplier(),
			}
		}
		if rl := conn.GetRateLimit(); rl != nil {
			pc.RateLimitConfig = map[string]any{
				"requests_per_second": rl.GetRequestsPerSecond(),
				"burst_size":          rl.GetBurstSize(),
			}
		}
		input.ProviderConnections = append(input.ProviderConnections, pc)
	}

	for _, route := range gw.GetInstructionRoutes() {
		input.InstructionRoutes = append(input.InstructionRoutes, InstructionRouteInput{
			InstructionType:      route.GetInstructionType(),
			ConnectionID:         route.GetConnectionId(),
			FallbackConnectionID: route.GetFallbackConnectionId(),
			OutboundMapping:      route.GetOutboundMappingId(),
			InboundMapping:       route.GetInboundMappingId(),
			HTTPMethod:           route.GetHttpMethod(),
			PathTemplate:         route.GetPathTemplate(),
		})
	}
}

// unitToDimension maps common instrument unit names to their Dimension enum values.
// Unit names in manifests (e.g., "kWh", "TONNE_CO2E") don't match Dimension enum
// names (ENERGY, CARBON), so we need an explicit mapping table.
var unitToDimension = map[string]string{
	"KWH":        "ENERGY",
	"MWH":        "ENERGY",
	"WH":         "ENERGY",
	"TONNE_CO2E": "CARBON",
	"KG_CO2E":    "CARBON",
	"GPU_HOUR":   "COMPUTE",
	"CPU_HOUR":   "COMPUTE",
	"GB":         "DATA",
	"TB":         "DATA",
	"LITER":      "VOLUME",
	"LITRE":      "VOLUME", //nolint:misspell // British English variant is a valid unit name
	"GALLON":     "VOLUME",
	"KG":         "MASS",
	"TONNE":      "MASS",
	"SECOND":     "TIME",
	"MINUTE":     "TIME",
	"HOUR":       "TIME",
	"POINTS":     "COUNT",
}

// instrumentTypeToDimension derives the Dimension enum name from the manifest
// InstrumentType and unit. FIAT->CURRENCY, VOUCHER->COUNT. For COMMODITY and
// other types, uses a unit-to-dimension mapping table since unit names (kWh)
// don't match dimension enum names (ENERGY).
func instrumentTypeToDimension(instType controlplanev1.InstrumentType, unit string) string {
	switch instType {
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT:
		return defaultDimension
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER:
		return "COUNT"
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
		controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED:
		upper := strings.ToUpper(unit)
		// First check the unit-to-dimension mapping table.
		if dim, ok := unitToDimension[upper]; ok {
			return dim
		}
		// Fall back to checking if the uppercased unit IS a valid Dimension name.
		if _, ok := referencedatav1.Dimension_value["DIMENSION_"+upper]; ok {
			return upper
		}
		return ""
	}
	return ""
}

// stripEnumPrefix removes a common prefix from a proto enum string representation.
// For example, stripEnumPrefix("NORMAL_BALANCE_DEBIT", "NORMAL_BALANCE_") returns "DEBIT".
// Returns the original string if the prefix is not found.
func stripEnumPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

// extractAuthConfig converts a manifest AuthConfigManifest oneof to (authType, configMap).
func extractAuthConfig(auth *controlplanev1.AuthConfigManifest) (string, map[string]any) {
	if auth == nil {
		return "", nil
	}
	switch v := auth.GetAuthConfig().(type) {
	case *controlplanev1.AuthConfigManifest_ApiKey:
		return "api_key", map[string]any{
			"header_name": v.ApiKey.GetHeaderName(),
			"secret_ref":  v.ApiKey.GetApiKeySecretRef(),
		}
	case *controlplanev1.AuthConfigManifest_Basic:
		return "basic", map[string]any{
			"username":     v.Basic.GetUsername(),
			"password_ref": v.Basic.GetPasswordSecretRef(),
		}
	case *controlplanev1.AuthConfigManifest_Oauth2:
		return "oauth2", map[string]any{
			"token_url":         v.Oauth2.GetTokenUrl(),
			"client_id":         v.Oauth2.GetClientId(),
			"client_secret_ref": v.Oauth2.GetClientSecretRef(),
			"scopes":            v.Oauth2.GetScopes(),
		}
	case *controlplanev1.AuthConfigManifest_Hmac:
		return "hmac", map[string]any{
			"algorithm":        v.Hmac.GetAlgorithm(),
			"secret_ref":       v.Hmac.GetSecretRef(),
			"signature_header": v.Hmac.GetSignatureHeader(),
		}
	case *controlplanev1.AuthConfigManifest_Mtls:
		return "mtls", map[string]any{
			"client_cert_ref": v.Mtls.GetClientCertSecretRef(),
			"client_key_ref":  v.Mtls.GetClientKeySecretRef(),
			"ca_cert_ref":     v.Mtls.GetCaCertSecretRef(),
		}
	default:
		return "", nil
	}
}
