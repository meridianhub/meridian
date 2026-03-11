package cookbook_test

// Schema validation tests for cookbook Starlark saga scripts.
// These tests verify that handler calls in cookbook patterns reference valid
// handlers with correct parameter names and types, using the proto-derived
// handler schema as the source of truth.
//
// For handlers that are registered in service clients, schema validation is
// strict: unknown parameters, missing required parameters, and wrong types
// all produce test failures. For handlers not yet registered (experimental
// or aspirational patterns), a pass-through mock is used so the test validates
// the script's overall structure without blocking on handler availability.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"

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

// openServiceModule is a Starlark value that responds to any attribute access
// by returning a callable that accepts any kwargs and returns an open mock struct.
// This is used for service namespaces whose handlers are not yet registered in
// the handler registry (experimental or aspirational patterns in the cookbook).
type openServiceModule struct {
	name string
}

func (m *openServiceModule) String() string       { return m.name }
func (m *openServiceModule) Type() string         { return "open_service_module" }
func (m *openServiceModule) Freeze()              {}
func (m *openServiceModule) Truth() starlark.Bool { return starlark.True }
func (m *openServiceModule) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: open_service_module")
}

// Attr returns a pass-through callable for any handler name.
// The callable accepts any kwargs and returns an open mock struct.
func (m *openServiceModule) Attr(name string) (starlark.Value, error) {
	fullName := m.name + "." + name
	return starlark.NewBuiltin(fullName, func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return &openResultStruct{name: fullName + ".Result"}, nil
	}), nil
}

// AttrNames returns an empty list; the module is open and all names are valid.
func (m *openServiceModule) AttrNames() []string { return nil }

// openResultStruct is a Starlark value that responds to any attribute access
// or dict-style key access with a placeholder value. It also supports iteration.
// Used as the return value from open mock handlers.
//
// It implements:
//   - starlark.HasAttrs: for x.field access
//   - starlark.Mapping: for x["key"] access (metadata["billing_account_id"])
//   - starlark.Iterable: for "for item in result" iteration
type openResultStruct struct {
	name string
}

func (s *openResultStruct) String() string        { return s.name + "{}" }
func (s *openResultStruct) Type() string          { return s.name }
func (s *openResultStruct) Freeze()               {}
func (s *openResultStruct) Truth() starlark.Bool  { return starlark.True }
func (s *openResultStruct) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: %s", s.name) }

// openMockValue returns a placeholder value for any field, with special cases
// for list fields (valuation_methods, items) and numeric fields (amount, count).
// Default return is starlark.String("") which:
//   - Passes schema TypeString and TypeUUID checks
//   - Can be passed to handler params expecting string
//   - Supports string concatenation
func openMockValue(contextName, name string) (starlark.Value, error) {
	// Fields commonly accessed as lists in cookbook scripts
	listFields := map[string]bool{
		"valuation_methods": true,
		"items":             true,
		"results":           true,
		"participants":      true,
	}
	if listFields[name] {
		// Return a list with two open structs (enough for scripts checking len >= 2)
		return starlark.NewList([]starlark.Value{
			&openResultStruct{name: contextName + "." + name + "[0]"},
			&openResultStruct{name: contextName + "." + name + "[1]"},
		}), nil
	}
	// Integer fields (for comparison with int)
	if name == "count" {
		return starlark.MakeInt(0), nil
	}
	// Numeric fields used in arithmetic in cookbook scripts
	numericFields := map[string]bool{
		"amount":      true,
		"total":       true,
		"balance":     true,
		"quantity":    true,
		"units":       true,
		"unit_amount": true,
		"value":       true, // market_data.get_observation().value used as Decimal input
	}
	if numericFields[name] {
		return starlark.Float(0), nil
	}
	// Nested map-like fields: return an openResultStruct with Mapping support
	// so x["key"] and x.get("key") work on them.
	nestedFields := map[string]bool{
		"metadata":          true,
		"instrument_amount": true,
	}
	if nestedFields[name] {
		return &openResultStruct{name: contextName + "." + name}, nil
	}
	// Default: return empty string — compatible with string type checks and
	// string operations, while avoiding "openResultStruct + string" failures.
	return starlark.String(""), nil
}

// Attr returns a placeholder value for any field access.
// Special-cases "get" to return a Starlark Builtin that mimics dict.get().
func (s *openResultStruct) Attr(name string) (starlark.Value, error) {
	// Provide a dict.get()-compatible method so scripts can call x.get("key")
	// or x.get("key", default). Returns empty string (or default) for any key.
	if name == "get" {
		return starlark.NewBuiltin("get", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if len(args) >= 2 {
				return args[1], nil
			}
			return starlark.String(""), nil
		}), nil
	}
	return openMockValue(s.name, name)
}

// AttrNames returns an empty list; the struct is open and all names are valid.
func (s *openResultStruct) AttrNames() []string { return nil }

// Get implements starlark.Mapping so that x["key"] works on openResultStruct.
// This is used for patterns like: metadata["billing_account_id"].
func (s *openResultStruct) Get(key starlark.Value) (starlark.Value, bool, error) {
	name, ok := key.(starlark.String)
	if !ok {
		return nil, false, nil
	}
	v, err := openMockValue(s.name, string(name))
	if err != nil {
		return nil, false, err
	}
	// Unwrap nested openResultStructs to strings for dict key access —
	// scripts typically use metadata["key"] as a string ID for handler params.
	if _, isOpen := v.(*openResultStruct); isOpen {
		return starlark.String(""), true, nil
	}
	return v, true, nil
}

// Iterate implements starlark.Iterable so that "for x in result" works.
// Returns an iterator over two placeholder items (enough for most for-loop patterns).
func (s *openResultStruct) Iterate() starlark.Iterator {
	items := []starlark.Value{
		&openResultStruct{name: s.name + "[0]"},
		&openResultStruct{name: s.name + "[1]"},
	}
	return &openResultIterator{items: items}
}

// openResultIterator iterates over a fixed list of open result structs.
type openResultIterator struct {
	items []starlark.Value
	index int
}

func (it *openResultIterator) Next(p *starlark.Value) bool {
	if it.index >= len(it.items) {
		return false
	}
	*p = it.items[it.index]
	it.index++
	return true
}

func (it *openResultIterator) Done() {}

// buildSchemaHandlerRegistry creates a handler registry with all services registered
// using zero-value clients. Zero-value clients are valid for registration because
// RegisterStarlarkHandlers only populates handler metadata — it never calls gRPC.
func buildSchemaHandlerRegistry(t *testing.T) *saga.HandlerRegistry {
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

// buildCookbookPredeclared constructs the Starlark predeclared environment for
// executing cookbook scripts. It combines:
//   - Schema-validated modules for all registered handlers (strict validation)
//   - Open mock modules for service namespaces with unregistered handlers
//   - Standard cookbook builtins: saga, step, Decimal, input_data
func buildCookbookPredeclared(t *testing.T, schemaReg *schema.Registry) starlark.StringDict {
	t.Helper()

	// Build strict validation modules from the schema registry.
	// These validate unknown params, missing required params, and wrong types.
	validationModules, err := schema.BuildValidationModules(schemaReg, nil)
	require.NoError(t, err, "failed to build validation modules")

	predeclared := make(starlark.StringDict)

	// Add schema-validated modules for registered service namespaces.
	for name, module := range validationModules {
		predeclared[name] = module
	}

	// Supplement with open mock modules for namespaces not covered by the schema.
	// These namespaces appear in cookbook patterns as aspirational or not-yet-registered
	// handlers. We provide pass-through access so the script can execute end-to-end
	// without errors unrelated to handler schema compliance.
	allCookbookNamespaces := []string{
		"current_account",
		"financial_accounting",
		"financial_gateway",
		"internal_account",
		"market_data",
		"market_information",
		"operational_gateway",
		"party",
		"position_keeping",
		"reconciliation",
		"reference_data",
		"repository",
		"valuation_engine",
	}

	for _, ns := range allCookbookNamespaces {
		if _, alreadyRegistered := predeclared[ns]; !alreadyRegistered {
			// Namespace not in schema: provide open mock module
			predeclared[ns] = &openServiceModule{name: ns}
		}
	}

	// For namespaces that ARE in the schema but the cookbook calls methods not
	// yet registered, wrap with a hybrid module. The hybrid uses an explicit
	// allowlist so that misspelled registered handler names still fail validation
	// while known aspirational/experimental handlers pass through.
	//
	// To add a handler to the allowlist, register it in the service client's
	// RegisterStarlarkHandlers (preferred) or add it here with a comment
	// explaining why it's not yet registered.
	openFallbackHandlers := map[string]map[string]struct{}{
		"current_account": {
			// Not yet exposed as Starlark handlers; used in saas-billing patterns
			"evaluate_asset_valuation": {},
			"execute_withdrawal":       {},
		},
		"party": {
			// Simple party lookup; not yet registered
			"get":                        {},
			"get_default_payment_method": {},
		},
		"position_keeping": {
			// Balance and list queries; not yet registered
			"get_balance":      {},
			"list_accounts":    {},
			"query_accounts":   {},
			"query_logs":       {},
			"query_positions":  {},
			"retrieve_balance": {},
		},
		"reference_data": {
			// Reference data lookups; not yet registered
			"get_account":      {},
			"get_account_type": {},
			"query":            {},
		},
	}

	for _, ns := range allCookbookNamespaces {
		if strictModule, ok := predeclared[ns]; ok {
			if _, isOpenModule := strictModule.(*openServiceModule); !isOpenModule {
				// Wrap with a hybrid module: strict for known handlers, open for explicit allowlist
				predeclared[ns] = &hybridServiceModule{
					name:         ns,
					strict:       strictModule,
					open:         &openServiceModule{name: ns},
					openHandlers: openFallbackHandlers[ns],
				}
			}
		}
	}

	// Standard cookbook builtins

	// saga(name) returns a mock saga object
	predeclared["saga"] = starlark.NewBuiltin("saga", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		members := starlark.StringDict{"name": starlark.String("mock_saga")}
		return starlarkstruct.FromStringDict(starlark.String("Saga"), members), nil
	})

	// step(name) is a no-op step marker
	predeclared["step"] = starlark.NewBuiltin("step", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.None, nil
	})

	// Decimal(s) returns a Float so that arithmetic operations (/, *, +, -)
	// work correctly in scripts. Scripts pass Decimal results to handlers as
	// the "amount" parameter which accepts string|int|float (TypeDecimal).
	// Invalid inputs return an error so that broken cookbook scripts fail validation.
	predeclared["Decimal"] = starlark.NewBuiltin("Decimal", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Decimal: expected exactly 1 argument, got %d", len(args))
		}
		switch v := args[0].(type) {
		case starlark.Float:
			return v, nil
		case starlark.Int:
			i64, ok := v.Int64()
			if !ok {
				return nil, fmt.Errorf("Decimal: integer too large to convert")
			}
			return starlark.Float(float64(i64)), nil
		case starlark.String:
			var f float64
			n, err := fmt.Sscanf(string(v), "%f", &f)
			if err != nil || n != 1 {
				return nil, fmt.Errorf("Decimal: cannot parse %q as a number", string(v))
			}
			return starlark.Float(f), nil
		default:
			return nil, fmt.Errorf("Decimal: unsupported type %s", args[0].Type())
		}
	})

	// input_data provides a mock dict with all keys used across cookbook scripts.
	// Scripts use both input_data["key"] (direct access) and input_data.get("key").
	inputData := starlark.NewDict(32)
	commonKeys := map[string]starlark.Value{
		// Payment / stripe pattern
		"tenant_id":         starlark.String("test-tenant"),
		"party_id":          starlark.String("test-party"),
		"amount_cents":      starlark.MakeInt(1000),
		"charge_id":         starlark.String("ch_test"),
		"payment_intent_id": starlark.String("pi_test"),
		"stripe_event_id":   starlark.String("evt_test"),
		// Common
		"amount":          starlark.Float(10.0),
		"instrument_code": starlark.String("GBP"),
		"currency":        starlark.String("GBP"),
		"correlation_id":  starlark.String("corr-test"),
		"log_id":          starlark.String("log-test"),
		"account_id":      starlark.String("acct-test"),
		"transaction_id":  starlark.String("txn-test"),
		"direction":       starlark.String("DEBIT"),
		"from_instrument": starlark.String("KWH"),
		"to_instrument":   starlark.String("GBP"),
		"created_at":      starlark.String("2026-01-01T00:00:00Z"),
		"party_type":      starlark.String("INDIVIDUAL"),
		"event_id":        starlark.String("event-test"),
		// Energy / usage patterns
		"timestamp": starlark.String("2026-01-01T00:00:00Z"),
		"recorded":  starlark.String("2026-01-01T00:00:00Z"),
		"value":     starlark.Float(10.0),
		// Instrument amount dict for multi-asset patterns
		"instrument_amount": func() starlark.Value {
			d := starlark.NewDict(2)
			_ = d.SetKey(starlark.String("amount"), starlark.Float(10.0))
			_ = d.SetKey(starlark.String("instrument_code"), starlark.String("KWH"))
			return d
		}(),
		// SaaS billing patterns
		"billing_period":     starlark.String("2026-01"),
		"usage_account":      starlark.String("acct-usage"),
		"billing_account_id": starlark.String("acct-billing"),
		"gpu_hours":          starlark.Float(1.0),
		"gpu_type":           starlark.String("A100"),
		"job_id":             starlark.String("job-test"),
		// Phantom cost basis / corporate actions
		"amount_per_unit": starlark.Float(1.0),
		"ex_date":         starlark.String("2026-01-01"),
		// Entity distribution
		"resolution_key_value": starlark.String("key-test"),
		"status":               starlark.String("ACTIVE"),
		// Precious metals
		"settlement_account_id": starlark.String("acct-settlement"),
	}
	for k, v := range commonKeys {
		_ = inputData.SetKey(starlark.String(k), v)
	}
	predeclared["input_data"] = inputData

	return predeclared
}

// hybridServiceModule delegates to the strict schema-validated module for
// known handlers, and falls back to the open mock for handlers in openHandlers.
// This enables strict schema validation for registered handlers while allowing
// cookbook scripts to call a known set of not-yet-registered handlers without
// errors. Any handler name that is neither registered nor in openHandlers will
// fail validation (e.g. misspelled handler names).
type hybridServiceModule struct {
	name         string
	strict       starlark.Value
	open         *openServiceModule
	openHandlers map[string]struct{} // explicit allowlist of unregistered handler names
}

func (h *hybridServiceModule) String() string       { return h.name }
func (h *hybridServiceModule) Type() string         { return "hybrid_service_module" }
func (h *hybridServiceModule) Freeze()              {}
func (h *hybridServiceModule) Truth() starlark.Bool { return starlark.True }
func (h *hybridServiceModule) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: hybrid_service_module")
}

// Attr tries the strict module first (schema-validated handlers).
// If the handler is not found in the strict module, it is only allowed to
// fall back to the open mock if explicitly listed in openHandlers.
// Unknown handler names that are neither registered nor in the allowlist
// return an error so that misspelled handler names fail validation.
func (h *hybridServiceModule) Attr(name string) (starlark.Value, error) {
	// Try strict module first (schema-validated handlers)
	if hasAttr, ok := h.strict.(starlark.HasAttrs); ok {
		val, err := hasAttr.Attr(name)
		if err == nil && val != nil {
			return val, nil
		}
	}
	// Fall back to open mock only for handlers in the explicit allowlist
	if _, allowed := h.openHandlers[name]; allowed {
		return h.open.Attr(name)
	}
	return nil, fmt.Errorf("%s.%s: handler not registered and not in open fallback allowlist (possible misspelling?)", h.name, name)
}

// AttrNames returns the names from the strict module only.
func (h *hybridServiceModule) AttrNames() []string {
	if hasAttr, ok := h.strict.(starlark.HasAttrs); ok {
		return hasAttr.AttrNames()
	}
	return nil
}

// cookbookSchemaValidationDir returns the path to the cookbook patterns directory.
func cookbookSchemaValidationDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Join(filepath.Dir(file), "patterns")
}

// TestCookbookSagaScripts_SchemaValidation validates that all Starlark handler calls
// in cookbook patterns are schema-compliant for registered handlers.
//
// For each .star file under patterns/, the test:
//  1. Builds a full handler registry from all service clients (using zero-value clients)
//  2. Derives the handler schema from proto metadata
//  3. Executes the script with schema-validated modules + open mocks for unregistered handlers
//  4. Fails if a registered handler is called with unknown params, missing required params,
//     wrong types, or invalid enum values
//
// Scripts can opt out with a comment line: `# schema-validation: skip`
func TestCookbookSagaScripts_SchemaValidation(t *testing.T) {
	// Build the handler registry and derive the schema once for all scripts.
	registry := buildSchemaHandlerRegistry(t)

	derivedSchema, err := schema.DeriveSchema(registry)
	require.NoError(t, err, "failed to derive schema from handler registry")
	t.Logf("Derived schema contains %d registered handlers", len(derivedSchema.Handlers))

	schemaReg := schema.NewRegistryFromSchema(derivedSchema)

	patternsDir := cookbookSchemaValidationDir(t)

	// Walk all .star files under patterns/
	var starFiles []string
	err = filepath.Walk(patternsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && strings.HasSuffix(path, ".star") {
			starFiles = append(starFiles, path)
		}
		return nil
	})
	require.NoError(t, err, "failed to walk patterns directory")
	require.NotEmpty(t, starFiles, "expected at least one .star file in patterns/")

	fileOpts := &syntax.FileOptions{}

	for _, path := range starFiles {
		relPath, _ := filepath.Rel(filepath.Join(patternsDir, ".."), path)
		t.Run(relPath, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err, "failed to read %s", relPath)
			content := string(data)

			// Honor skip directive
			if strings.Contains(content, "# schema-validation: skip") {
				t.Skipf("skipped by schema-validation: skip directive")
				return
			}

			// Build a fresh predeclared environment per subtest to prevent
			// mutable Starlark dicts (input_data, instrument_amount) from
			// leaking state between scripts.
			predeclared := buildCookbookPredeclared(t, schemaReg)

			thread := &starlark.Thread{Name: relPath}
			_, execErr := starlark.ExecFileOptions(fileOpts, thread, filepath.Base(path), content, predeclared)
			assert.NoError(t, execErr,
				"script %s failed schema validation: %v", relPath, execErr)
		})
	}
}
