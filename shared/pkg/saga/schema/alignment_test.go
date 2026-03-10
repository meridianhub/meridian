package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ---------------------------------------------------------------------------
// 1. Proto <-> Derived Schema Alignment
// ---------------------------------------------------------------------------

// TestProtoParamFieldsExistInDerivedSchema validates that every proto request
// message field (not marked Derived) appears as a param in the derived schema.
func TestProtoParamFieldsExistInDerivedSchema(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		if meta == nil || meta.ProtoRequestType == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err)

			// Collect derived param names (after overrides, aliases, etc.)
			derivedSet := make(map[string]bool, len(derived.Params))
			for pName := range derived.Params {
				derivedSet[pName] = true
			}

			// Every non-derived proto field should appear (possibly under an alias)
			md := meta.ProtoRequestType.ProtoReflect().Descriptor()
			fields := md.Fields()
			for i := 0; i < fields.Len(); i++ {
				fieldName := string(fields.Get(i).Name())

				// Check if override marks it as derived (excluded)
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Derived {
					assert.False(t, derivedSet[fieldName],
						"field %q is marked Derived but still appears in derived params", fieldName)
					continue
				}

				// Check if override aliases it to another name
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Alias != "" {
					assert.True(t, derivedSet[ov.Alias],
						"proto field %q aliased to %q should exist in derived params", fieldName, ov.Alias)
					continue
				}

				assert.True(t, derivedSet[fieldName],
					"proto field %q should exist in derived params", fieldName)
			}
		})
	}
}

// TestProtoResponseFieldsExistInDerivedSchema validates that every proto response
// message field appears as a return field in the derived schema.
func TestProtoResponseFieldsExistInDerivedSchema(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		if meta == nil || meta.ProtoResponseType == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err)

			md := meta.ProtoResponseType.ProtoReflect().Descriptor()
			fields := md.Fields()
			for i := 0; i < fields.Len(); i++ {
				fieldName := string(fields.Get(i).Name())
				_, exists := derived.Returns[fieldName]
				assert.True(t, exists,
					"proto response field %q should exist in derived returns", fieldName)
			}
		})
	}
}

// TestDerivedParamTypesMatchProto validates that derived param types are
// compatible with proto field types (accounting for overrides).
func TestDerivedParamTypesMatchProto(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		if meta == nil || meta.ProtoRequestType == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err)

			md := meta.ProtoRequestType.ProtoReflect().Descriptor()
			fields := md.Fields()
			for i := 0; i < fields.Len(); i++ {
				fd := fields.Get(i)
				fieldName := string(fd.Name())

				// Skip derived fields
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Derived {
					continue
				}

				// Resolve the derived param name (may be aliased)
				derivedName := fieldName
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Alias != "" {
					derivedName = ov.Alias
				}

				derivedField, exists := derived.Params[derivedName]
				if !exists {
					continue // covered by TestProtoParamFieldsExistInDerivedSchema
				}

				// If there's a type override, the derived type should match the override
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Type != "" {
					assert.Equal(t, FieldType(ov.Type), derivedField.Type,
						"param %q: derived type should match override type", derivedName)
					continue
				}

				// Otherwise derived type should match proto-mapped type
				expectedType := protoKindToFieldType(fd.Kind(), fd)
				if fd.IsMap() {
					expectedType = TypeMap
				} else if fd.IsList() {
					expectedType = TypeArray
				}
				assert.Equal(t, expectedType, derivedField.Type,
					"param %q: derived type should match proto type", derivedName)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Go Closures <-> Derived Schema Alignment
// ---------------------------------------------------------------------------

// TestEveryRegisteredHandlerProducesDerivedDef validates that every handler
// in the Go handler registry produces a valid derived schema entry.
func TestEveryRegisteredHandlerProducesDerivedDef(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	require.NotEmpty(t, allMeta, "handler registry should not be empty")

	for name, meta := range allMeta {
		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err, "DeriveHandlerDef should succeed for %s", name)
			require.NotNil(t, derived, "derived def should not be nil for %s", name)
			assert.NotEmpty(t, derived.Description,
				"handler %q should have a description", name)
		})
	}
}

// TestHandlerRegistryCoversExpectedServices ensures all known service prefixes
// have at least one registered handler.
func TestHandlerRegistryCoversExpectedServices(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	handlers := registry.List()

	// Collect unique service prefixes
	servicePrefixes := make(map[string]bool)
	for _, name := range handlers {
		parts := strings.SplitN(name, ".", 2)
		if len(parts) == 2 {
			servicePrefixes[parts[0]] = true
		}
	}

	// Expected service prefixes based on service client packages
	expectedPrefixes := []string{
		"current_account",
		"financial_accounting",
		"financial_gateway",
		"internal_account",
		"market_information",
		"operational_gateway",
		"party",
		"position_keeping",
		"reconciliation",
		"reference_data",
	}

	for _, prefix := range expectedPrefixes {
		assert.True(t, servicePrefixes[prefix],
			"expected service prefix %q to have registered handlers", prefix)
	}
}

// TestAllHandlersHaveMetadata validates that every registered handler has
// non-nil metadata (not just a nil-metadata registration).
func TestAllHandlersHaveMetadata(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, meta,
				"handler %q should have non-nil metadata", name)
		})
	}
}

// TestAllAnnotatedHandlersHaveProtoRequestType validates that handlers with
// descriptions also have ProtoRequestType set. Handlers that intentionally
// lack a ProtoRequestType (e.g., composite handlers) are logged but not failed.
// Note: ProtoRequestType is stored as a typed nil pointer (e.g., (*Type)(nil))
// for reflection, so we use Go interface comparison (!=nil) not reflect-based checks.
func TestAllAnnotatedHandlersHaveProtoRequestType(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	var withProto, withoutProto int
	for name, meta := range allMeta {
		if meta == nil || meta.Description == "" {
			continue
		}
		if meta.ProtoRequestType != nil {
			withProto++
		} else {
			withoutProto++
			t.Logf("INFO: handler %q has description but no ProtoRequestType (composite/custom handler)", name)
		}
	}

	// At least 80% of described handlers should have proto types
	total := withProto + withoutProto
	require.Greater(t, total, 0, "should have at least one described handler")
	coverage := float64(withProto) / float64(total) * 100
	t.Logf("Proto coverage: %d/%d handlers (%.0f%%)", withProto, total, coverage)
	assert.GreaterOrEqual(t, coverage, 80.0,
		"at least 80%% of described handlers should have ProtoRequestType")
}

// ---------------------------------------------------------------------------
// 3. Enum Value Consistency
// ---------------------------------------------------------------------------

// TestEnumValuesMatchProto validates that enum values in derived params match
// the proto enum values (after prefix stripping, excluding UNSPECIFIED).
func TestEnumValuesMatchProto(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		if meta == nil || meta.ProtoRequestType == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err)

			md := meta.ProtoRequestType.ProtoReflect().Descriptor()
			fields := md.Fields()
			for i := 0; i < fields.Len(); i++ {
				fd := fields.Get(i)
				if fd.Kind() != protoreflect.EnumKind {
					continue
				}

				fieldName := string(fd.Name())

				// Skip derived fields
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Derived {
					continue
				}

				// Resolve alias
				derivedName := fieldName
				if ov, ok := meta.ParamOverrides[fieldName]; ok && ov.Alias != "" {
					derivedName = ov.Alias
				}

				derivedField, exists := derived.Params[derivedName]
				if !exists || derivedField.Type != TypeEnum {
					continue
				}

				// Compute expected values from proto enum descriptor
				expectedValues := deriveEnumValues(fd.Enum())
				if len(expectedValues) == 0 {
					continue
				}

				assert.ElementsMatch(t, expectedValues, derivedField.Values,
					"enum param %q: derived values should match proto enum values", derivedName)
			}
		})
	}
}

// TestEnumParamsHaveValues ensures every enum-typed derived param has non-empty
// values, catching annotation bugs where enum type is set without specifying values.
func TestEnumParamsHaveValues(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, meta := range allMeta {
		if meta == nil || meta.ProtoRequestType == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			derived, err := DeriveHandlerDef(name, meta)
			require.NoError(t, err)

			for paramName, field := range derived.Params {
				if field.Type != TypeEnum {
					continue
				}

				// Enum fields from proto should always have values.
				// Override-injected enum fields may not (logged as warning).
				if isProtoField(meta, paramName) {
					assert.NotEmpty(t, field.Values,
						"proto enum param %q should have values", paramName)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. Service Ownership Verification
// ---------------------------------------------------------------------------

// serviceToDirectoryName maps handler prefixes to service directory names.
var serviceToDirectoryName = map[string]string{
	"current_account":      "current-account",
	"financial_accounting": "financial-accounting",
	"financial_gateway":    "financial-gateway",
	"internal_account":     "internal-account",
	"market_information":   "market-information",
	"operational_gateway":  "operational-gateway",
	"party":                "party",
	"position_keeping":     "position-keeping",
	"reconciliation":       "reconciliation",
	"reference_data":       "reference-data",
}

// TestHandlerNamingConvention validates that all handler names follow the
// <service_prefix>.<action> convention and that the prefix maps to a known service.
func TestHandlerNamingConvention(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	handlers := registry.List()

	for _, name := range handlers {
		t.Run(name, func(t *testing.T) {
			parts := strings.SplitN(name, ".", 2)
			require.Len(t, parts, 2,
				"handler name %q must follow <service>.<action> convention", name)

			prefix := parts[0]
			action := parts[1]

			assert.NotEmpty(t, action,
				"handler name %q must have a non-empty action", name)

			_, knownService := serviceToDirectoryName[prefix]
			assert.True(t, knownService,
				"handler prefix %q is not mapped to a known service directory", prefix)
		})
	}
}

// TestServicePrefixConsistency validates that every registered handler prefix
// maps to a known service, and that all expected services have handlers.
func TestServicePrefixConsistency(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	handlers := registry.List()

	// Group handlers by prefix
	byPrefix := make(map[string][]string)
	for _, name := range handlers {
		parts := strings.SplitN(name, ".", 2)
		if len(parts) == 2 {
			byPrefix[parts[0]] = append(byPrefix[parts[0]], name)
		}
	}

	// Every prefix should map to a known service
	for prefix, handlerNames := range byPrefix {
		_, known := serviceToDirectoryName[prefix]
		assert.True(t, known,
			"prefix %q (used by %d handlers: %v) is not mapped to a known service",
			prefix, len(handlerNames), handlerNames)
	}

	// Every known service should have at least one handler
	for prefix, dirName := range serviceToDirectoryName {
		assert.NotEmpty(t, byPrefix[prefix],
			"service %q (dir: %s) should have at least one registered handler",
			prefix, dirName)
	}
}

// ---------------------------------------------------------------------------
// 5. Cross-Cutting Alignment: Compensation Handler Params
// ---------------------------------------------------------------------------

// TestCompensationHandlerParamCompatibility validates that compensation handlers
// accept at least the key params returned by their forward handler (e.g., IDs).
func TestCompensationHandlerParamCompatibility(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	derivedSchema, err := DeriveSchema(registry)
	require.NoError(t, err)

	for name, handler := range derivedSchema.Handlers {
		if handler.Compensate == "" {
			continue
		}

		t.Run(name+"->"+handler.Compensate, func(t *testing.T) {
			compHandler, exists := derivedSchema.Handlers[handler.Compensate]
			require.True(t, exists,
				"compensation handler %q referenced by %q must exist", handler.Compensate, name)

			// Compensation handler should accept params (not be completely empty)
			// This is a soft check since compensation handlers may need different params
			if len(handler.Returns) > 0 {
				assert.NotEmpty(t, compHandler.Params,
					"compensation handler %q should accept params to identify what to compensate",
					handler.Compensate)
			}
		})
	}
}
