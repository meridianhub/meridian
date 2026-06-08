package generator_test

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registryWithDeprecatedHandler builds a schema registry that contains a current
// handler plus a deprecated alias (with param mapping and a default) and a second
// handler flagged deprecated:true directly (no replacement). This exercises both
// arms of collectDeprecatedHandlersFromRegistry: the alias (IsDeprecated &&
// ReplacedBy != "") path that yields a mapping, and the directly-deprecated
// (ReplacedBy == "") path that is skipped.
func registryWithDeprecatedHandler(t *testing.T) *schema.Registry {
	t.Helper()
	yamlData := `
service: position_keeping
version: "2.0"
handlers:
  position_keeping.record_entry:
    description: "Record an entry (v2)"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
      instrument_code:
        type: string
        required: true
      side:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: position_keeping.initiate_log
        param_mapping:
          quantity: amount
          instrument_code: currency
          side: direction
        defaults:
          instrument_code: "'GBP'"
        sunset: "3.0"
  position_keeping.legacy_noop:
    description: "Deprecated with no replacement"
    deprecated: true
    compensation_strategy: none
    params:
      id:
        type: string
        required: true
    returns:
      status:
        type: string
`
	reg := schema.NewRegistry()
	require.NoError(t, reg.LoadFromYAML([]byte(yamlData)))
	return reg
}

// --- collectDeprecatedHandlersFromRegistry ---

func TestCollectDeprecatedHandlersFromRegistry_AliasOnly(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)
	deprecated := generator.CollectDeprecatedHandlersFromRegistry(reg)

	// Only the alias (which has a ReplacedBy) is collected. The directly
	// deprecated handler (legacy_noop) has ReplacedBy == "" and is skipped.
	require.Contains(t, deprecated, "position_keeping.initiate_log")
	assert.NotContains(t, deprecated, "position_keeping.legacy_noop")
	assert.Equal(
		t,
		"position_keeping.record_entry",
		generator.DeprecatedHandlerCurrentName(deprecated["position_keeping.initiate_log"]),
	)
}

func TestCollectDeprecatedHandlersFromRegistry_EmptyRegistry(t *testing.T) {
	deprecated := generator.CollectDeprecatedHandlersFromRegistry(schema.NewRegistry())
	assert.Empty(t, deprecated)
}

// --- applyMutatingPhase end-to-end via registry (rewriteScriptBlocks /
// flushScriptBlock / applyDeprecatedHandlerFixes / renameKwargs chain) ---

func TestApplyMutatingPhase_RewritesDeprecatedCallInScriptBlock(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      position_keeping.initiate_log(amount=100, currency='USD', direction='CREDIT')",
		"    enabled: true",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)

	// Handler name rewritten to the current name.
	assert.Contains(t, result, "position_keeping.record_entry(")
	assert.NotContains(t, result, "position_keeping.initiate_log(")
	// Params renamed via the reverse param mapping (old -> new).
	assert.Contains(t, result, "quantity=100")
	assert.Contains(t, result, "instrument_code='USD'")
	assert.Contains(t, result, "side='CREDIT'")
	// Lines outside the script block are preserved verbatim.
	assert.Contains(t, result, "  - name: capture")
	assert.Contains(t, result, "    enabled: true")
}

func TestApplyMutatingPhase_InjectsDefaultForMissingParam(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	// The old call omits the param that maps to instrument_code, so the
	// conversion default ('GBP') must be injected.
	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      position_keeping.initiate_log(amount=100, direction='DEBIT')",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)

	assert.Contains(t, result, "position_keeping.record_entry(")
	assert.Contains(t, result, "quantity=100")
	assert.Contains(t, result, "side='DEBIT'")
	assert.Contains(t, result, "instrument_code='GBP'")
}

func TestApplyMutatingPhase_NoDeprecatedCallLeavesScriptUnchanged(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      position_keeping.record_entry(quantity=1, instrument_code='GBP', side='CREDIT')",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)
	assert.Equal(t, manifest, result)
}

func TestApplyMutatingPhase_MultipleScriptBlocks(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	manifest := strings.Join([]string{
		"sagas:",
		"  - name: first",
		"    script: |",
		"      position_keeping.initiate_log(amount=1, currency='GBP', direction='CREDIT')",
		"  - name: second",
		"    script: |",
		"      position_keeping.initiate_log(amount=2, currency='USD', direction='DEBIT')",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)

	assert.Equal(t, 2, strings.Count(result, "position_keeping.record_entry("))
	assert.NotContains(t, result, "initiate_log(")
}

func TestApplyMutatingPhase_BlankLineInsideScriptBlock(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	// A blank line inside the block must not terminate it (exercises the
	// empty-line branch of rewriteScriptBlocks).
	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      position_keeping.initiate_log(amount=100, currency='GBP', direction='CREDIT')",
		"",
		"      print('done')",
		"  - name: next",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)

	assert.Contains(t, result, "position_keeping.record_entry(")
	assert.Contains(t, result, "print('done')")
	assert.Contains(t, result, "  - name: next")
}

// registryWithTwoDeprecatedHandlers registers two deprecated aliases of
// differing name lengths so that applyDeprecatedHandlerFixes must sort them
// (longest first) - exercising the sort comparator's swap branch.
func registryWithTwoDeprecatedHandlers(t *testing.T) *schema.Registry {
	t.Helper()
	yamlData := `
service: pk
version: "2.0"
handlers:
  pk.record_entry:
    description: "v2"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
    returns:
      entry_id:
        type: string
    conversions:
      - from_version: 1
        from_name: pk.initiate_log
        param_mapping:
          quantity: amount
  pk.close_position:
    description: "v2 close"
    version: 2
    compensation_strategy: none
    params:
      quantity:
        type: Decimal
        required: true
    returns:
      ok:
        type: string
    conversions:
      - from_version: 1
        from_name: pk.close
        param_mapping:
          quantity: amount
`
	reg := schema.NewRegistry()
	require.NoError(t, reg.LoadFromYAML([]byte(yamlData)))
	return reg
}

func TestApplyMutatingPhase_TwoDeprecatedHandlersSorted(t *testing.T) {
	reg := registryWithTwoDeprecatedHandlers(t)

	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      pk.initiate_log(amount=1)",
		"      pk.close(amount=2)",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)

	assert.Contains(t, result, "pk.record_entry(quantity=1)")
	assert.Contains(t, result, "pk.close_position(quantity=2)")
	assert.NotContains(t, result, "initiate_log(")
}

func TestApplyMutatingPhase_ScriptBlockRunsToEndOfFile(t *testing.T) {
	reg := registryWithDeprecatedHandler(t)

	// No trailing de-indented line: the block is flushed by the
	// end-of-input tail branch of rewriteScriptBlocks.
	manifest := strings.Join([]string{
		"sagas:",
		"  - name: capture",
		"    script: |",
		"      position_keeping.initiate_log(amount=5, currency='GBP', direction='CREDIT')",
	}, "\n")

	result := generator.ApplyMutatingPhase(manifest, reg)
	assert.Contains(t, result, "position_keeping.record_entry(")
}
