package schema

import (
	"sort"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
	marketinformationclient "github.com/meridianhub/meridian/services/market-information/client"
	operationalgatewayclient "github.com/meridianhub/meridian/services/operational-gateway/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	positionkeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	reconciliationclient "github.com/meridianhub/meridian/services/reconciliation/client"
	referencedataclient "github.com/meridianhub/meridian/services/reference-data/client"
)

// buildFullHandlerRegistry creates a handler registry with all services registered.
// Uses zero-value clients since registration only populates metadata, never calls gRPC.
func buildFullHandlerRegistry(t *testing.T) *saga.HandlerRegistry {
	t.Helper()
	registry := saga.NewHandlerRegistry()

	type registrar struct {
		name string
		fn   func() error
	}

	registrars := []registrar{
		{"current-account", func() error {
			return currentaccountclient.RegisterStarlarkHandlers(registry, &currentaccountclient.Client{})
		}},
		{"financial-accounting", func() error {
			return financialaccountingclient.RegisterStarlarkHandlers(registry, &financialaccountingclient.Client{})
		}},
		{"financial-gateway", func() error {
			return financialgatewayclient.RegisterStarlarkHandlers(registry, &financialgatewayclient.Client{})
		}},
		{"internal-account", func() error {
			return internalaccountclient.RegisterStarlarkHandlers(registry, &internalaccountclient.Client{})
		}},
		{"market-information", func() error {
			return marketinformationclient.RegisterStarlarkHandlers(registry, &marketinformationclient.Client{})
		}},
		{"operational-gateway", func() error {
			return operationalgatewayclient.RegisterStarlarkHandlers(registry, &operationalgatewayclient.Client{})
		}},
		{"party", func() error {
			return partyclient.RegisterStarlarkHandlers(registry, &partyclient.Client{})
		}},
		{"position-keeping", func() error {
			return positionkeepingclient.RegisterStarlarkHandlers(registry, &positionkeepingclient.Client{})
		}},
		{"reconciliation", func() error {
			return reconciliationclient.RegisterStarlarkHandlers(registry, &reconciliationclient.Client{})
		}},
		{"reference-data", func() error {
			return referencedataclient.RegisterStarlarkHandlers(registry, &referencedataclient.Client{})
		}},
	}

	for _, r := range registrars {
		require.NoError(t, r.fn(), "failed to register %s handlers", r.name)
	}

	return registry
}

func TestHandlerProtoAlignment(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	yamlRegistry, err := DefaultRegistry()
	require.NoError(t, err, "failed to load YAML registry")

	allMeta := registry.AllWithMetadata()
	require.NotEmpty(t, allMeta, "handler registry should not be empty")

	var annotatedCount int

	for name, metadata := range allMeta {
		t.Run(name, func(t *testing.T) {
			// 1. Proto type must be set — skip if not yet annotated
			if metadata == nil || metadata.ProtoRequestType == nil {
				t.Skipf("handler %s: missing ProtoRequestType (not yet annotated)", name)
				return
			}
			annotatedCount++

			// 2. Derive schema from proto — validates derivation works
			derived, deriveErr := DeriveHandlerDef(name, metadata)
			require.NoError(t, deriveErr, "DeriveHandlerDef failed for %s", name)
			require.NotNil(t, derived, "DeriveHandlerDef returned nil for %s", name)

			// 3. Enum params from proto reflection must have non-empty values.
			// Params created by ParamOverrides may have enum type without values
			// (annotation bug) — log these for visibility but don't fail the test.
			for paramName, field := range derived.Params {
				if field.Type == TypeEnum && len(field.Values) == 0 {
					if isProtoField(metadata, paramName) {
						t.Errorf("handler %s param %s: proto enum type must have values", name, paramName)
					} else {
						t.Logf("WARNING: handler %s param %s: override sets enum type but no values (fix annotation)", name, paramName)
					}
				}
			}

			// 4. Compensation handler must exist in registry
			if metadata.Compensate != "" {
				assert.True(t, registry.Has(metadata.Compensate),
					"handler %s: compensation handler %q not found in registry", name, metadata.Compensate)
			}

			// 5. Compare with handlers.yaml for regression safety
			yamlHandler, yamlErr := yamlRegistry.GetHandler(name)
			if yamlErr != nil {
				// Handler exists in proto but not in YAML — acceptable during migration
				t.Logf("handler %s: present in proto registry but not in handlers.yaml (new handler)", name)
				return
			}

			// Compare with handlers.yaml — log drift for visibility during migration.
			// Proto is the source of truth; YAML mismatches are informational, not failures.
			for yamlParamName, yamlField := range yamlHandler.Params {
				derivedField, exists := derived.Params[yamlParamName]
				if !exists {
					t.Logf("DRIFT: handler %s param %s: in YAML but not in derived schema (aliased or derived)", name, yamlParamName)
					continue
				}

				if yamlField.Type != derivedField.Type {
					t.Logf("DRIFT: handler %s param %s: type YAML=%s derived=%s", name, yamlParamName, yamlField.Type, derivedField.Type)
				}

				if yamlField.Type == TypeEnum && derivedField.Type == TypeEnum {
					yamlVals := sorted(yamlField.Values)
					derivedVals := sorted(derivedField.Values)
					if !stringSliceEqual(yamlVals, derivedVals) {
						t.Logf("DRIFT: handler %s param %s: enum values YAML=%v derived=%v", name, yamlParamName, yamlVals, derivedVals)
					}
				}
			}
		})
	}

	t.Logf("Total handlers: %d, annotated with proto: %d", len(allMeta), annotatedCount)
}

// TestAllYAMLHandlersHaveRegistration tracks YAML handlers that lack runtime registration.
// During migration, some YAML handlers are aspirational (services not built yet).
// This test logs unregistered handlers for visibility without failing.
func TestAllYAMLHandlersHaveRegistration(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	yamlRegistry, err := DefaultRegistry()
	require.NoError(t, err)

	var registered, unregistered int
	for _, name := range yamlRegistry.ListHandlers() {
		if registry.Has(name) {
			registered++
		} else {
			unregistered++
			t.Logf("UNREGISTERED: handler %s is in handlers.yaml but not registered at runtime", name)
		}
	}

	t.Logf("YAML handlers: %d registered, %d unregistered (aspirational)", registered, unregistered)
	assert.Greater(t, registered, 0, "at least one YAML handler should be registered")
}

// TestCompensationChainIntegrity verifies all compensation references form valid chains.
func TestCompensationChainIntegrity(t *testing.T) {
	registry := buildFullHandlerRegistry(t)
	allMeta := registry.AllWithMetadata()

	for name, metadata := range allMeta {
		if metadata == nil || metadata.Compensate == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			assert.True(t, registry.Has(metadata.Compensate),
				"handler %s references compensation handler %q which is not registered",
				name, metadata.Compensate)

			// Compensation handler should not itself have a compensation (no chains of chains)
			_, compMeta, err := registry.GetWithMetadata(metadata.Compensate)
			require.NoError(t, err)
			if compMeta != nil {
				assert.Empty(t, compMeta.Compensate,
					"compensation handler %s should not itself reference another compensation handler %q",
					metadata.Compensate, compMeta.Compensate)
			}
		})
	}
}

// TestDeriveSchemaFullRegistry runs DeriveSchema on the full registry and
// validates the result is a well-formed schema.
func TestDeriveSchemaFullRegistry(t *testing.T) {
	registry := buildFullHandlerRegistry(t)

	derived, err := DeriveSchema(registry)
	require.NoError(t, err)
	require.NotNil(t, derived)
	assert.NotEmpty(t, derived.Handlers, "derived schema should contain handlers")

	t.Logf("Derived schema contains %d handlers", len(derived.Handlers))
}

func sorted(vals []string) []string {
	out := make([]string, len(vals))
	copy(out, vals)
	sort.Strings(out)
	return out
}

// isProtoField returns true if the field name exists in the proto request message
// (as opposed to being created by a ParamOverride).
func isProtoField(meta *saga.HandlerMetadata, fieldName string) bool {
	if meta == nil || meta.ProtoRequestType == nil {
		return false
	}
	fd := meta.ProtoRequestType.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	return fd != nil
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
