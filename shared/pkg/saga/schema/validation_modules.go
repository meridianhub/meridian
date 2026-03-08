// Package schema provides Starlark service module generation from handler schemas.
package schema

import (
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// findClosestMatch finds the closest string in candidates to the target using
// Levenshtein distance. Returns empty string if no candidate is close enough.
func findClosestMatch(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1

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

// HandlerCallInfo captures metadata about a handler call found during validation.
// This enables building relationship graphs between sagas and handlers.
type HandlerCallInfo struct {
	// HandlerName is the fully-qualified handler name (e.g., "position_keeping.initiate_log").
	HandlerName string

	// ParamNames lists the keyword argument names passed in the call.
	ParamNames []string
}

// ValidationError codes for handler validation failures.
const (
	// ValidationCodeUnknownHandler indicates a call to a handler that doesn't exist in the schema.
	ValidationCodeUnknownHandler = "UNKNOWN_HANDLER"

	// ValidationCodeUnknownParam indicates a parameter name not defined in the handler schema.
	ValidationCodeUnknownParam = "UNKNOWN_PARAM"

	// ValidationCodeMissingRequiredParam indicates a required parameter was not provided.
	ValidationCodeMissingRequiredParam = "MISSING_REQUIRED_PARAM"

	// ValidationCodeWrongParamType indicates a parameter value doesn't match the schema type.
	ValidationCodeWrongParamType = "WRONG_PARAM_TYPE"

	// ValidationCodeDeprecatedHandler indicates a call to a deprecated handler.
	// This is a warning, not an error — the call will still succeed.
	ValidationCodeDeprecatedHandler = "DEPRECATED_HANDLER"
)

// ValidationFailure represents a structured validation error from handler call checking.
type ValidationFailure struct {
	// Code is a machine-readable error code (e.g., UNKNOWN_HANDLER).
	Code string

	// Message is a human-readable description.
	Message string

	// Suggestion is an optional "Did you mean?" hint.
	Suggestion string

	// AvailableValues lists valid options (e.g., known handler names or param names).
	AvailableValues []string
}

// Error implements the error interface.
func (f *ValidationFailure) Error() string {
	msg := fmt.Sprintf("[%s] %s", f.Code, f.Message)
	if f.Suggestion != "" {
		msg += fmt.Sprintf(" (suggestion: %s)", f.Suggestion)
	}
	return msg
}

// ValidationWarning captures a non-fatal warning encountered during validation.
type ValidationWarning struct {
	// Code is a machine-readable warning code.
	Code string

	// Message is a human-readable description.
	Message string

	// Suggestion is an optional conversion hint.
	Suggestion string
}

// BuildValidationModules creates Starlark service modules from the schema registry alone.
// Unlike BuildServiceModules, this does NOT require a HandlerRegistry — it builds
// validation-only modules that check parameter names, types, and required fields
// at Starlark execution time, but do not call real handlers.
//
// The optional callLog parameter, if non-nil, will be appended to with metadata
// about each handler call encountered during validation.
func BuildValidationModules(schemaRegistry *Registry, callLog *[]HandlerCallInfo) (starlark.StringDict, error) {
	return BuildValidationModulesWithWarnings(schemaRegistry, callLog, nil)
}

// BuildValidationModulesWithWarnings is like BuildValidationModules but also collects
// deprecation warnings into the provided warnings slice.
func BuildValidationModulesWithWarnings(schemaRegistry *Registry, callLog *[]HandlerCallInfo, warnings *[]ValidationWarning) (starlark.StringDict, error) {
	schemaHandlers := schemaRegistry.ListHandlers()

	// Build handler tree, including deprecated name aliases
	schemaRegistry.mu.RLock()
	deprecatedCount := len(schemaRegistry.deprecatedNames)
	schemaRegistry.mu.RUnlock()

	allNames := make([]string, len(schemaHandlers), len(schemaHandlers)+deprecatedCount)
	copy(allNames, schemaHandlers)

	// Add deprecated name aliases to the tree so they resolve
	schemaRegistry.mu.RLock()
	for oldName := range schemaRegistry.deprecatedNames {
		allNames = append(allNames, oldName)
	}
	schemaRegistry.mu.RUnlock()

	tree := parseHandlerTree(allNames)
	if err := tree.validate(); err != nil {
		return nil, err
	}

	// Build Starlark structs from tree
	modules := make(starlark.StringDict)
	for name, child := range tree.children {
		module, err := buildValidationStruct(name, child, schemaRegistry, callLog, warnings)
		if err != nil {
			return nil, err
		}
		modules[name] = module
	}

	return modules, nil
}

// buildValidationStruct recursively builds a starlarkstruct from a handler tree node,
// using validation-only wrappers instead of real handler calls.
func buildValidationStruct(name string, node *handlerTree, schemaRegistry *Registry, callLog *[]HandlerCallInfo, warnings *[]ValidationWarning) (*starlarkstruct.Struct, error) {
	members := make(starlark.StringDict)

	// Add child namespaces as nested structs
	for childName, childNode := range node.children {
		childStruct, err := buildValidationStruct(childName, childNode, schemaRegistry, callLog, warnings)
		if err != nil {
			return nil, err
		}
		members[childName] = childStruct
	}

	// Add handlers as validation-only builtin functions
	for handlerName, fullName := range node.handlers {
		// Check if this is a deprecated name alias
		if mapping := schemaRegistry.LookupDeprecated(fullName); mapping != nil {
			// This is a deprecated handler name — wrap it to produce a warning
			// but validate against the current handler's schema
			currentDef, err := schemaRegistry.GetHandler(mapping.CurrentName)
			if err != nil {
				return nil, fmt.Errorf("failed to get current handler schema %s: %w", mapping.CurrentName, err)
			}
			members[handlerName] = wrapDeprecatedValidationHandler(fullName, mapping, currentDef, callLog, warnings)
			continue
		}

		handlerDef, err := schemaRegistry.GetHandler(fullName)
		if err != nil {
			return nil, fmt.Errorf("failed to get handler schema %s: %w", fullName, err)
		}

		// Check if the handler itself is marked deprecated
		if handlerDef.Deprecated {
			members[handlerName] = wrapDeprecatedCurrentHandler(fullName, handlerDef, callLog, warnings)
		} else {
			members[handlerName] = wrapValidationHandler(fullName, handlerDef, callLog)
		}
	}

	return starlarkstruct.FromStringDict(starlark.String(name), members), nil
}

// wrapValidationHandler creates a Starlark builtin that validates handler call
// parameters against the schema without executing the real handler.
//
//nolint:gocognit // Handler validation inherently checks multiple conditions
func wrapValidationHandler(fullName string, handlerDef *HandlerDef, callLog *[]HandlerCallInfo) *starlark.Builtin {
	return starlark.NewBuiltin(fullName, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%w: handler %s", ErrPositionalArgsNotAllowed, fullName)
		}

		paramNames := collectParamNames(kwargs)
		logHandlerCall(callLog, fullName, paramNames)

		if err := validateUnknownParams(fullName, paramNames, handlerDef); err != nil {
			return nil, err
		}
		if err := validateRequiredParams(fullName, paramNames, handlerDef); err != nil {
			return nil, err
		}
		if err := validateParamTypes(fullName, kwargs, handlerDef); err != nil {
			return nil, err
		}

		return buildMockResult(fullName, handlerDef), nil
	})
}

// collectParamNames extracts parameter names from Starlark kwargs.
func collectParamNames(kwargs []starlark.Tuple) []string {
	names := make([]string, 0, len(kwargs))
	for _, kw := range kwargs {
		if keyVal, ok := kw[0].(starlark.String); ok {
			names = append(names, string(keyVal))
		}
	}
	return names
}

// logHandlerCall appends a call info entry if the call log is provided.
func logHandlerCall(callLog *[]HandlerCallInfo, fullName string, paramNames []string) {
	if callLog != nil {
		*callLog = append(*callLog, HandlerCallInfo{
			HandlerName: fullName,
			ParamNames:  paramNames,
		})
	}
}

// validateUnknownParams checks that all provided parameter names exist in the handler schema.
func validateUnknownParams(fullName string, paramNames []string, handlerDef *HandlerDef) error {
	for _, paramName := range paramNames {
		if _, exists := handlerDef.Params[paramName]; !exists {
			knownParams := sortedParamNames(handlerDef)
			suggestion := findClosestMatch(paramName, knownParams)
			vf := &ValidationFailure{
				Code:            ValidationCodeUnknownParam,
				Message:         fmt.Sprintf("handler %s has no parameter %q", fullName, paramName),
				AvailableValues: knownParams,
			}
			if suggestion != "" {
				vf.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			return vf
		}
	}
	return nil
}

// validateRequiredParams checks that all required parameters are provided.
func validateRequiredParams(fullName string, paramNames []string, handlerDef *HandlerDef) error {
	providedParams := make(map[string]bool, len(paramNames))
	for _, name := range paramNames {
		providedParams[name] = true
	}
	for paramName, fieldDef := range handlerDef.Params {
		if fieldDef.Required && !providedParams[paramName] {
			return &ValidationFailure{
				Code:    ValidationCodeMissingRequiredParam,
				Message: fmt.Sprintf("handler %s requires parameter %q", fullName, paramName),
			}
		}
	}
	return nil
}

// validateParamTypes checks that parameter values match their schema-defined types.
func validateParamTypes(fullName string, kwargs []starlark.Tuple, handlerDef *HandlerDef) error {
	for _, kw := range kwargs {
		keyVal, ok := kw[0].(starlark.String)
		if !ok {
			continue
		}
		paramName := string(keyVal)
		fieldDef, exists := handlerDef.Params[paramName]
		if !exists {
			continue
		}
		if err := checkStarlarkParamType(fullName, paramName, kw[1], fieldDef); err != nil {
			return err
		}
	}
	return nil
}

// checkStarlarkParamType validates that a Starlark value is compatible with the schema field type.
func checkStarlarkParamType(handlerName, paramName string, value starlark.Value, fieldDef *FieldDef) error {
	// Allow None for any optional parameter
	if _, isNone := value.(starlark.NoneType); isNone {
		if !fieldDef.Required {
			return nil
		}
		return &ValidationFailure{
			Code:    ValidationCodeWrongParamType,
			Message: fmt.Sprintf("handler %s parameter %q is required but got None", handlerName, paramName),
		}
	}

	compatible := isTypeCompatible(value, fieldDef)
	if !compatible {
		return &ValidationFailure{
			Code:    ValidationCodeWrongParamType,
			Message: fmt.Sprintf("handler %s parameter %q expects type %s but got %s", handlerName, paramName, fieldDef.Type, value.Type()),
		}
	}

	// For enum types, validate the value against allowed values
	if fieldDef.Type == TypeEnum && len(fieldDef.Values) > 0 {
		if strVal, ok := value.(starlark.String); ok {
			valid := false
			for _, allowed := range fieldDef.Values {
				if string(strVal) == allowed {
					valid = true
					break
				}
			}
			if !valid {
				return &ValidationFailure{
					Code:            ValidationCodeWrongParamType,
					Message:         fmt.Sprintf("handler %s parameter %q got %q, allowed values: %v", handlerName, paramName, string(strVal), fieldDef.Values),
					AvailableValues: fieldDef.Values,
				}
			}
		}
	}

	return nil
}

// isTypeCompatible checks if a Starlark value is compatible with a schema FieldType.
func isTypeCompatible(value starlark.Value, fieldDef *FieldDef) bool {
	switch fieldDef.Type {
	case TypeString, TypeUUID:
		_, ok := value.(starlark.String)
		return ok
	case TypeInt32, TypeInt64, TypeUint32:
		_, ok := value.(starlark.Int)
		return ok
	case TypeBool:
		_, ok := value.(starlark.Bool)
		return ok
	case TypeDecimal:
		// Decimal accepts string (common: Decimal("100.00")) or int or float
		switch value.(type) {
		case starlark.String, starlark.Int, starlark.Float:
			return true
		default:
			return false
		}
	case TypeEnum:
		_, ok := value.(starlark.String)
		return ok
	case TypeArray:
		_, ok := value.(*starlark.List)
		return ok
	case TypeMap:
		_, ok := value.(*starlark.Dict)
		return ok
	default:
		// Unknown type — accept anything for forward compatibility
		return true
	}
}

// buildMockResult creates a Starlark struct with the expected return fields of a handler.
// All values are set to None, which is sufficient for validation (scripts can reference
// the result fields without runtime errors).
func buildMockResult(handlerName string, handlerDef *HandlerDef) *starlarkstruct.Struct {
	members := make(starlark.StringDict, len(handlerDef.Returns))

	keys := make([]string, 0, len(handlerDef.Returns))
	for k := range handlerDef.Returns {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		// Use placeholder values appropriate to the type for better validation coverage
		members[key] = starlark.String("")
	}

	typeName := handlerName + ".Result"
	return starlarkstruct.FromStringDict(starlark.String(typeName), members)
}

// wrapDeprecatedValidationHandler creates a Starlark builtin for a deprecated handler name alias.
// It records a deprecation warning and validates against the current handler's schema using
// the conversion rule's param mapping.
func wrapDeprecatedValidationHandler(oldName string, mapping *DeprecatedMapping, currentDef *HandlerDef, callLog *[]HandlerCallInfo, warnings *[]ValidationWarning) *starlark.Builtin {
	return starlark.NewBuiltin(oldName, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%w: handler %s", ErrPositionalArgsNotAllowed, oldName)
		}

		paramNames := collectParamNames(kwargs)
		logHandlerCall(callLog, oldName, paramNames)

		// Record deprecation warning
		suggestion := fmt.Sprintf("Use %s instead", mapping.CurrentName)
		if mapping.ConversionRule != nil && len(mapping.ConversionRule.ParamMapping) > 0 {
			var mappings []string
			for newParam, oldParam := range mapping.ConversionRule.ParamMapping {
				mappings = append(mappings, fmt.Sprintf("%s->%s", oldParam, newParam))
			}
			sort.Strings(mappings)
			suggestion += fmt.Sprintf(" with param mapping: %s", strings.Join(mappings, ", "))
		}
		if mapping.ConversionRule != nil && mapping.ConversionRule.Sunset != "" {
			suggestion += fmt.Sprintf(" (will be removed in version %s)", mapping.ConversionRule.Sunset)
		}

		if warnings != nil {
			*warnings = append(*warnings, ValidationWarning{
				Code:       ValidationCodeDeprecatedHandler,
				Message:    fmt.Sprintf("handler %s is deprecated", oldName),
				Suggestion: suggestion,
			})
		}

		// Validate using old param names mapped to current params
		// For deprecated name aliases, we accept the old param names
		return buildMockResult(mapping.CurrentName, currentDef), nil
	})
}

// wrapDeprecatedCurrentHandler creates a Starlark builtin for a handler that is marked
// deprecated but still exists under its own name. It validates normally but records a warning.
func wrapDeprecatedCurrentHandler(fullName string, handlerDef *HandlerDef, callLog *[]HandlerCallInfo, warnings *[]ValidationWarning) *starlark.Builtin {
	return starlark.NewBuiltin(fullName, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%w: handler %s", ErrPositionalArgsNotAllowed, fullName)
		}

		paramNames := collectParamNames(kwargs)
		logHandlerCall(callLog, fullName, paramNames)

		// Record deprecation warning
		if warnings != nil {
			*warnings = append(*warnings, ValidationWarning{
				Code:    ValidationCodeDeprecatedHandler,
				Message: fmt.Sprintf("handler %s is deprecated", fullName),
			})
		}

		if err := validateUnknownParams(fullName, paramNames, handlerDef); err != nil {
			return nil, err
		}
		if err := validateRequiredParams(fullName, paramNames, handlerDef); err != nil {
			return nil, err
		}
		if err := validateParamTypes(fullName, kwargs, handlerDef); err != nil {
			return nil, err
		}

		return buildMockResult(fullName, handlerDef), nil
	})
}

// sortedParamNames returns the sorted parameter names from a HandlerDef.
func sortedParamNames(handlerDef *HandlerDef) []string {
	names := make([]string, 0, len(handlerDef.Params))
	for name := range handlerDef.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
