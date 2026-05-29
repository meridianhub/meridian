// Package schema provides Starlark service module generation from handler schemas.
// This implements typed service clients that replace magic string handler references
// with direct method calls like `position_keeping.initiate_log(...)`.
package schema

import (
	"errors"
	"fmt"
	"sort"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

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
func wrapHandler(fullName string, handler saga.Handler, handlerDef *HandlerDef) *starlark.Builtin {
	return starlark.NewBuiltin(fullName, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%w: handler %s", ErrPositionalArgsNotAllowed, fullName)
		}

		ctx := getStarlarkContext(thread)
		if ctx == nil {
			return nil, ErrMissingStarlarkContext
		}

		if err := authorizeHandlerInvocation(ctx, handlerDef, fullName); err != nil {
			return nil, err
		}

		params, err := convertKwargsToParams(kwargs)
		if err != nil {
			return nil, err
		}

		if err := CoerceParams(params, handlerDef); err != nil {
			return nil, fmt.Errorf("handler %s: %w", fullName, err)
		}
		if err := handlerDef.ValidateParams(params); err != nil {
			return nil, fmt.Errorf("handler %s: %w", fullName, err)
		}

		ctx.IdempotencyKey = ctx.NextIdempotencyKey()

		result, err := handler(ctx, params)
		trackStepResult(thread, fullName, result, err, params, handlerDef)

		if err != nil {
			return nil, err
		}
		return goToStarlarkResult(fullName, result)
	})
}
