package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"gopkg.in/yaml.v3"
)

// CEL Balance type fields available in account type policy expressions.
var celBalanceFields = []string{
	"quantity",
	"instrument",
	"bucket_id",
	"as_of",
	"amount",
}

// celPartyTypeFields lists the variables available in party type CEL expressions.
var celPartyTypeFields = []string{"attributes", "party_type"}

// mappingCELAvailableFields lists the variables available in mapping CEL expressions.
var mappingCELAvailableFields = []string{"payload", "value"}

// eventFilterAvailableFields lists the variables available in event trigger filter CEL expressions.
var eventFilterAvailableFields = []string{"event"}

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
				"account_type",
				acctType.GetCode(),
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
				"account_type",
				acctType.GetCode(),
			)
		}
	}
}

// validateCELExpression compiles a single CEL expression and produces structured errors.
// resourceType and resourceID are optional context identifiers (e.g., "account_type", "SETTLEMENT").
func (v *ManifestValidator) validateCELExpression(
	expression string,
	path string,
	env *cel.Env,
	availableFields []string,
	result *ValidationResult,
	resourceType string,
	resourceID string,
) {
	if len(expression) > 4096 {
		addError(result, ValidationError{
			Severity:     SeverityError,
			Path:         path,
			Code:         "CEL_EXPRESSION_TOO_LONG",
			Message:      fmt.Sprintf("CEL expression exceeds maximum length of 4096 bytes (got %d)", len(expression)),
			ResourceType: resourceType,
			ResourceID:   resourceID,
		})
		return
	}

	_, issues := env.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	if strings.Contains(errMsg, "undeclared reference") {
		addError(result, buildCELUndeclaredError(errMsg, path, availableFields, resourceType, resourceID))
		return
	}

	addError(result, ValidationError{
		Severity:     SeverityError,
		Path:         path,
		Code:         "CEL_COMPILATION_ERROR",
		Message:      errMsg,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
}

// buildCELUndeclaredError creates a ValidationError for an undeclared reference
// in a CEL expression, with a "Did you mean?" suggestion when possible.
func buildCELUndeclaredError(errMsg, path string, availableFields []string, resourceType, resourceID string) ValidationError {
	var suggestion string
	undeclaredField := extractUndeclaredReference(errMsg)
	if undeclaredField != "" {
		if match := findClosestMatch(undeclaredField, availableFields); match != "" {
			suggestion = fmt.Sprintf("Did you mean %q?", match)
		}
	}
	return ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "CEL_UNDECLARED_REFERENCE",
		Message:         errMsg,
		Suggestion:      suggestion,
		AvailableFields: availableFields,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
	}
}

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
			ResourceType:    "saga",
			ResourceID:      saga.GetName(),
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

	// Cross-check CEL field references against AsyncAPI schema
	v.validateEventFilterCELFields(saga.GetFilter(), channel, path+".filter", result)
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

// validateEventFilterCELFields cross-checks CEL expression field references against
// the AsyncAPI schema for the given event channel. Produces warnings for unknown fields.
func (v *ManifestValidator) validateEventFilterCELFields(
	expression string,
	channel string,
	path string,
	result *ValidationResult,
) {
	if v.asyncAPISchemas == nil {
		return
	}

	schemaFields, ok := v.asyncAPISchemas[channel]
	if !ok {
		return
	}

	celFields := extractCELFieldRefs(expression, v.eventFilterEnv)
	schemaFieldList := mapKeys(schemaFields)

	for _, field := range celFields {
		if !schemaFields[field] {
			ve := ValidationError{
				Severity:        SeverityWarning,
				Path:            path,
				Code:            "CEL_UNKNOWN_EVENT_FIELD",
				Message:         fmt.Sprintf("field %q is not defined in the AsyncAPI schema for channel %q", field, channel),
				AvailableFields: schemaFieldList,
			}
			if suggestion := findClosestMatch(field, schemaFieldList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// extractCELFieldRefs extracts field names accessed on the "event" variable from a CEL expression.
// For example, "event.amount > 0 && event.currency == 'GBP'" returns ["amount", "currency"].
func extractCELFieldRefs(expression string, env *cel.Env) []string {
	celAST, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil
	}

	parsedExpr, err := cel.AstToParsedExpr(celAST)
	if err != nil {
		return nil
	}

	fields := make(map[string]bool)
	extractFieldsFromExpr(parsedExpr.GetExpr(), fields)

	result := make([]string, 0, len(fields))
	for f := range fields {
		result = append(result, f)
	}
	sort.Strings(result)
	return result
}

// extractFieldsFromExpr recursively walks a CEL proto expression to find
// select expressions on the "event" variable (event.field_name).
func extractFieldsFromExpr(expr *exprpb.Expr, fields map[string]bool) {
	if expr == nil {
		return
	}

	switch e := expr.ExprKind.(type) {
	case *exprpb.Expr_SelectExpr:
		extractSelectField(e.SelectExpr, fields)
	case *exprpb.Expr_CallExpr:
		extractCallFields(e.CallExpr, fields)
	case *exprpb.Expr_ListExpr:
		for _, elem := range e.ListExpr.GetElements() {
			extractFieldsFromExpr(elem, fields)
		}
	case *exprpb.Expr_StructExpr:
		for _, entry := range e.StructExpr.GetEntries() {
			extractFieldsFromExpr(entry.GetValue(), fields)
		}
	case *exprpb.Expr_ComprehensionExpr:
		extractComprehensionFields(e.ComprehensionExpr, fields)
	}
}

func extractSelectField(sel *exprpb.Expr_Select, fields map[string]bool) {
	if ident, ok := sel.GetOperand().ExprKind.(*exprpb.Expr_IdentExpr); ok && ident.IdentExpr.GetName() == "event" {
		fields[sel.GetField()] = true
	} else {
		extractFieldsFromExpr(sel.GetOperand(), fields)
	}
}

func extractCallFields(call *exprpb.Expr_Call, fields map[string]bool) {
	// Handle index operator: event["field_name"] is represented as _[_](event, "field_name")
	if call.GetFunction() == "_[_]" && len(call.GetArgs()) == 2 {
		if ident, ok := call.GetArgs()[0].ExprKind.(*exprpb.Expr_IdentExpr); ok && ident.IdentExpr.GetName() == "event" {
			if constExpr, ok := call.GetArgs()[1].ExprKind.(*exprpb.Expr_ConstExpr); ok {
				if sv := constExpr.ConstExpr.GetStringValue(); sv != "" {
					fields[sv] = true
					return
				}
			}
		}
	}
	extractFieldsFromExpr(call.GetTarget(), fields)
	for _, arg := range call.GetArgs() {
		extractFieldsFromExpr(arg, fields)
	}
}

func extractComprehensionFields(comp *exprpb.Expr_Comprehension, fields map[string]bool) {
	extractFieldsFromExpr(comp.GetIterRange(), fields)
	extractFieldsFromExpr(comp.GetAccuInit(), fields)
	extractFieldsFromExpr(comp.GetLoopCondition(), fields)
	extractFieldsFromExpr(comp.GetLoopStep(), fields)
	extractFieldsFromExpr(comp.GetResult(), fields)
}

// ─── AsyncAPI Schema Loading ────────────────────────────────────────────────

// tryLoadAsyncAPISchemas attempts to load event payload schemas from api/asyncapi/*.yaml.
// Returns nil if the directory doesn't exist or no files are found.
func tryLoadAsyncAPISchemas() map[string]map[string]bool {
	dir := findRepoFile("api/asyncapi")
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	schemas := make(map[string]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		parseAsyncAPIFile(data, schemas)
	}

	if len(schemas) == 0 {
		return nil
	}
	return schemas
}

// asyncAPIDoc represents the minimal structure of an AsyncAPI 3.0 document.
type asyncAPIDoc struct {
	Channels   map[string]asyncAPIChannel `yaml:"channels"`
	Components asyncAPIComponents         `yaml:"components"`
}

type asyncAPIChannel struct {
	Messages map[string]asyncAPIMessageRef `yaml:"messages"`
}

type asyncAPIMessageRef struct {
	Ref string `yaml:"$ref"`
}

type asyncAPIComponents struct {
	Messages map[string]asyncAPIMessage `yaml:"messages"`
	Schemas  map[string]asyncAPISchema  `yaml:"schemas"`
}

type asyncAPIMessage struct {
	Payload asyncAPIPayloadRef `yaml:"payload"`
}

type asyncAPIPayloadRef struct {
	Ref string `yaml:"$ref"`
}

type asyncAPISchema struct {
	Properties map[string]yaml.Node `yaml:"properties"`
}

// parseAsyncAPIFile parses an AsyncAPI YAML file and populates the schemas map.
func parseAsyncAPIFile(data []byte, schemas map[string]map[string]bool) {
	var doc asyncAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return
	}

	for channelName, channel := range doc.Channels {
		for _, msgRef := range channel.Messages {
			fields := resolveMessageFields(msgRef, doc.Components)
			if len(fields) == 0 {
				continue
			}
			existing := schemas[channelName]
			if existing == nil {
				existing = make(map[string]bool, len(fields))
			}
			for _, f := range fields {
				existing[f] = true
			}
			schemas[channelName] = existing
		}
	}
}

// resolveMessageFields follows $ref chains from a message reference to its schema
// and returns the list of top-level property names.
func resolveMessageFields(msgRef asyncAPIMessageRef, components asyncAPIComponents) []string {
	msgName := extractRef(msgRef.Ref)
	if msgName == "" {
		return nil
	}
	msg, ok := components.Messages[msgName]
	if !ok {
		return nil
	}
	schemaName := extractRef(msg.Payload.Ref)
	if schemaName == "" {
		return nil
	}
	s, ok := components.Schemas[schemaName]
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(s.Properties))
	for fieldName := range s.Properties {
		fields = append(fields, fieldName)
	}
	return fields
}

// extractRef extracts the last segment from a JSON/YAML $ref (e.g., "#/components/schemas/Foo" -> "Foo").
func extractRef(ref string) string {
	if ref == "" {
		return ""
	}
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}
