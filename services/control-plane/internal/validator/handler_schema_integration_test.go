package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"google.golang.org/protobuf/encoding/protojson"
)

// fullHandlerSchema returns a comprehensive schema covering ALL handlers called
// by example manifests and cookbook patterns. This acts as the "real" handler
// registry that production would use, validating that scripts only call handlers
// with correct parameters and enum values.
func fullHandlerSchema() *schema.Schema {
	return &schema.Schema{
		Service: "meridian",
		Handlers: map[string]*schema.HandlerDef{
			// --- position_keeping ---
			"position_keeping.initiate_log": {
				Params: map[string]*schema.FieldDef{
					"position_id":     {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
					"direction":       {Type: schema.TypeEnum, Required: true, Values: []string{"CREDIT", "DEBIT"}},
					"instrument_code": {Type: schema.TypeString},
					"correlation_id":  {Type: schema.TypeString},
					"attributes":      {Type: schema.TypeMap},
					"description":     {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"log_id": {Type: schema.TypeString},
					"status": {Type: schema.TypeString},
				},
				Compensate: "position_keeping.cancel_log",
			},
			"position_keeping.cancel_log": {
				Params: map[string]*schema.FieldDef{
					"log_id": {Type: schema.TypeString, Required: true},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.query_logs": {
				Params: map[string]*schema.FieldDef{
					"correlation_id":  {Type: schema.TypeString},
					"instrument_code": {Type: schema.TypeString},
					"position_id":     {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"count": {Type: schema.TypeInt32},
					"logs":  {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.query_positions": {
				Params: map[string]*schema.FieldDef{
					"entity_id":       {Type: schema.TypeString},
					"instrument_code": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"positions": {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.query_accounts": {
				Params: map[string]*schema.FieldDef{
					"instrument_code": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"accounts": {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.get_balance": {
				Params: map[string]*schema.FieldDef{
					"account_id": {Type: schema.TypeString, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"amount":          {Type: schema.TypeDecimal},
					"instrument_code": {Type: schema.TypeString},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.list_accounts": {
				Params: map[string]*schema.FieldDef{
					"instrument_code": {Type: schema.TypeString},
					"account_type":    {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"accounts": {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"position_keeping.retrieve_balance": {
				Params: map[string]*schema.FieldDef{
					"account_id":      {Type: schema.TypeString, Required: true},
					"instrument_code": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"amount": {Type: schema.TypeDecimal},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- financial_accounting ---
			"financial_accounting.post_entries": {
				Params: map[string]*schema.FieldDef{
					"entries": {Type: schema.TypeArray},
				},
				Compensate: "financial_accounting.reverse_entries",
			},
			"financial_accounting.reverse_entries": {
				Params: map[string]*schema.FieldDef{
					"entries": {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- financial_gateway ---
			"financial_gateway.dispatch_payment": {
				Params: map[string]*schema.FieldDef{
					"amount":           {Type: schema.TypeDecimal, Required: true},
					"currency":         {Type: schema.TypeString, Required: true},
					"payment_order_id": {Type: schema.TypeString},
					"idempotency_key":  {Type: schema.TypeString},
					"party_id":         {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"payment_id": {Type: schema.TypeString},
					"status":     {Type: schema.TypeString},
				},
				Compensate: "financial_gateway.dispatch_refund",
			},
			"financial_gateway.dispatch_refund": {
				Params: map[string]*schema.FieldDef{
					"payment_id":      {Type: schema.TypeString},
					"amount":          {Type: schema.TypeDecimal},
					"party_id":        {Type: schema.TypeString},
					"idempotency_key": {Type: schema.TypeString},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- reference_data ---
			"reference_data.get_account": {
				Params: map[string]*schema.FieldDef{
					"id": {Type: schema.TypeString, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"account_type_code": {Type: schema.TypeString},
					"metadata":          {Type: schema.TypeMap},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"reference_data.get_account_type": {
				Params: map[string]*schema.FieldDef{
					"code": {Type: schema.TypeString, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"default_conversion_method_id": {Type: schema.TypeString},
					"valuation_methods":            {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"reference_data.query": {
				Params: map[string]*schema.FieldDef{
					"entity_type": {Type: schema.TypeString},
					"filter":      {Type: schema.TypeString},
					"filters":     {Type: schema.TypeMap},
				},
				Returns: map[string]*schema.FieldDef{
					"results": {Type: schema.TypeArray},
					"count":   {Type: schema.TypeInt32},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- valuation_engine ---
			"valuation_engine.compute": {
				Params: map[string]*schema.FieldDef{
					"method_id":       {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
					"from_instrument": {Type: schema.TypeString, Required: true},
					"to_instrument":   {Type: schema.TypeString, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"amount": {Type: schema.TypeDecimal},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- current_account ---
			"current_account.evaluate_asset_valuation": {
				Params: map[string]*schema.FieldDef{
					"account_id":      {Type: schema.TypeString, Required: true},
					"instrument_code": {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"valued_amount": {Type: schema.TypeDecimal},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"current_account.execute_withdrawal": {
				Params: map[string]*schema.FieldDef{
					"account_id":      {Type: schema.TypeString, Required: true},
					"amount":          {Type: schema.TypeDecimal, Required: true},
					"instrument_code": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"transaction_id": {Type: schema.TypeString},
				},
				Compensate: "current_account.reverse_withdrawal",
			},
			"current_account.reverse_withdrawal": {
				Params: map[string]*schema.FieldDef{
					"transaction_id": {Type: schema.TypeString, Required: true},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- repository ---
			"repository.create_entity": {
				Params: map[string]*schema.FieldDef{
					"entity_type": {Type: schema.TypeString, Required: true},
					"entity_id":   {Type: schema.TypeString},
					"attributes":  {Type: schema.TypeMap},
				},
				Returns: map[string]*schema.FieldDef{
					"entity_id": {Type: schema.TypeString},
				},
				Compensate: "repository.delete_entity",
			},
			"repository.delete_entity": {
				Params: map[string]*schema.FieldDef{
					"entity_id": {Type: schema.TypeString, Required: true},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"repository.get_entity": {
				Params: map[string]*schema.FieldDef{
					"entity_id":   {Type: schema.TypeString, Required: true},
					"entity_type": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"entity_id":  {Type: schema.TypeString},
					"attributes": {Type: schema.TypeMap},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"repository.update_entity": {
				Params: map[string]*schema.FieldDef{
					"entity_id":  {Type: schema.TypeString, Required: true},
					"attributes": {Type: schema.TypeMap},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- market_data ---
			"market_data.get_observation": {
				Params: map[string]*schema.FieldDef{
					"source":          {Type: schema.TypeString},
					"dataset_code":    {Type: schema.TypeString},
					"instrument_code": {Type: schema.TypeString},
					"timestamp":       {Type: schema.TypeString},
					"effective_at":    {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"price":     {Type: schema.TypeDecimal},
					"value":     {Type: schema.TypeDecimal},
					"timestamp": {Type: schema.TypeString},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},

			// --- party ---
			"party.get": {
				Params: map[string]*schema.FieldDef{
					"party_id": {Type: schema.TypeString, Required: true},
				},
				Returns: map[string]*schema.FieldDef{
					"party_id":          {Type: schema.TypeString},
					"jurisdiction":      {Type: schema.TypeString},
					"jurisdiction_code": {Type: schema.TypeString},
					"attributes":        {Type: schema.TypeMap},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"party.list_participants": {
				Params: map[string]*schema.FieldDef{
					"org_id":            {Type: schema.TypeString, Required: true},
					"relationship_type": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"participants": {Type: schema.TypeArray},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
			"party.get_structuring_data": {
				Params: map[string]*schema.FieldDef{
					"party_id":          {Type: schema.TypeString, Required: true},
					"entity_type":       {Type: schema.TypeString},
					"org_id":            {Type: schema.TypeString},
					"relationship_type": {Type: schema.TypeString},
				},
				Returns: map[string]*schema.FieldDef{
					"allocation_share": {Type: schema.TypeDecimal},
					"payout_account":   {Type: schema.TypeString},
				},
				CompensationStrategy: schema.CompensationStrategyNone,
			},
		},
	}
}

// cookbookPatternsDir returns the absolute path to cookbook/patterns/ relative to repo root.
func cookbookPatternsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "cookbook", "patterns")
}

// repoRoot returns the absolute path to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	// Navigate from services/control-plane/internal/validator/ to repo root.
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..")
}

// TestExampleManifests_FullHandlerSchemaValidation validates all example manifests
// against the full handler schema, catching missing required parameters, invalid
// enum values, and incorrect result access patterns.
func TestExampleManifests_FullHandlerSchemaValidation(t *testing.T) {
	dir := exampleManifestsDir(t)
	registry := schema.NewRegistryFromSchema(fullHandlerSchema())

	v, err := New(WithSchemaRegistry(registry))
	require.NoError(t, err)

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "no example manifest JSON files found in %s", dir)

	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)

			var manifest controlplanev1.Manifest
			err = protojson.Unmarshal(data, &manifest)
			require.NoError(t, err, "failed to parse %s into proto", name)

			result := v.Validate(&manifest, nil, WithSkipImmutabilityChecks())

			for _, e := range result.Errors {
				t.Errorf("validation error [%s] at %s: %s", e.Code, e.Path, e.Message)
			}
			assert.True(t, result.Valid, "manifest %s has validation errors", name)
		})
	}
}

// TestCookbookStarlarkScripts_HandlerSchemaValidation validates every cookbook
// Starlark script against the full handler schema. Scripts are compiled and
// executed with typed service modules. Handler validation errors (unknown handler,
// unknown param, missing required param, wrong type) are treated as test failures.
// Other runtime errors (dict key access, arithmetic on stub values) are expected
// since cookbook scripts are designed for execution with real service responses.
//
// Scripts containing "# schema-validation: skip" are only checked for syntax.
func TestCookbookStarlarkScripts_HandlerSchemaValidation(t *testing.T) {
	patternsDir := cookbookPatternsDir(t)
	registry := schema.NewRegistryFromSchema(fullHandlerSchema())

	// Build typed service modules from the schema
	var callLog []schema.HandlerCallInfo
	modules, err := schema.BuildValidationModules(registry, &callLog)
	require.NoError(t, err, "failed to build validation modules from schema")

	// Build predeclared with service modules + runtime stubs
	predeclared := buildCookbookPredeclared(modules)

	// Handler validation error codes that indicate schema problems (real bugs)
	handlerErrorCodes := []string{
		schema.ValidationCodeUnknownHandler,
		schema.ValidationCodeUnknownParam,
		schema.ValidationCodeMissingRequiredParam,
		schema.ValidationCodeWrongParamType,
	}

	entries, err := os.ReadDir(patternsDir)
	require.NoError(t, err)

	starFileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		patternName := entry.Name()
		patternDir := filepath.Join(patternsDir, patternName)

		starFiles, err := filepath.Glob(filepath.Join(patternDir, "*.star"))
		require.NoError(t, err)

		for _, starFile := range starFiles {
			starFileCount++
			baseName := filepath.Base(starFile)
			testName := fmt.Sprintf("%s/%s", patternName, baseName)

			t.Run(testName, func(t *testing.T) {
				scriptData, err := os.ReadFile(starFile)
				require.NoError(t, err)

				script := string(scriptData)

				// Parse for syntax errors (always)
				fileOpts := &syntax.FileOptions{}
				_, parseErr := fileOpts.Parse(baseName, script, 0)
				require.NoError(t, parseErr, "Starlark syntax error in %s", testName)

				// Scripts marked "# schema-validation: skip" only get syntax checking
				if strings.Contains(script, "# schema-validation: skip") {
					t.Logf("skipping execution (schema-validation: skip directive)")
					return
				}

				// Execute with typed service modules to validate handler calls
				thread := &starlark.Thread{
					Name:  baseName,
					Print: func(_ *starlark.Thread, _ string) {},
				}
				sandbox.HardenThread(thread, sandbox.DefaultConfig())

				_, execErr := starlark.ExecFileOptions(fileOpts, thread, baseName, script, predeclared)
				if execErr == nil {
					return // clean execution
				}

				// Check if the error is a handler validation error (schema bug)
				errStr := execErr.Error()
				for _, code := range handlerErrorCodes {
					if strings.Contains(errStr, "["+code+"]") {
						t.Errorf("handler schema error in %s: %v", testName, execErr)
						return
					}
				}

				// Also check for struct attribute errors indicating unknown handlers
				if structAttrErrorPattern.MatchString(errStr) {
					t.Errorf("unknown handler in %s: %v", testName, execErr)
					return
				}

				// Other errors (runtime: dict access, arithmetic, etc.) are expected
				// since we don't provide full runtime mocks
				t.Logf("runtime error (expected without full mocks): %v", execErr)
			})
		}
	}

	require.Greater(t, starFileCount, 0, "no .star files found in %s", patternsDir)
	t.Logf("validated %d cookbook Starlark scripts against full handler schema", starFileCount)
}

// buildCookbookPredeclared creates the predeclared dictionary for cookbook Starlark
// validation, including typed service modules and runtime stubs (saga, step, etc.).
func buildCookbookPredeclared(serviceModules starlark.StringDict) starlark.StringDict {
	predeclared := make(starlark.StringDict, len(serviceModules)+10)

	// Add typed service modules
	for name, module := range serviceModules {
		predeclared[name] = module
	}

	// Seed input_data with all keys that cookbook scripts access via ctx["key"].
	// Values are plausible dummies - the test validates handler *calls*, not business logic.
	predeclared["input_data"] = buildSeedInputData()
	predeclared["party_scope"] = starlark.NewDict(0)

	predeclared["Decimal"] = starlark.NewBuiltin("Decimal",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.String("0"), nil
		})

	// saga(name=...) returns a no-op dict-like value
	predeclared["saga"] = starlark.NewBuiltin("saga",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.NewDict(0), nil
		})

	// step(name=...) is a no-op
	predeclared["step"] = starlark.NewBuiltin("step",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.None, nil
		})

	// output is commonly assigned but needs to exist
	predeclared["output"] = starlark.None

	return predeclared
}

// buildSeedInputData creates a Starlark dict pre-populated with all keys that
// cookbook Starlark scripts access from input_data / ctx. This allows scripts
// to execute past dict lookups so handler calls can be validated.
func buildSeedInputData() *starlark.Dict {
	// instrument_amount is a nested dict accessed as ctx["instrument_amount"]["amount"]
	instrumentAmount := starlark.NewDict(2)
	_ = instrumentAmount.SetKey(starlark.String("amount"), starlark.String("100.00"))
	_ = instrumentAmount.SetKey(starlark.String("instrument_code"), starlark.String("KWH"))

	// metadata is a nested dict
	metadata := starlark.NewDict(2)
	_ = metadata.SetKey(starlark.String("billing_account_id"), starlark.String("acc-billing"))
	_ = metadata.SetKey(starlark.String("counterparty_account_id"), starlark.String("acc-counter"))

	seeds := map[string]starlark.Value{
		"account_id":           starlark.String("acc-001"),
		"amount_cents":         starlark.MakeInt(10000),
		"amount_per_unit":      starlark.String("0.15"),
		"billing_period":       starlark.String("2026-03"),
		"charge_id":            starlark.String("charge-001"),
		"correlation_id":       starlark.String("corr-001"),
		"created_by":           starlark.String("system"),
		"direction":            starlark.String("DEBIT"),
		"ex_date":              starlark.String("2026-03-20"),
		"gpu_hours":            starlark.String("10"),
		"gpu_type":             starlark.String("A100"),
		"idempotency_key":      starlark.String("idem-001"),
		"instrument_amount":    instrumentAmount,
		"instrument_code":      starlark.String("KWH"),
		"job_id":               starlark.String("job-001"),
		"match_id":             starlark.String("match-001"),
		"max_members":          starlark.MakeInt(10),
		"metadata":             metadata,
		"party_id":             starlark.String("party-001"),
		"payment_intent_id":    starlark.String("pi_001"),
		"resolution_key_value": starlark.String("key-001"),
		"selection":            starlark.String("runner-1"),
		"stake_amount":         starlark.String("50.00"),
		"syndicate_id":         starlark.String("synd-001"),
		"timestamp":            starlark.String("2026-03-20T12:00:00Z"),
		"usage_account":        starlark.String("acc-usage"),
		"value":                starlark.String("100.00"),
	}

	d := starlark.NewDict(len(seeds))
	for k, v := range seeds {
		_ = d.SetKey(starlark.String(k), v)
	}
	return d
}

// TestHandlerSchema_DetectsMissingRequiredParam verifies that the schema catches
// a script that omits a required parameter (position_id for initiate_log).
func TestHandlerSchema_DetectsMissingRequiredParam(t *testing.T) {
	s := fullHandlerSchema()
	v, err := New(WithDerivedSchema(s))
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "test-missing-param",
			Industry: "test",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{{
			Code: "GBP",
			Name: "British Pound",
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		}},
		Sagas: []*controlplanev1.SagaDefinition{{
			Name:    "test_missing_position_id",
			Trigger: "api:/v1/test",
			Script: `
def execute(ctx):
    position_keeping.initiate_log(amount=Decimal("100"), direction="DEBIT")
execute(input_data)
`,
		}},
	}

	result := v.Validate(manifest, nil, WithSkipImmutabilityChecks())
	assert.False(t, result.Valid, "should detect missing required param position_id")

	found := false
	for _, e := range result.Errors {
		if e.Code == schema.ValidationCodeMissingRequiredParam {
			found = true
			break
		}
	}
	assert.True(t, found, "should have a MISSING_REQUIRED_PARAM error, got: %v", result.Errors)
}

// TestHandlerSchema_DetectsInvalidEnumValue verifies that the schema catches
// a script that uses an invalid enum value for direction.
func TestHandlerSchema_DetectsInvalidEnumValue(t *testing.T) {
	s := fullHandlerSchema()
	v, err := New(WithDerivedSchema(s))
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "test-invalid-enum",
			Industry: "test",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{{
			Code: "GBP",
			Name: "British Pound",
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		}},
		Sagas: []*controlplanev1.SagaDefinition{{
			Name:    "test_invalid_direction",
			Trigger: "api:/v1/test",
			Script: `
def execute(ctx):
    position_keeping.initiate_log(position_id="acc1", amount=Decimal("100"), direction="INVALID")
execute(input_data)
`,
		}},
	}

	result := v.Validate(manifest, nil, WithSkipImmutabilityChecks())
	assert.False(t, result.Valid, "should detect invalid enum value")

	found := false
	for _, e := range result.Errors {
		if e.Code == schema.ValidationCodeWrongParamType {
			found = true
			break
		}
	}
	assert.True(t, found, "should have a WRONG_PARAM_TYPE error for invalid enum, got: %v", result.Errors)
}

// TestHandlerSchema_DetectsUnknownHandler verifies that the schema catches
// a script that calls a handler not registered in the schema.
func TestHandlerSchema_DetectsUnknownHandler(t *testing.T) {
	s := fullHandlerSchema()
	v, err := New(WithDerivedSchema(s))
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "test-unknown-handler",
			Industry: "test",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{{
			Code: "GBP",
			Name: "British Pound",
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		}},
		Sagas: []*controlplanev1.SagaDefinition{{
			Name:    "test_unknown_handler",
			Trigger: "api:/v1/test",
			Script: `
def execute(ctx):
    position_keeping.nonexistent_handler(position_id="acc1")
execute(input_data)
`,
		}},
	}

	result := v.Validate(manifest, nil, WithSkipImmutabilityChecks())
	assert.False(t, result.Valid, "should detect unknown handler")

	found := false
	for _, e := range result.Errors {
		if e.Code == schema.ValidationCodeUnknownHandler {
			found = true
			break
		}
	}
	assert.True(t, found, "should have an UNKNOWN_HANDLER error, got: %v", result.Errors)
}
