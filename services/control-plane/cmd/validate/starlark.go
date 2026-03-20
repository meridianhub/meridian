package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// skipDirective is the comment that opts a .star file out of schema validation.
const skipDirective = "# schema-validation: skip"

// starlarkValidationResult holds the validation outcome for a single .star file.
type starlarkValidationResult struct {
	File    string
	Pass    bool
	Skipped bool
	Error   string
}

// validateStarlarkFiles validates standalone .star files against the handler schema.
// It builds validation modules from the derived schema and executes each script
// to catch UNKNOWN_HANDLER, UNKNOWN_PARAM, MISSING_REQUIRED_PARAM, and WRONG_PARAM_TYPE errors.
func validateStarlarkFiles(glob string, derivedSchema *schema.Schema) ([]starlarkValidationResult, error) {
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files matched pattern: %s", glob)
	}

	schemaReg := schema.NewRegistryFromSchema(derivedSchema)

	var results []starlarkValidationResult
	for _, file := range files {
		result := validateSingleStarlarkFile(file, schemaReg)
		results = append(results, result)
	}
	return results, nil
}

// validateSingleStarlarkFile validates one .star file against the handler schema.
func validateSingleStarlarkFile(path string, schemaReg *schema.Registry) starlarkValidationResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return starlarkValidationResult{File: path, Error: fmt.Sprintf("failed to read file: %v", err)}
	}
	content := string(data)

	// Honor skip directive
	if strings.Contains(content, skipDirective) {
		return starlarkValidationResult{File: path, Pass: true, Skipped: true}
	}

	// Syntax check
	fileOpts := &syntax.FileOptions{}
	if _, parseErr := fileOpts.Parse(filepath.Base(path), content, 0); parseErr != nil {
		return starlarkValidationResult{File: path, Error: fmt.Sprintf("syntax error: %v", parseErr)}
	}

	// Build predeclared environment with schema-validated modules
	predeclared, err := buildStarlarkPredeclared(schemaReg)
	if err != nil {
		return starlarkValidationResult{File: path, Error: fmt.Sprintf("failed to build validation modules: %v", err)}
	}

	// Execute the script
	thread := &starlark.Thread{
		Name:  filepath.Base(path),
		Print: func(_ *starlark.Thread, _ string) {},
	}
	if _, execErr := starlark.ExecFileOptions(fileOpts, thread, filepath.Base(path), content, predeclared); execErr != nil {
		return starlarkValidationResult{File: path, Error: execErr.Error()}
	}

	return starlarkValidationResult{File: path, Pass: true}
}

// buildStarlarkPredeclared constructs the Starlark predeclared environment for
// validating standalone cookbook scripts. It combines:
//   - Schema-validated modules for all registered handlers (strict validation)
//   - Open mock modules for service namespaces not yet in the handler registry
//   - Standard builtins: saga, step, Decimal, input_data
func buildStarlarkPredeclared(schemaReg *schema.Registry) (starlark.StringDict, error) {
	// Build strict validation modules from the schema registry.
	validationModules, err := schema.BuildValidationModules(schemaReg, nil)
	if err != nil {
		return nil, err
	}

	predeclared := make(starlark.StringDict)
	for name, module := range validationModules {
		predeclared[name] = module
	}

	// Add open mock modules for namespaces not covered by the schema.
	// These are service namespaces used in cookbook patterns that either:
	// (a) have no registered handlers yet (experimental/aspirational)
	// (b) use a different service module name than what's registered
	cookbookNamespaces := []string{
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

	// Explicit allowlist of handlers not yet registered but used in cookbook scripts.
	// Misspelled handler names NOT in this list will still fail validation.
	openFallbackHandlers := map[string]map[string]struct{}{
		"current_account": {
			"evaluate_asset_valuation": {},
			"execute_withdrawal":       {},
		},
		"party": {
			"get": {},
		},
		"position_keeping": {
			"get_balance":      {},
			"list_accounts":    {},
			"query_accounts":   {},
			"query_logs":       {},
			"query_positions":  {},
			"retrieve_balance": {},
		},
		"reference_data": {
			"get_account":      {},
			"get_account_type": {},
			"query":            {},
		},
	}

	for _, ns := range cookbookNamespaces {
		if _, registered := predeclared[ns]; !registered {
			predeclared[ns] = &openServiceModule{name: ns}
		} else {
			// Wrap registered modules with hybrid fallback for known unregistered handlers
			if fallbacks, ok := openFallbackHandlers[ns]; ok {
				predeclared[ns] = &hybridServiceModule{
					name:         ns,
					strict:       predeclared[ns],
					open:         &openServiceModule{name: ns},
					openHandlers: fallbacks,
				}
			}
		}
	}

	// Standard builtins

	// saga(name) returns a mock saga object
	predeclared["saga"] = starlark.NewBuiltin("saga", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		members := starlark.StringDict{"name": starlark.String("mock_saga")}
		return starlarkstruct.FromStringDict(starlark.String("Saga"), members), nil
	})

	// step(name) is a no-op step marker
	predeclared["step"] = starlark.NewBuiltin("step", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.None, nil
	})

	// Decimal(s) returns a Float for arithmetic compatibility
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

	// input_data is a permissive dict that returns sensible defaults for any key access.
	predeclared["input_data"] = newPermissiveInputData()

	return predeclared, nil
}

// newPermissiveInputData creates a permissive input_data mock that returns
// sensible defaults for any key access pattern used in cookbook scripts.
func newPermissiveInputData() *permissiveInputDict {
	return &permissiveInputDict{}
}

// permissiveInputDict is a Starlark value that responds to both dict-style
// access (x["key"]) and attribute access (x.get("key")), returning sensible
// defaults for any key. This enables cookbook scripts to execute end-to-end
// so handler parameter validation can fire.
type permissiveInputDict struct{}

func (d *permissiveInputDict) String() string        { return "input_data{}" }
func (d *permissiveInputDict) Type() string          { return "dict" }
func (d *permissiveInputDict) Freeze()               {}
func (d *permissiveInputDict) Truth() starlark.Bool  { return starlark.True }
func (d *permissiveInputDict) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: dict") }

// Get implements starlark.Mapping for x["key"] access.
func (d *permissiveInputDict) Get(key starlark.Value) (v starlark.Value, found bool, err error) {
	name, ok := key.(starlark.String)
	if !ok {
		return starlark.String(""), true, nil
	}
	return permissiveValue(string(name)), true, nil
}

// Attr implements starlark.HasAttrs for x.get("key") access.
func (d *permissiveInputDict) Attr(name string) (starlark.Value, error) {
	if name == "get" {
		return starlark.NewBuiltin("get", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if len(args) >= 2 {
				return args[1], nil
			}
			if len(args) >= 1 {
				if keyStr, ok := args[0].(starlark.String); ok {
					return permissiveValue(string(keyStr)), nil
				}
			}
			return starlark.String(""), nil
		}), nil
	}
	return nil, nil
}

// AttrNames lists the available methods.
func (d *permissiveInputDict) AttrNames() []string { return []string{"get"} }

// permissiveValue returns a sensible default value for a given key name.
// Integer fields return Int, decimal fields return Float, nested dicts
// return a permissive struct, and everything else returns String.
func permissiveValue(name string) starlark.Value {
	// Integer fields - used where handlers expect int32/int64/uint32
	intFields := map[string]bool{
		"amount_cents": true, "max_members": true, "count": true,
	}
	if intFields[name] || strings.HasSuffix(name, "_cents") || strings.HasSuffix(name, "_minor_units") {
		return starlark.MakeInt(10)
	}
	// Decimal/float fields - used where handlers expect TypeDecimal
	numericFields := map[string]bool{
		"amount": true, "total": true, "balance": true, "quantity": true,
		"units": true, "unit_amount": true, "value": true, "stake_amount": true,
		"gpu_hours": true, "amount_per_unit": true,
	}
	if numericFields[name] {
		return starlark.Float(10.0)
	}

	nestedFields := map[string]bool{
		"metadata": true, "instrument_amount": true, "attributes": true,
	}
	if nestedFields[name] {
		return &permissiveResultValue{name: "input_data." + name}
	}

	return starlark.String("mock-" + name)
}

// openServiceModule is a Starlark value that responds to any attribute access
// by returning a callable that accepts any kwargs and returns a permissive result.
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

func (m *openServiceModule) Attr(name string) (starlark.Value, error) {
	fullName := m.name + "." + name
	return starlark.NewBuiltin(fullName, func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return &permissiveResultValue{name: fullName + ".Result"}, nil
	}), nil
}

func (m *openServiceModule) AttrNames() []string { return nil }

// hybridServiceModule delegates to strict schema-validated module for
// known handlers, falls back to open mock for handlers in openHandlers.
type hybridServiceModule struct {
	name         string
	strict       starlark.Value
	open         *openServiceModule
	openHandlers map[string]struct{}
}

func (h *hybridServiceModule) String() string       { return h.name }
func (h *hybridServiceModule) Type() string         { return "hybrid_service_module" }
func (h *hybridServiceModule) Freeze()              {}
func (h *hybridServiceModule) Truth() starlark.Bool { return starlark.True }
func (h *hybridServiceModule) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: hybrid_service_module")
}

func (h *hybridServiceModule) Attr(name string) (starlark.Value, error) {
	if hasAttr, ok := h.strict.(starlark.HasAttrs); ok {
		val, err := hasAttr.Attr(name)
		if err == nil && val != nil {
			return val, nil
		}
	}
	if _, allowed := h.openHandlers[name]; allowed {
		return h.open.Attr(name)
	}
	return nil, fmt.Errorf("%s.%s: handler not registered and not in open fallback allowlist (possible misspelling?)", h.name, name)
}

func (h *hybridServiceModule) AttrNames() []string {
	if hasAttr, ok := h.strict.(starlark.HasAttrs); ok {
		return hasAttr.AttrNames()
	}
	return nil
}

// permissiveResultValue is a Starlark value that responds to any attribute access
// or dict-style key access with a placeholder value. It also supports iteration.
type permissiveResultValue struct {
	name string
}

func (r *permissiveResultValue) String() string        { return r.name + "{}" }
func (r *permissiveResultValue) Type() string          { return r.name }
func (r *permissiveResultValue) Freeze()               {}
func (r *permissiveResultValue) Truth() starlark.Bool  { return starlark.True }
func (r *permissiveResultValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: %s", r.name) }

func (r *permissiveResultValue) Attr(name string) (starlark.Value, error) {
	if name == "get" {
		return starlark.NewBuiltin("get", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if len(args) >= 2 {
				return args[1], nil
			}
			return starlark.String(""), nil
		}), nil
	}
	return permissiveResultField(r.name, name), nil
}

func (r *permissiveResultValue) AttrNames() []string { return nil }

// Get implements starlark.Mapping for x["key"] access.
func (r *permissiveResultValue) Get(key starlark.Value) (v starlark.Value, found bool, err error) {
	name, ok := key.(starlark.String)
	if !ok {
		return starlark.String(""), true, nil
	}
	return permissiveResultField(r.name, string(name)), true, nil
}

// Len implements starlark.Sequence for len(result).
func (r *permissiveResultValue) Len() int { return 2 }

// Index implements starlark.Indexable for result[i].
func (r *permissiveResultValue) Index(i int) starlark.Value {
	return &permissiveResultValue{name: fmt.Sprintf("%s[%d]", r.name, i)}
}

// Iterate implements starlark.Iterable for "for x in result".
func (r *permissiveResultValue) Iterate() starlark.Iterator {
	items := []starlark.Value{
		&permissiveResultValue{name: r.name + "[0]"},
		&permissiveResultValue{name: r.name + "[1]"},
	}
	return &resultIterator{items: items}
}

type resultIterator struct {
	items []starlark.Value
	index int
}

func (it *resultIterator) Next(p *starlark.Value) bool {
	if it.index >= len(it.items) {
		return false
	}
	*p = it.items[it.index]
	it.index++
	return true
}

func (it *resultIterator) Done() {}

// permissiveResultField returns a placeholder value for a result field.
func permissiveResultField(contextName, name string) starlark.Value {
	listFields := map[string]bool{
		"valuation_methods": true, "items": true, "results": true, "participants": true,
	}
	if listFields[name] {
		return starlark.NewList([]starlark.Value{
			&permissiveResultValue{name: contextName + "." + name + "[0]"},
			&permissiveResultValue{name: contextName + "." + name + "[1]"},
		})
	}
	intFields := map[string]bool{"count": true}
	if intFields[name] {
		return starlark.MakeInt(0)
	}
	if name == "max_members" {
		return starlark.MakeInt(100)
	}
	numericFields := map[string]bool{
		"amount": true, "total": true, "balance": true, "quantity": true,
		"units": true, "unit_amount": true, "value": true, "stake_amount": true,
	}
	if numericFields[name] {
		return starlark.Float(0)
	}
	nestedFields := map[string]bool{
		"metadata": true, "instrument_amount": true, "attributes": true,
	}
	if nestedFields[name] {
		return &permissiveResultValue{name: contextName + "." + name}
	}
	return starlark.String("")
}
