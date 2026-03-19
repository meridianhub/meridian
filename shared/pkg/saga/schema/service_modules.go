// Package schema provides Starlark service module generation from handler schemas.
// This implements typed service clients that replace magic string handler references
// with direct method calls like `position_keeping.initiate_log(...)`.
package schema

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Thread-local storage key for StarlarkContext.
// This key is used to pass the StarlarkContext through the Starlark thread
// so handlers can access it during execution.
const starlarkContextKey = "saga.StarlarkContext"

// Service module generation errors.
var (
	// ErrNilRegistry is returned when a nil handler registry is passed.
	ErrNilRegistry = errors.New("handler registry is nil")

	// ErrNilSchema is returned when a nil schema is passed.
	ErrNilSchema = errors.New("schema is nil")

	// ErrHandlerMissingFromRegistry is returned when a schema defines a handler
	// that is not registered in the HandlerRegistry.
	ErrHandlerMissingFromRegistry = errors.New("handler defined in schema but not in registry")

	// ErrHandlerMissingSchema is returned when a registered handler has no schema.
	ErrHandlerMissingSchema = errors.New("handler registered but has no schema definition")

	// ErrNamingConflict is returned when a handler name creates a conflict
	// (e.g., "foo.bar" and "foo.bar.baz" where "bar" would be both a handler and namespace).
	ErrNamingConflict = errors.New("handler naming conflict: name used as both handler and namespace")

	// ErrMissingStarlarkContext is returned when the StarlarkContext is not set on the thread.
	ErrMissingStarlarkContext = errors.New("StarlarkContext not set on thread")

	// ErrPositionalArgsNotAllowed is returned when a handler is called with positional args.
	ErrPositionalArgsNotAllowed = errors.New("positional arguments not allowed, use keyword arguments")

	// ErrDictKeyNotString is returned when a Starlark dict has a non-string key.
	ErrDictKeyNotString = errors.New("dict key must be string")

	// ErrDecimalConversion is returned when a value cannot be converted to Decimal.
	ErrDecimalConversion = errors.New("cannot convert to Decimal")

	// ErrHandlerAuthorizationDenied is returned when the executing user lacks the
	// required RBAC permission for a handler with a declared ResourceType.
	ErrHandlerAuthorizationDenied = errors.New("handler authorization denied")

	// ErrNilHandlerDef is returned when a handler schema definition is nil.
	ErrNilHandlerDef = errors.New("nil schema definition")

	// ErrPartialRBACMetadata is returned when only one of resource_type/required_permission is set.
	ErrPartialRBACMetadata = errors.New("resource_type and required_permission must both be set or both be empty")
)

// handlerTree represents a hierarchical tree of handler names.
// Handler names like "service.domain.action" are split into a tree structure:
//
//	service
//	  └─ domain
//	       └─ action (handler)
type handlerTree struct {
	children map[string]*handlerTree // Nested namespaces
	handlers map[string]string       // Handler names at this level (name -> full qualified name)
}

// newHandlerTree creates a new empty handler tree.
func newHandlerTree() *handlerTree {
	return &handlerTree{
		children: make(map[string]*handlerTree),
		handlers: make(map[string]string),
	}
}

// parseHandlerTree builds a handler tree from a list of handler names.
// Handler names are expected in the format "service.action" or "service.domain.action".
func parseHandlerTree(handlerNames []string) *handlerTree {
	root := newHandlerTree()

	for _, name := range handlerNames {
		parts := strings.Split(name, ".")
		if len(parts) < 2 {
			// Invalid handler name, skip
			continue
		}

		current := root

		// Navigate/create the tree structure for all parts except the last
		for i := 0; i < len(parts)-1; i++ {
			part := parts[i]
			if current.children[part] == nil {
				current.children[part] = newHandlerTree()
			}
			current = current.children[part]
		}

		// The last part is the handler name
		handlerName := parts[len(parts)-1]
		current.handlers[handlerName] = name
	}

	return root
}

// findNode finds a node at the given dot-separated path.
// Returns nil if the path does not exist.
func (t *handlerTree) findNode(path string) *handlerTree {
	parts := strings.Split(path, ".")
	current := t

	for _, part := range parts {
		if current.children[part] == nil {
			return nil
		}
		current = current.children[part]
	}

	return current
}

// validate checks the tree for naming conflicts.
// A conflict occurs when a name is used as both a handler and a namespace.
func (t *handlerTree) validate() error {
	return t.validateNode("")
}

func (t *handlerTree) validateNode(path string) error {
	// Check for conflicts at this level
	for handlerName := range t.handlers {
		if _, exists := t.children[handlerName]; exists {
			fullPath := handlerName
			if path != "" {
				fullPath = path + "." + handlerName
			}
			return fmt.Errorf("%w: %q is used as both a handler and a namespace", ErrNamingConflict, fullPath)
		}
	}

	// Recursively validate children
	for name, child := range t.children {
		childPath := name
		if path != "" {
			childPath = path + "." + name
		}
		if err := child.validateNode(childPath); err != nil {
			return err
		}
	}

	return nil
}

// BuildServiceModules creates Starlark service modules from the handler registry.
// It derives the handler schema from proto metadata on the registry using DeriveSchema,
// then returns a StringDict containing all top-level service structs that can be injected
// into the Starlark runtime.
//
// The resulting modules enable typed handler calls like:
//
//	position_keeping.initiate_log(position_id="123", amount="100.00", direction="DEBIT")
func BuildServiceModules(registry *saga.HandlerRegistry) (starlark.StringDict, error) {
	if registry == nil {
		return nil, ErrNilRegistry
	}

	// Derive schema from proto metadata on the handler registry
	derivedSchema, err := DeriveSchema(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to derive schema from handler registry: %w", err)
	}

	return BuildServiceModulesFromSchema(registry, derivedSchema)
}

// BuildServiceModulesFromSchema builds Starlark service modules from a handler registry
// and a pre-built schema. This is the lower-level API used when the caller has a
// pre-constructed schema (e.g., parsed from YAML for testing, or from a validation registry).
// Production code should prefer BuildServiceModules which derives the schema automatically.
func BuildServiceModulesFromSchema(registry *saga.HandlerRegistry, s *Schema) (starlark.StringDict, error) {
	if registry == nil {
		return nil, ErrNilRegistry
	}
	if s == nil {
		return nil, ErrNilSchema
	}

	// Fast-fail: reject handlers with partial RBAC metadata at build time
	for name, handlerDef := range s.Handlers {
		if handlerDef == nil {
			return nil, fmt.Errorf("handler %s: %w", name, ErrNilHandlerDef)
		}
		if (handlerDef.ResourceType == "") != (handlerDef.RequiredPermission == "") {
			return nil, fmt.Errorf("handler %s: %w", name, ErrPartialRBACMetadata)
		}
	}

	// Get all handler names from the schema
	schemaHandlers := make([]string, 0, len(s.Handlers))
	for name := range s.Handlers {
		schemaHandlers = append(schemaHandlers, name)
	}
	sort.Strings(schemaHandlers)

	// Build handler tree
	tree := parseHandlerTree(schemaHandlers)

	// Validate tree structure
	if err := tree.validate(); err != nil {
		return nil, err
	}

	// Build Starlark structs from tree
	modules := make(starlark.StringDict)
	for name, child := range tree.children {
		module, err := buildServiceStruct(name, child, registry, s)
		if err != nil {
			return nil, err
		}
		modules[name] = module
	}

	return modules, nil
}

// buildServiceStruct recursively builds a starlarkstruct from a handler tree node.
func buildServiceStruct(name string, node *handlerTree, registry *saga.HandlerRegistry, derivedSchema *Schema) (*starlarkstruct.Struct, error) {
	members := make(starlark.StringDict)

	// Add child namespaces as nested structs
	for childName, childNode := range node.children {
		childStruct, err := buildServiceStruct(childName, childNode, registry, derivedSchema)
		if err != nil {
			return nil, err
		}
		members[childName] = childStruct
	}

	// Add handlers as builtin functions
	for handlerName, fullName := range node.handlers {
		handler, err := registry.Get(fullName)
		if err != nil {
			return nil, fmt.Errorf("failed to get handler %s: %w", fullName, err)
		}

		handlerDef, ok := derivedSchema.Handlers[fullName]
		if !ok {
			return nil, fmt.Errorf("failed to get handler schema %s: %w", fullName, ErrHandlerNotFound)
		}

		members[handlerName] = wrapHandler(fullName, handler, handlerDef)
	}

	return starlarkstruct.FromStringDict(starlark.String(name), members), nil
}

// wrapHandler creates a Starlark builtin that wraps a Go Handler.
// It handles parameter validation against the schema and type conversion.
//
func wrapHandler(fullName string, handler saga.Handler, handlerDef *HandlerDef) *starlark.Builtin {
	return starlark.NewBuiltin(fullName, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		// Handle positional args (should be empty for handlers) - check first for fast fail
		if len(args) > 0 {
			return nil, fmt.Errorf("%w: handler %s", ErrPositionalArgsNotAllowed, fullName)
		}

		// Get StarlarkContext from thread-local storage
		ctx := getStarlarkContext(thread)
		if ctx == nil {
			return nil, ErrMissingStarlarkContext
		}

		// Authorization check: enforce RBAC when handler declares a ResourceType
		if err := authorizeHandlerInvocation(ctx, handlerDef, fullName); err != nil {
			return nil, err
		}

		// Convert kwargs to Go map
		params, err := convertKwargsToParams(kwargs)
		if err != nil {
			return nil, err
		}

		// Coerce all parameters to their schema-defined types
		if err := CoerceParams(params, handlerDef); err != nil {
			return nil, fmt.Errorf("handler %s: %w", fullName, err)
		}

		// Validate parameters against schema
		if err := handlerDef.ValidateParams(params); err != nil {
			return nil, fmt.Errorf("handler %s: %w", fullName, err)
		}

		// Generate idempotency key for this step
		ctx.IdempotencyKey = ctx.NextIdempotencyKey()

		// Call the handler
		result, err := handler(ctx, params)

		// Always track step results (for both success and failure cases)
		// Get stepResults slice from thread-local storage
		if stepResultsVal := thread.Local("saga.StepResults"); stepResultsVal != nil {
			if stepResults, ok := stepResultsVal.(*[]saga.StepResult); ok {
				// Build step result
				stepResult := saga.StepResult{
					StepName: fullName,
					Success:  err == nil,
					Output:   result,
				}

				if err != nil {
					stepResult.Error = err.Error()
				} else if handlerDef.Compensate != "" {
					// If successful and has compensation handler, capture compensation metadata
					stepResult.CompensateHandler = handlerDef.Compensate

					// Derive compensation params from BOTH forward step output AND input
					compensateParams := make(map[string]any)

					// 1. Copy ALL fields from output (not just _id fields)
					//    Compensation handlers need output fields like version, status, etc.
					if output, ok := result.(map[string]interface{}); ok {
						for key, value := range output {
							compensateParams[key] = value
						}
					}

					// 2. Copy commonly-needed input fields to compensation params
					//    Compensation handlers often need context from the forward step input
					inputFieldsForCompensation := []string{
						"transaction_id", "account_id", "position_id", "direction",
						"amount", "instrument_code", "currency", "booking_log_id", "posting_id", "posting_type",
					}
					for _, field := range inputFieldsForCompensation {
						if value, ok := params[field]; ok {
							compensateParams[field] = value
						}
					}

					// 3. Handle field aliases: position_id and account_id are often interchangeable
					//    If position_id exists but account_id doesn't, copy it as account_id
					if posID, ok := compensateParams["position_id"]; ok {
						if _, hasAcctID := compensateParams["account_id"]; !hasAcctID {
							compensateParams["account_id"] = posID
						}
					}
					// And vice versa
					if acctID, ok := compensateParams["account_id"]; ok {
						if _, hasPosID := compensateParams["position_id"]; !hasPosID {
							compensateParams["position_id"] = acctID
						}
					}

					// 4. Invert direction for financial posting compensations
					//    Compensation postings must use the opposite direction to reverse the original
					if handlerDef.Compensate == "financial_accounting.compensate_posting" {
						if direction, ok := compensateParams["direction"].(string); ok {
							switch direction {
							case "DEBIT":
								compensateParams["direction"] = "CREDIT"
							case "CREDIT":
								compensateParams["direction"] = "DEBIT"
							}
						}
					}

					stepResult.CompensateParams = compensateParams
				}

				*stepResults = append(*stepResults, stepResult)
			}
		}

		if err != nil {
			return nil, err
		}

		// Convert result to Starlark value
		return goToStarlarkResult(fullName, result)
	})
}

// authorizeHandlerInvocation checks RBAC authorization before handler invocation.
//
// Fail-safe rules:
//   - Handlers without RBAC metadata (both empty): allow (backward compatibility)
//   - Partial RBAC metadata (one set, other empty): deny (fail-closed)
//   - Sagas without Claims (system-initiated): allow
//   - Claims present + both RBAC fields set: check that Claims has the required scope
//     formatted as "resource_type:permission" (e.g., "payment_order:write")
func authorizeHandlerInvocation(ctx *saga.StarlarkContext, handlerDef *HandlerDef, fullName string) error {
	// No RBAC metadata declared: backward compatibility, allow
	if handlerDef.ResourceType == "" && handlerDef.RequiredPermission == "" {
		return nil
	}

	// Partial RBAC metadata: fail closed
	if handlerDef.ResourceType == "" || handlerDef.RequiredPermission == "" {
		return fmt.Errorf(
			"%w: handler %s must declare both resource_type and required_permission",
			ErrHandlerAuthorizationDenied,
			fullName,
		)
	}

	// No Claims on context: system-initiated saga, allow
	if ctx.Claims == nil {
		return nil
	}

	// Build the required scope string: "resource_type:permission"
	requiredScope := handlerDef.ResourceType + ":" + handlerDef.RequiredPermission

	// Check if the user has the required scope
	if ctx.Claims.HasScope(requiredScope) {
		return nil
	}

	// Also check role-based access: "resource_type:permission" as a role
	if ctx.Claims.HasRole(requiredScope) {
		return nil
	}

	return fmt.Errorf("%w: handler %s requires permission %q via scope or role", ErrHandlerAuthorizationDenied, fullName, requiredScope)
}

// convertKwargsToParams converts Starlark kwargs to a Go map.
func convertKwargsToParams(kwargs []starlark.Tuple) (map[string]any, error) {
	params := make(map[string]any, len(kwargs))
	for _, kwarg := range kwargs {
		keyVal, ok := kwarg[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%w: got %s", ErrDictKeyNotString, kwarg[0].Type())
		}
		key := string(keyVal)
		value, err := starlarkToGoValue(kwarg[1])
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameter %s: %w", key, err)
		}
		params[key] = value
	}
	return params, nil
}

// setStarlarkContext stores the StarlarkContext in thread-local storage.
func setStarlarkContext(thread *starlark.Thread, ctx *saga.StarlarkContext) {
	thread.SetLocal(starlarkContextKey, ctx)
}

// getStarlarkContext retrieves the StarlarkContext from thread-local storage.
func getStarlarkContext(thread *starlark.Thread) *saga.StarlarkContext {
	val := thread.Local(starlarkContextKey)
	if val == nil {
		return nil
	}
	ctx, ok := val.(*saga.StarlarkContext)
	if !ok {
		return nil
	}
	return ctx
}

// starlarkToGoValue converts a Starlark value to a Go value.
//
func starlarkToGoValue(v starlark.Value) (any, error) {
	switch val := v.(type) {
	case starlark.String:
		return string(val), nil
	case starlark.Int:
		return starlarkIntToGo(val), nil
	case starlark.Float:
		return float64(val), nil
	case starlark.Bool:
		return bool(val), nil
	case starlark.NoneType:
		//nolint:nilnil // nil,nil is the correct representation of Starlark None
		return nil, nil
	case *starlark.List:
		return starlarkListToGo(val)
	case *starlark.Dict:
		return starlarkDictToGo(val)
	case *starlarkstruct.Struct:
		return starlarkStructToGo(val)
	default:
		// For custom types, try to convert to string
		return val.String(), nil
	}
}

// starlarkIntToGo converts a Starlark Int to Go int64 or string (for very large ints).
func starlarkIntToGo(val starlark.Int) any {
	if i, ok := val.Int64(); ok {
		return i
	}
	// Fall back to string for very large ints
	return val.String()
}

// starlarkListToGo converts a Starlark List to Go []any.
func starlarkListToGo(val *starlark.List) ([]any, error) {
	result := make([]any, val.Len())
	for i := 0; i < val.Len(); i++ {
		elem, err := starlarkToGoValue(val.Index(i))
		if err != nil {
			return nil, err
		}
		result[i] = elem
	}
	return result, nil
}

// starlarkDictToGo converts a Starlark Dict to Go map[string]any.
func starlarkDictToGo(val *starlark.Dict) (map[string]any, error) {
	result := make(map[string]any)
	for _, item := range val.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%w: got %s", ErrDictKeyNotString, item[0].Type())
		}
		value, err := starlarkToGoValue(item[1])
		if err != nil {
			return nil, err
		}
		result[string(key)] = value
	}
	return result, nil
}

// starlarkStructToGo converts a Starlark Struct to Go map[string]any.
func starlarkStructToGo(val *starlarkstruct.Struct) (map[string]any, error) {
	result := make(map[string]any)
	for _, attrName := range val.AttrNames() {
		attrVal, _ := val.Attr(attrName)
		converted, err := starlarkToGoValue(attrVal)
		if err != nil {
			return nil, err
		}
		result[attrName] = converted
	}
	return result, nil
}

// toDecimal converts various Go types to decimal.Decimal.
func toDecimal(v any) (decimal.Decimal, error) {
	switch val := v.(type) {
	case decimal.Decimal:
		return val, nil
	case string:
		return decimal.NewFromString(val)
	case int:
		return decimal.NewFromInt(int64(val)), nil
	case int64:
		return decimal.NewFromInt(val), nil
	case float64:
		return decimal.NewFromFloat(val), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: unsupported type %T", ErrDecimalConversion, v)
	}
}

// goToStarlarkResult converts a Go handler result to a Starlark struct.
// Handler results are expected to be map[string]any which gets converted
// to a branded starlarkstruct.Struct for type-safe field access.
func goToStarlarkResult(handlerName string, result any) (starlark.Value, error) {
	if result == nil {
		return starlark.None, nil
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		// If not a map, convert directly to Starlark value
		return goToStarlarkValue(result)
	}

	// Build struct from map
	members := make(starlark.StringDict, len(resultMap))

	// Sort keys for deterministic output
	keys := make([]string, 0, len(resultMap))
	for k := range resultMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		val, err := goToStarlarkValue(resultMap[key])
		if err != nil {
			return nil, fmt.Errorf("failed to convert result field %s: %w", key, err)
		}
		members[key] = val
	}

	// Create a branded struct with handler name as the type
	// This allows scripts to check type(result) == "position_keeping.initiate_log.Result"
	typeName := handlerName + ".Result"
	return starlarkstruct.FromStringDict(starlark.String(typeName), members), nil
}

// goToStarlarkValue converts a Go value to a Starlark value.
//
func goToStarlarkValue(v any) (starlark.Value, error) {
	if v == nil {
		return starlark.None, nil
	}

	switch val := v.(type) {
	case string:
		return starlark.String(val), nil
	case int:
		return starlark.MakeInt(val), nil
	case int64:
		return starlark.MakeInt64(val), nil
	case int32:
		return starlark.MakeInt(int(val)), nil
	case uint32:
		return starlark.MakeUint(uint(val)), nil
	case float64:
		return starlark.Float(val), nil
	case bool:
		return starlark.Bool(val), nil
	case decimal.Decimal:
		// Convert Decimal to string for lossless representation in Starlark
		// Starlark scripts should use Decimal() builtin to work with these values
		return starlark.String(val.String()), nil
	case []any:
		return goSliceToStarlark(val)
	case []string:
		return goStringSliceToStarlark(val), nil
	case map[string]any:
		return goMapToStarlark(val)
	default:
		// Try to convert to string as fallback
		return starlark.String(fmt.Sprintf("%v", v)), nil
	}
}

// goSliceToStarlark converts a Go []any to a Starlark List.
func goSliceToStarlark(val []any) (*starlark.List, error) {
	list := make([]starlark.Value, len(val))
	for i, elem := range val {
		converted, err := goToStarlarkValue(elem)
		if err != nil {
			return nil, err
		}
		list[i] = converted
	}
	return starlark.NewList(list), nil
}

// goStringSliceToStarlark converts a Go []string to a Starlark List.
func goStringSliceToStarlark(val []string) *starlark.List {
	list := make([]starlark.Value, len(val))
	for i, elem := range val {
		list[i] = starlark.String(elem)
	}
	return starlark.NewList(list)
}

// goMapToStarlark converts a Go map[string]any to a Starlark Dict.
func goMapToStarlark(val map[string]any) (*starlark.Dict, error) {
	dict := starlark.NewDict(len(val))
	for k, v := range val {
		converted, err := goToStarlarkValue(v)
		if err != nil {
			return nil, err
		}
		if err := dict.SetKey(starlark.String(k), converted); err != nil {
			return nil, err
		}
	}
	return dict, nil
}

// SetStarlarkContext is the exported version for external packages to set context.
func SetStarlarkContext(thread *starlark.Thread, ctx *saga.StarlarkContext) {
	setStarlarkContext(thread, ctx)
}

// GetStarlarkContext is the exported version for external packages to get context.
func GetStarlarkContext(thread *starlark.Thread) *saga.StarlarkContext {
	return getStarlarkContext(thread)
}
