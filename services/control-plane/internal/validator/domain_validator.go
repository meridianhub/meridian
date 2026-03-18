package validator

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/shopspring/decimal"
)

// accountIDPattern matches valid Stripe Connect account IDs (acct_ followed by 16+ alphanumeric chars).
var accountIDPattern = regexp.MustCompile(`^acct_[A-Za-z0-9]{16,}$`)

// allowedProviders is the set of supported payment rail providers.
var allowedProviders = map[string]bool{
	"stripe_connect": true,
}

// allowedPaymentMethods is the set of supported payment methods.
var allowedPaymentMethods = map[string]bool{
	"card":         true,
	"sepa_debit":   true,
	"bank_account": true,
}

// validatePaymentRails validates all payment rail configurations in the manifest.
func (v *ManifestValidator) validatePaymentRails(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, rail := range manifest.GetPaymentRails() {
		basePath := fmt.Sprintf("payment_rails[%d]", i)

		// Validate provider
		if rail.GetProvider() != "" && !allowedProviders[rail.GetProvider()] {
			providerList := mapKeys(allowedProviders)
			addError(result, ValidationError{
				Severity:        SeverityError,
				Path:            basePath + ".provider",
				Code:            "INVALID_PAYMENT_PROVIDER",
				Message:         fmt.Sprintf("unsupported payment provider %q", rail.GetProvider()),
				AvailableFields: providerList,
				ResourceType:    "payment_rail",
				ResourceID:      rail.GetProvider(),
			})
		}

		// Validate account_id format
		if rail.GetAccountId() != "" && !accountIDPattern.MatchString(rail.GetAccountId()) {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         basePath + ".account_id",
				Code:         "INVALID_ACCOUNT_ID_FORMAT",
				Message:      fmt.Sprintf("account_id %q does not match expected format acct_[A-Za-z0-9]{16,}", rail.GetAccountId()),
				ResourceType: "payment_rail",
				ResourceID:   rail.GetProvider(),
			})
		}

		// Validate platform_fee
		if fee := rail.GetPlatformFee(); fee != nil {
			v.validatePlatformFee(fee, basePath+".platform_fee", result)
		}

		// Validate supported_methods contain only known values
		for j, method := range rail.GetSupportedMethods() {
			if !allowedPaymentMethods[method] {
				methodList := mapKeys(allowedPaymentMethods)
				ve := ValidationError{
					Severity:        SeverityWarning,
					Path:            fmt.Sprintf("%s.supported_methods[%d]", basePath, j),
					Code:            "UNKNOWN_PAYMENT_METHOD",
					Message:         fmt.Sprintf("payment method %q is not a recognized method", method),
					AvailableFields: methodList,
					ResourceType:    "payment_rail",
					ResourceID:      rail.GetProvider(),
				}
				if suggestion := findClosestMatch(method, methodList); suggestion != "" {
					ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
				}
				addError(result, ve)
			}
		}
	}
}

// validatePlatformFee validates the platform fee value is a valid positive decimal.
func (v *ManifestValidator) validatePlatformFee(
	fee *controlplanev1.PlatformFee,
	basePath string,
	result *ValidationResult,
) {
	if fee.GetValue() == "" {
		return
	}

	d, err := decimal.NewFromString(fee.GetValue())
	if err != nil {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".value",
			Code:     "INVALID_PLATFORM_FEE_VALUE",
			Message:  fmt.Sprintf("platform_fee.value %q is not a valid decimal", fee.GetValue()),
		})
		return
	}

	if d.LessThanOrEqual(decimal.Zero) {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".value",
			Code:     "INVALID_PLATFORM_FEE_VALUE",
			Message:  fmt.Sprintf("platform_fee.value must be greater than 0, got %s", fee.GetValue()),
		})
	}
}

// validatePartyTypes validates all party type definitions in the manifest.
func (v *ManifestValidator) validatePartyTypes(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate (tenant_id, party_type) pairs
	seen := make(map[string]int)
	for i, pt := range manifest.GetPartyTypes() {
		key := pt.GetTenantId() + ":" + pt.GetPartyType()
		if prev, exists := seen[key]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("party_types[%d].party_type", i),
				Code:     "DUPLICATE_PARTY_TYPE",
				Message:  fmt.Sprintf("duplicate party_type %q for tenant %q (first defined at party_types[%d])", pt.GetPartyType(), pt.GetTenantId(), prev),
			})
		} else {
			seen[key] = i
		}

		basePath := fmt.Sprintf("party_types[%d]", i)

		// Validate attribute_schema is valid JSON
		if schema := pt.GetAttributeSchema(); schema != "" {
			v.validatePartyTypeSchema(schema, basePath+".attribute_schema", result)
		}

		// Validate CEL expressions
		if expr := pt.GetValidationCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".validation_cel", v.partyTypeCelEnv, celPartyTypeFields, result, "party_type", fmt.Sprintf("%s:%s", pt.GetTenantId(), pt.GetPartyType()))
		}
		if expr := pt.GetEligibilityCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".eligibility_cel", v.partyTypeCelEnv, celPartyTypeFields, result, "party_type", fmt.Sprintf("%s:%s", pt.GetTenantId(), pt.GetPartyType()))
		}
		if expr := pt.GetErrorMessageCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".error_message_cel", v.partyTypeCelEnv, celPartyTypeFields, result, "party_type", fmt.Sprintf("%s:%s", pt.GetTenantId(), pt.GetPartyType()))
		}
	}
}

// validatePartyTypeSchema validates that a party type attribute_schema is valid JSON.
func (v *ManifestValidator) validatePartyTypeSchema(schema, path string, result *ValidationResult) {
	var js json.RawMessage
	if err := json.Unmarshal([]byte(schema), &js); err != nil {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "INVALID_JSON_SCHEMA",
			Message:  fmt.Sprintf("attribute_schema is not valid JSON: %s", err.Error()),
		})
	}
}

// validateMappings validates all MappingDefinition entries in the manifest.
// It enforces no duplicate (name, version) pairs, valid CEL expressions,
// and valid status.
func (v *ManifestValidator) validateMappings(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, mp := range manifest.GetMappings() {
		v.validateSingleMapping(mp, fmt.Sprintf("mappings[%d]", i), result)
	}
}

// validateSingleMapping validates one MappingDefinition entry.
func (v *ManifestValidator) validateSingleMapping(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	v.validateMappingCELExpressions(mp, basePath, result)
	v.validateMappingFields(mp, basePath, result)
	v.validateMappingComputedFields(mp, basePath, result)
	v.validateMappingBatch(mp, basePath, result)
	v.validateMappingStatus(mp, basePath, result)
	v.validateMappingIdempotency(mp, basePath, result)
}

// validateMappingCELExpressions validates inbound/outbound CEL validation expressions.
func (v *ManifestValidator) validateMappingCELExpressions(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	if expr := mp.GetInboundValidationCel(); expr != "" {
		v.validateMappingCELExpression(expr, basePath+".inbound_validation_cel", result)
	}
	if expr := mp.GetOutboundValidationCel(); expr != "" {
		v.validateMappingCELExpression(expr, basePath+".outbound_validation_cel", result)
	}
}

// validateMappingFields validates CEL transforms on field correspondences.
func (v *ManifestValidator) validateMappingFields(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	for j, field := range mp.GetFields() {
		ft := field.GetTransform()
		if ft == nil {
			continue
		}
		celT := ft.GetCel()
		if celT == nil {
			continue
		}
		fieldPath := fmt.Sprintf("%s.fields[%d].transform.cel", basePath, j)
		if expr := celT.GetInboundCel(); expr != "" {
			v.validateMappingCELExpression(expr, fieldPath+".inbound_cel", result)
		}
		if expr := celT.GetOutboundCel(); expr != "" {
			v.validateMappingCELExpression(expr, fieldPath+".outbound_cel", result)
		}
	}
}

// validateMappingComputedFields validates CEL expressions on computed fields.
func (v *ManifestValidator) validateMappingComputedFields(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	for j, cf := range mp.GetInboundComputedFields() {
		if expr := cf.GetCelExpression(); expr != "" {
			v.validateMappingCELExpression(expr, fmt.Sprintf("%s.inbound_computed_fields[%d].cel_expression", basePath, j), result)
		}
	}
	for j, cf := range mp.GetOutboundComputedFields() {
		if expr := cf.GetCelExpression(); expr != "" {
			v.validateMappingCELExpression(expr, fmt.Sprintf("%s.outbound_computed_fields[%d].cel_expression", basePath, j), result)
		}
	}
}

// validateMappingBatch checks batch consistency (is_batch requires batch_target_path).
func (v *ManifestValidator) validateMappingBatch(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	if mp.GetIsBatch() && mp.GetBatchTargetPath() == "" {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".batch_target_path",
			Code:     "MAPPING_BATCH_TARGET_REQUIRED",
			Message:  "batch_target_path must be set when is_batch is true",
		})
	}
}

// validateMappingStatus checks that status is a defined, non-unspecified value.
func (v *ManifestValidator) validateMappingStatus(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	status := mp.GetStatus()
	if status == mappingv1.MappingStatus_MAPPING_STATUS_UNSPECIFIED {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".status",
			Code:     "INVALID_MAPPING_STATUS",
			Message:  "mapping status must be specified (DRAFT, ACTIVE, or DEPRECATED)",
		})
	}
}

// validateMappingIdempotency enforces cross-field constraints on IdempotencyConfig.
// When use_content_hash is false, source_selector must be non-empty.
// When use_content_hash is true, content_hash_fields must have at least one entry.
func (v *ManifestValidator) validateMappingIdempotency(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	cfg := mp.GetIdempotency()
	if cfg == nil {
		return
	}
	idemPath := basePath + ".idempotency"
	if !cfg.GetUseContentHash() && cfg.GetSourceSelector() == "" {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     idemPath + ".source_selector",
			Code:     "IDEMPOTENCY_SOURCE_REQUIRED",
			Message:  "source_selector is required when use_content_hash is false",
		})
	}
	if cfg.GetUseContentHash() && len(cfg.GetContentHashFields()) == 0 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     idemPath + ".content_hash_fields",
			Code:     "IDEMPOTENCY_HASH_FIELDS_REQUIRED",
			Message:  "content_hash_fields must have at least one entry when use_content_hash is true",
		})
	}
}

// validateWebhookTriggers checks that webhook triggers reference provider connections
// defined in the manifest's operational_gateway.provider_connections section.
func (v *ManifestValidator) validateWebhookTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Build lookup of available provider connection IDs.
	connectionIDs := make(map[string]bool)
	var availableIDs []string
	if gw := manifest.GetOperationalGateway(); gw != nil {
		for _, pc := range gw.GetProviderConnections() {
			cid := pc.GetConnectionId()
			connectionIDs[cid] = true
			availableIDs = append(availableIDs, cid)
		}
	}
	sort.Strings(availableIDs)

	for i, saga := range manifest.GetSagas() {
		source := extractWebhookSource(saga.GetTrigger())
		if source == "" {
			continue
		}

		if !connectionIDs[source] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("sagas[%d].trigger", i),
				Code:            "UNKNOWN_WEBHOOK_SOURCE",
				Message:         fmt.Sprintf("webhook source %q does not match any provider connection in operational_gateway.provider_connections", source),
				AvailableFields: availableIDs,
				ResourceType:    "saga",
				ResourceID:      saga.GetName(),
			}
			if suggestion := findClosestMatch(source, availableIDs); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// validateScheduledTriggers enforces that scheduled trigger names are unique across all sagas.
func (v *ManifestValidator) validateScheduledTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Track seen schedule names → first saga index.
	seen := make(map[string]int)

	for i, saga := range manifest.GetSagas() {
		trigger := saga.GetTrigger()
		if !strings.HasPrefix(trigger, "scheduled:") {
			continue
		}

		name := strings.TrimPrefix(trigger, "scheduled:")
		if firstIdx, exists := seen[name]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("sagas[%d].trigger", i),
				Code:     "DUPLICATE_SCHEDULED_TRIGGER",
				Message:  fmt.Sprintf("scheduled trigger name %q already defined at sagas[%d]", name, firstIdx),
			})
		} else {
			seen[name] = i
		}
	}
}

// apiPathPattern validates that API trigger paths start with '/'.
var apiPathPattern = regexp.MustCompile(`^/`)

// validateAPITriggers validates sagas with "api:" trigger prefix.
// It checks path format, uniqueness, and (when an OpenAPI spec is available) endpoint existence.
func (v *ManifestValidator) validateAPITriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	seenPaths := make(map[string]int)
	var availablePaths []string
	if v.apiPathRegistry != nil {
		availablePaths = mapKeys(v.apiPathRegistry)
	}

	for i, saga := range manifest.GetSagas() {
		trigger := saga.GetTrigger()
		if !strings.HasPrefix(trigger, "api:") {
			continue
		}

		path := strings.TrimPrefix(trigger, "api:")
		sagaPath := fmt.Sprintf("sagas[%d].trigger", i)

		// Validate format: must start with '/'
		if !apiPathPattern.MatchString(path) {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         sagaPath,
				Code:         "INVALID_API_PATH_FORMAT",
				Message:      fmt.Sprintf("API trigger path %q must start with '/'", path),
				Suggestion:   "API paths should follow the format '/v1/resource'",
				ResourceType: "saga",
				ResourceID:   saga.GetName(),
			})
			continue
		}

		// Check uniqueness
		if prevIdx, exists := seenPaths[path]; exists {
			addError(result, ValidationError{
				Severity:     SeverityError,
				Path:         sagaPath,
				Code:         "DUPLICATE_API_TRIGGER",
				Message:      fmt.Sprintf("API path %q already bound to saga at sagas[%d]", path, prevIdx),
				ResourceType: "saga",
				ResourceID:   saga.GetName(),
			})
		} else {
			seenPaths[path] = i
		}

		// Check existence in OpenAPI spec (only when spec is available)
		if v.apiPathRegistry != nil && !v.apiPathRegistry[path] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            sagaPath,
				Code:            "UNKNOWN_API_ENDPOINT",
				Message:         fmt.Sprintf("API path %q is not defined in the OpenAPI spec", path),
				AvailableFields: availablePaths,
				ResourceType:    "saga",
				ResourceID:      saga.GetName(),
			}
			if suggestion := findClosestMatch(path, availablePaths); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// tryLoadOpenAPIPaths attempts to load API paths from api/openapi/meridian.swagger.json.
// Returns nil if the file doesn't exist or can't be parsed.
func tryLoadOpenAPIPaths() map[string]bool {
	specPath := findRepoFile("api/openapi/meridian.swagger.json")
	if specPath == "" {
		return nil
	}

	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil
	}

	return parseOpenAPIPaths(data)
}

// parseOpenAPIPaths extracts endpoint paths from an OpenAPI/Swagger JSON spec.
func parseOpenAPIPaths(data []byte) map[string]bool {
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil
	}

	paths := make(map[string]bool, len(spec.Paths))
	for p := range spec.Paths {
		paths[p] = true
	}
	return paths
}
