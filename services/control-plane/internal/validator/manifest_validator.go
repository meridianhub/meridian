// Package validator provides manifest validation for the control plane.
// It performs structural schema validation, CEL type-checking for policy
// expressions, Starlark compilation for saga scripts, cross-reference
// validation, and immutability checks. All errors are structured with
// location paths and suggestions for AI-friendly feedback loops.
package validator

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Known service bindings available on the saga context object.
// These are the service modules that Starlark scripts can call via ctx.<service>.
var knownServiceBindings = []string{
	"position_keeping",
	"financial_accounting",
	"current_account",
	"valuation_engine",
	"repository",
	"notification",
	"payment_order",
	"reconciliation",
	"reference_data",
}

// Known top-level Starlark builtins provided by the saga runtime.
var knownStarlarkBuiltins = []string{
	"input_data",
	"invoke_handler",
	"party_scope",
	"Decimal",
	"print",
	"len",
	"range",
	"str",
	"int",
	"float",
	"bool",
	"list",
	"dict",
	"tuple",
	"type",
	"True",
	"False",
	"None",
	"hasattr",
	"getattr",
	"enumerate",
	"zip",
	"sorted",
	"reversed",
	"min",
	"max",
	"any",
	"all",
	"hash",
	"repr",
	"fail",
}

// ErrUnhashable is returned when hashing a starlark module is attempted.
var ErrUnhashable = errors.New("unhashable: module")

// CEL Balance type fields available in account type policy expressions.
var celBalanceFields = []string{
	"quantity",
	"instrument",
	"bucket_id",
	"as_of",
	"amount",
}

// Severity indicates the severity of a validation finding.
type Severity string

const (
	// SeverityError blocks manifest activation.
	SeverityError Severity = "error"
	// SeverityWarning allows activation but should be reviewed.
	SeverityWarning Severity = "warning"
)

// ValidationError represents a single validation finding with structured
// location information and optional suggestions for AI feedback.
type ValidationError struct {
	// Severity indicates whether this blocks activation.
	Severity Severity `json:"severity"`

	// Path is the location within the manifest (e.g., "instruments[0].code").
	Path string `json:"path"`

	// Code is a machine-readable error code (e.g., "CEL_TYPE_ERROR").
	Code string `json:"code"`

	// Message is a human-readable description of the issue.
	Message string `json:"message"`

	// Line is the source line number (for script errors).
	Line int `json:"line,omitempty"`

	// Column is the source column number (for script errors).
	Column int `json:"column,omitempty"`

	// Suggestion is a "Did you mean...?" hint for typos.
	Suggestion string `json:"suggestion,omitempty"`

	// AvailableFields lists valid field names when an unknown field is referenced.
	AvailableFields []string `json:"available_fields,omitempty"`
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	msg := fmt.Sprintf("[%s] %s: %s", e.Severity, e.Path, e.Message)
	if e.Suggestion != "" {
		msg += fmt.Sprintf(" (suggestion: %s)", e.Suggestion)
	}
	return msg
}

// ValidationResult contains the outcome of manifest validation.
type ValidationResult struct {
	// Valid is true when there are no errors (warnings are allowed).
	Valid bool `json:"valid"`

	// Errors contains all error-severity findings.
	Errors []ValidationError `json:"errors"`

	// Warnings contains all warning-severity findings.
	Warnings []ValidationError `json:"warnings"`
}

// ManifestValidator validates Meridian manifests.
type ManifestValidator struct {
	protoValidator  protovalidate.Validator
	celEnv          *cel.Env
	bucketCelEnv    *cel.Env
	partyTypeCelEnv *cel.Env
	mappingCelEnv   *cel.Env
	eventFilterEnv  *cel.Env
	channelRegistry map[string]bool
}

// New creates a new ManifestValidator.
func New() (*ManifestValidator, error) {
	pv, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create proto validator: %w", err)
	}

	// CEL environment for account type validation policies.
	// These expressions operate on balance state.
	// Use DynType for numeric fields to allow both int and double literals
	// (e.g., "amount > 0" and "amount > 0.0" both work).
	celEnv, err := cel.NewEnv(
		cel.Variable("quantity", cel.DynType),
		cel.Variable("instrument", cel.StringType),
		cel.Variable("bucket_id", cel.StringType),
		cel.Variable("as_of", cel.TimestampType),
		cel.Variable("amount", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	// CEL environment for bucketing expressions.
	bucketCelEnv, err := cel.NewEnv(
		cel.Variable("instrument_code", cel.StringType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket CEL environment: %w", err)
	}

	// CEL environment for party type validation/eligibility expressions.
	// These expressions operate on party attributes.
	partyTypeCelEnv, err := cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("party_type", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create party type CEL environment: %w", err)
	}

	// CEL environment for mapping validation/transformation expressions.
	// These expressions operate on arbitrary payload fields.
	mappingCelEnv, err := cel.NewEnv(
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("value", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping CEL environment: %w", err)
	}

	// CEL environment for event trigger filter expressions.
	// Filters operate on a dynamic event map representing the domain event payload.
	eventFilterEnv, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create event filter CEL environment: %w", err)
	}

	// Build channel registry from the canonical topic list.
	allTopics := topics.All()
	channelRegistry := make(map[string]bool, len(allTopics))
	for _, topic := range allTopics {
		channelRegistry[topic] = true
	}

	return &ManifestValidator{
		protoValidator:  pv,
		celEnv:          celEnv,
		bucketCelEnv:    bucketCelEnv,
		partyTypeCelEnv: partyTypeCelEnv,
		mappingCelEnv:   mappingCelEnv,
		eventFilterEnv:  eventFilterEnv,
		channelRegistry: channelRegistry,
	}, nil
}

// Validate performs full validation of a manifest.
// If previousManifest is non-nil, immutability checks are also performed.
func (v *ManifestValidator) Validate(
	manifest *controlplanev1.Manifest,
	previousManifest *controlplanev1.Manifest,
) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// 1. Structural validation (protobuf constraints)
	v.validateStructural(manifest, result)

	// 2. Duplicate code detection
	v.validateDuplicates(manifest, result)

	// 3. CEL expression validation
	v.validateCELExpressions(manifest, result)

	// 4. Starlark script compilation
	v.validateStarlarkScripts(manifest, result)

	// 5. Cross-reference validation
	v.validateCrossReferences(manifest, result)

	// 6. Payment rails validation
	v.validatePaymentRails(manifest, result)

	// 7. Party types validation
	v.validatePartyTypes(manifest, result)

	// 8. Mappings validation
	v.validateMappings(manifest, result)

	// 9. Event trigger channel and filter validation
	v.validateEventTriggers(manifest, result)

	// 10. Immutability checks
	if previousManifest != nil {
		v.validateImmutability(manifest, previousManifest, result)
	}

	// Set valid flag based on error count
	result.Valid = len(result.Errors) == 0

	return result
}

// validateStructural uses protovalidate to check protobuf constraints.
func (v *ManifestValidator) validateStructural(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	err := v.protoValidator.Validate(manifest)
	if err == nil {
		return
	}

	var valErr *protovalidate.ValidationError
	if errors.As(err, &valErr) {
		for _, violation := range valErr.Violations {
			path := buildFieldPath(violation)
			message := ""
			if violation.Proto != nil {
				message = violation.Proto.GetMessage()
			}
			if message == "" {
				message = violation.String()
			}
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     path,
				Code:     "PROTO_VALIDATION",
				Message:  message,
			})
		}
	} else {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     "",
			Code:     "PROTO_VALIDATION",
			Message:  err.Error(),
		})
	}
}

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
				Severity: SeverityError,
				Path:     fmt.Sprintf("instruments[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate instrument code %q (first defined at instruments[%d])", inst.GetCode(), prev),
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
				Severity: SeverityError,
				Path:     fmt.Sprintf("account_types[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate account type code %q (first defined at account_types[%d])", acct.GetCode(), prev),
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
				Severity: SeverityError,
				Path:     fmt.Sprintf("sagas[%d].name", i),
				Code:     "DUPLICATE_NAME",
				Message:  fmt.Sprintf("duplicate saga name %q (first defined at sagas[%d])", saga.GetName(), prev),
			})
		} else {
			sagaNames[saga.GetName()] = i
		}
		// Warn when an event-triggered saga has no filter; all events will trigger the saga
		if strings.HasPrefix(saga.GetTrigger(), "event:") && saga.Filter == nil {
			addError(result, ValidationError{
				Severity:   SeverityWarning,
				Path:       fmt.Sprintf("sagas[%d].filter", i),
				Code:       "MISSING_EVENT_FILTER",
				Message:    fmt.Sprintf("saga %q subscribes to event trigger %q without a filter; the saga will execute for every matching event", saga.GetName(), saga.GetTrigger()),
				Suggestion: `Add a CEL filter expression, e.g. filter: 'event.amount > 0'`,
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
				Severity: SeverityError,
				Path:     fmt.Sprintf("mappings[%d]", i),
				Code:     "DUPLICATE_MAPPING",
				Message:  fmt.Sprintf("duplicate mapping name=%q version=%d (first defined at mappings[%d])", mp.GetName(), mp.GetVersion(), prev),
			})
		} else {
			mappingKeys[key] = i
		}
	}

	// Check duplicate operational_gateway connection_ids and instruction_types
	v.validateOperationalGatewayDuplicates(manifest, result)
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
				Severity: SeverityError,
				Path:     fmt.Sprintf("operational_gateway.provider_connections[%d].connection_id", i),
				Code:     "DUPLICATE_CONNECTION_ID",
				Message:  fmt.Sprintf("duplicate connection_id %q (first defined at provider_connections[%d])", conn.GetConnectionId(), prev),
			})
		} else {
			connectionIDs[conn.GetConnectionId()] = i
		}
	}

	instructionTypes := make(map[string]int)
	for i, route := range gw.GetInstructionRoutes() {
		if prev, exists := instructionTypes[route.GetInstructionType()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("operational_gateway.instruction_routes[%d].instruction_type", i),
				Code:     "DUPLICATE_INSTRUCTION_TYPE",
				Message:  fmt.Sprintf("duplicate instruction_type %q (first defined at instruction_routes[%d])", route.GetInstructionType(), prev),
			})
		} else {
			instructionTypes[route.GetInstructionType()] = i
		}
	}
}

// validateCELExpressions type-checks CEL expressions in account type policies.
func (v *ManifestValidator) validateCELExpressions(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, acctType := range manifest.GetAccountTypes() {
		policies := acctType.GetPolicies()
		if policies == nil {
			continue
		}

		// Validate validation expression
		if expr := policies.GetValidation(); expr != "" {
			v.validateCELExpression(
				expr,
				fmt.Sprintf("account_types[%d].policies.validation", i),
				v.celEnv,
				celBalanceFields,
				result,
			)
		}

		// Validate bucketing expression
		if expr := policies.GetBucketing(); expr != "" {
			v.validateCELExpression(
				expr,
				fmt.Sprintf("account_types[%d].policies.bucketing", i),
				v.bucketCelEnv,
				[]string{"instrument_code", "attributes"},
				result,
			)
		}
	}
}

// validateCELExpression compiles a single CEL expression and produces structured errors.
func (v *ManifestValidator) validateCELExpression(
	expression string,
	path string,
	env *cel.Env,
	availableFields []string,
	result *ValidationResult,
) {
	// Check length constraint
	if len(expression) > 4096 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 4096 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := env.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	// Check for undeclared reference errors to provide field suggestions
	if strings.Contains(errMsg, "undeclared reference") {
		// Extract the undeclared field name from the error
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			suggestion = findClosestMatch(undeclaredField, availableFields)
			if suggestion != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
		}

		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: availableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
}

// validateStarlarkScripts compiles each saga's Starlark script.
func (v *ManifestValidator) validateStarlarkScripts(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, saga := range manifest.GetSagas() {
		script := saga.GetScript()
		if script == "" {
			continue
		}
		v.validateSingleStarlarkScript(saga, script, fmt.Sprintf("sagas[%d].script", i), result)
	}
}

// validateSingleStarlarkScript compiles and validates one Starlark script.
func (v *ManifestValidator) validateSingleStarlarkScript(
	saga *controlplanev1.SagaDefinition,
	script string,
	path string,
	result *ValidationResult,
) {
	if len(script) > 65536 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "STARLARK_SCRIPT_TOO_LARGE",
			Message:  fmt.Sprintf("Starlark script exceeds maximum size of 65536 bytes (got %d)", len(script)),
		})
		return
	}

	fileOpts := &syntax.FileOptions{}
	_, parseErr := fileOpts.Parse(saga.GetName()+".star", script, 0)
	if parseErr != nil {
		addError(result, parseStarlarkError(parseErr, path))
		return
	}

	predeclared := buildStarlarkPredeclared()
	thread := &starlark.Thread{
		Name:  saga.GetName(),
		Print: func(_ *starlark.Thread, _ string) {},
	}

	_, execErr := starlark.ExecFileOptions(fileOpts, thread, saga.GetName()+".star", script, predeclared)
	if execErr != nil {
		ve := parseStarlarkError(execErr, path)
		addStarlarkUndefinedSuggestion(execErr, &ve)
		addError(result, ve)
	}
}

// addStarlarkUndefinedSuggestion enriches a validation error with a "Did you mean?" suggestion
// when the Starlark error is about an undefined name.
func addStarlarkUndefinedSuggestion(execErr error, ve *ValidationError) {
	if !strings.Contains(execErr.Error(), "undefined") {
		return
	}
	undefinedName := extractUndefinedStarlarkName(execErr.Error())
	if undefinedName == "" {
		return
	}
	allNames := make([]string, 0, len(knownServiceBindings)+len(knownStarlarkBuiltins))
	allNames = append(allNames, knownServiceBindings...)
	allNames = append(allNames, knownStarlarkBuiltins...)
	if suggestion := findClosestMatch(undefinedName, allNames); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
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
			)
		}
	}

	for i, rule := range manifest.GetValuationRules() {
		checkInstrumentRef(
			rule.GetFromInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].from_instrument", i),
			result,
		)
		checkInstrumentRef(
			rule.GetToInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].to_instrument", i),
			result,
		)
	}

	// Validate operational_gateway cross-references
	v.validateOperationalGatewayCrossRefs(manifest, result)
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

// mappingCELAvailableFields lists the variables available in mapping CEL expressions.
var mappingCELAvailableFields = []string{"payload", "value"}

// validateMappingCELExpression compiles a single CEL expression for mapping contexts.
// Uses a simplified CEL environment with payload and value variables.
func (v *ManifestValidator) validateMappingCELExpression(
	expression string,
	path string,
	result *ValidationResult,
) {
	if len(expression) > 2048 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 2048 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := v.mappingCelEnv.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	// Check for undeclared reference errors to provide field suggestions.
	if strings.Contains(errMsg, "undeclared reference") {
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			if match := findClosestMatch(undeclaredField, mappingCELAvailableFields); match != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", match)
			}
		}
		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: mappingCELAvailableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
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

// eventFilterAvailableFields lists the variables available in event trigger filter CEL expressions.
var eventFilterAvailableFields = []string{"event"}

// validateEventTriggers validates all event-triggered sagas in the manifest.
// It checks that the channel referenced after "event:" exists in the topic registry,
// and that any filter expression is valid CEL.
func (v *ManifestValidator) validateEventTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, saga := range manifest.GetSagas() {
		if !strings.HasPrefix(saga.GetTrigger(), "event:") {
			continue
		}
		path := fmt.Sprintf("sagas[%d]", i)
		v.validateEventTrigger(saga, path, result)
	}
}

// validateEventTrigger validates a single event-triggered saga definition.
// It checks channel existence and, if a filter is provided, validates it as CEL.
func (v *ManifestValidator) validateEventTrigger(
	saga *controlplanev1.SagaDefinition,
	path string,
	result *ValidationResult,
) {
	channel := strings.TrimPrefix(saga.GetTrigger(), "event:")

	if !v.channelRegistry[channel] {
		availableChans := v.availableChannels()
		ve := ValidationError{
			Severity:        SeverityError,
			Path:            path + ".trigger",
			Code:            "INVALID_EVENT_CHANNEL",
			Message:         fmt.Sprintf("unknown event channel %q; must be a registered topic", channel),
			AvailableFields: availableChans,
		}
		if suggestion := findClosestMatch(channel, availableChans); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}

	if saga.Filter == nil || saga.GetFilter() == "" {
		// Missing filter warning is already emitted in validateDuplicates; skip here.
		return
	}

	v.validateEventFilterCEL(saga.GetFilter(), path+".filter", result)
}

// validateEventFilterCEL compiles a CEL expression in the event filter environment.
func (v *ManifestValidator) validateEventFilterCEL(
	expression string,
	path string,
	result *ValidationResult,
) {
	if len(expression) > 4096 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 4096 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := v.eventFilterEnv.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	if strings.Contains(errMsg, "undeclared reference") {
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			if match := findClosestMatch(undeclaredField, eventFilterAvailableFields); match != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", match)
			}
		}
		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: eventFilterAvailableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
}

// availableChannels returns a sorted list of all registered event channel names.
func (v *ManifestValidator) availableChannels() []string {
	return mapKeys(v.channelRegistry)
}

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
			})
		}

		// Validate account_id format
		if rail.GetAccountId() != "" && !accountIDPattern.MatchString(rail.GetAccountId()) {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     basePath + ".account_id",
				Code:     "INVALID_ACCOUNT_ID_FORMAT",
				Message:  fmt.Sprintf("account_id %q does not match expected format acct_[A-Za-z0-9]{16,}", rail.GetAccountId()),
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

// checkInstrumentRef validates that a referenced instrument code exists in the manifest.
func checkInstrumentRef(
	code string,
	validCodes map[string]bool,
	codeList []string,
	path string,
	result *ValidationResult,
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
	}
	if suggestion := findClosestMatch(code, codeList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// celPartyTypeFields lists the variables available in party type CEL expressions.
var celPartyTypeFields = []string{"attributes", "party_type"}

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
			v.validateCELExpression(expr, basePath+".validation_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
		}
		if expr := pt.GetEligibilityCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".eligibility_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
		}
		if expr := pt.GetErrorMessageCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".error_message_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
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

// validateImmutability checks that immutable code fields have not been changed.
func (v *ManifestValidator) validateImmutability(
	current *controlplanev1.Manifest,
	previous *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Build maps of previous codes by index position to detect renames
	prevInstrumentsByIdx := make(map[int]string)
	for i, inst := range previous.GetInstruments() {
		prevInstrumentsByIdx[i] = inst.GetCode()
	}

	prevAccountTypesByIdx := make(map[int]string)
	for i, acct := range previous.GetAccountTypes() {
		prevAccountTypesByIdx[i] = acct.GetCode()
	}

	// Build lookup maps for previous codes
	prevInstruments := make(map[string]bool)
	for _, inst := range previous.GetInstruments() {
		prevInstruments[inst.GetCode()] = true
	}
	prevAccountTypes := make(map[string]bool)
	for _, acct := range previous.GetAccountTypes() {
		prevAccountTypes[acct.GetCode()] = true
	}

	// Check instruments: detect code changes at same index position
	for i, inst := range current.GetInstruments() {
		if prevCode, existed := prevInstrumentsByIdx[i]; existed {
			if inst.GetCode() != prevCode {
				addError(result, ValidationError{
					Severity: SeverityError,
					Path:     fmt.Sprintf("instruments[%d].code", i),
					Code:     "IMMUTABLE_FIELD_CHANGED",
					Message:  fmt.Sprintf("instrument code changed from %q to %q; codes are immutable primary keys", prevCode, inst.GetCode()),
				})
			}
		}
	}

	// Check account types: detect code changes at same index position
	for i, acct := range current.GetAccountTypes() {
		if prevCode, existed := prevAccountTypesByIdx[i]; existed {
			if acct.GetCode() != prevCode {
				addError(result, ValidationError{
					Severity: SeverityError,
					Path:     fmt.Sprintf("account_types[%d].code", i),
					Code:     "IMMUTABLE_FIELD_CHANGED",
					Message:  fmt.Sprintf("account type code changed from %q to %q; codes are immutable primary keys", prevCode, acct.GetCode()),
				})
			}
		}
	}
}

// buildStarlarkPredeclared creates the predeclared dictionary for Starlark compilation.
// This includes service modules as simple struct values and known builtins.
func buildStarlarkPredeclared() starlark.StringDict {
	predeclared := make(starlark.StringDict)

	// Add service modules as empty structs (enough for compilation checking)
	for _, svc := range knownServiceBindings {
		predeclared[svc] = &starlarkModule{name: svc}
	}

	// Add common builtins
	predeclared["input_data"] = starlark.NewDict(0)
	predeclared["invoke_handler"] = starlark.NewBuiltin("invoke_handler",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.None, nil
		})
	predeclared["party_scope"] = starlark.NewDict(0)
	predeclared["Decimal"] = starlark.NewBuiltin("Decimal",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.String("0"), nil
		})

	return predeclared
}

// starlarkModule is a simple stub module for compilation checking.
// It accepts any attribute access, returning another module or a no-op function.
type starlarkModule struct {
	name string
}

func (m *starlarkModule) String() string        { return m.name }
func (m *starlarkModule) Type() string          { return "module" }
func (m *starlarkModule) Freeze()               {}
func (m *starlarkModule) Truth() starlark.Bool  { return true }
func (m *starlarkModule) Hash() (uint32, error) { return 0, ErrUnhashable }

func (m *starlarkModule) Attr(name string) (starlark.Value, error) {
	// Return a callable for any attribute access (simulates service methods)
	return starlark.NewBuiltin(m.name+"."+name,
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.NewDict(0), nil
		}), nil
}

func (m *starlarkModule) AttrNames() []string {
	return nil
}

// buildFieldPath extracts a dotted field path from a protovalidate Violation.
func buildFieldPath(violation *protovalidate.Violation) string {
	if violation.Proto == nil || violation.Proto.GetField() == nil {
		return ""
	}
	elements := violation.Proto.GetField().GetElements()
	if len(elements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(elements))
	for _, elem := range elements {
		parts = append(parts, elem.GetFieldName())
	}
	return strings.Join(parts, ".")
}

// addError appends a validation error to the result in the appropriate list.
func addError(result *ValidationResult, ve ValidationError) {
	if ve.Severity == SeverityWarning {
		result.Warnings = append(result.Warnings, ve)
	} else {
		result.Errors = append(result.Errors, ve)
	}
}

// parseStarlarkError converts a Starlark error into a structured ValidationError.
func parseStarlarkError(err error, basePath string) ValidationError {
	ve := ValidationError{
		Severity: SeverityError,
		Path:     basePath,
		Code:     "STARLARK_COMPILATION_ERROR",
		Message:  err.Error(),
	}

	// Try to extract line/column from Starlark error format "file:line:col: message"
	errStr := err.Error()
	parts := strings.SplitN(errStr, ":", 4)
	if len(parts) >= 3 {
		var line, col int
		if _, scanErr := fmt.Sscanf(parts[1], "%d", &line); scanErr == nil {
			ve.Line = line
		}
		if _, scanErr := fmt.Sscanf(parts[2], "%d", &col); scanErr == nil {
			ve.Column = col
		}
	}

	// Detect syntax errors vs execution errors
	if strings.Contains(errStr, "syntax") || strings.Contains(errStr, "got ") {
		ve.Code = "STARLARK_SYNTAX_ERROR"
	}

	return ve
}

// extractUndeclaredReference extracts the field name from a CEL undeclared reference error.
// Example: "undeclared reference to 'quanity'" -> "quanity"
func extractUndeclaredReference(errMsg string) string {
	const prefix = "undeclared reference to '"
	idx := strings.Index(errMsg, prefix)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(prefix):]
	endIdx := strings.Index(rest, "'")
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// extractUndefinedStarlarkName extracts the name from a Starlark "undefined: X" error.
func extractUndefinedStarlarkName(errMsg string) string {
	const marker = "undefined: "
	idx := strings.Index(errMsg, marker)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(marker):]
	// The name goes until the end of the line or end of string
	endIdx := strings.IndexAny(rest, " \n\t")
	if endIdx < 0 {
		return rest
	}
	return rest[:endIdx]
}

// findClosestMatch finds the closest string in candidates to the target using
// Levenshtein distance. Returns empty string if no candidate is close enough.
func findClosestMatch(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1 // Threshold: must be within half the target length

	for _, candidate := range candidates {
		dist := levenshteinDistance(strings.ToLower(target), strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}

	return bestMatch
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	la := len(a)
	lb := len(b)

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := 0; i <= la; i++ {
		prev[i] = i
	}

	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = del
			if ins < curr[i] {
				curr[i] = ins
			}
			if sub < curr[i] {
				curr[i] = sub
			}
		}
		prev, curr = curr, prev
	}

	return prev[la]
}

// mapKeys returns the sorted keys of a map[string]bool.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
