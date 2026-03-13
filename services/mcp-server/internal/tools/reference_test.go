// Package tools provides the tool registry for the MCP server.
package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// callReferenceTool registers all reference tools and calls the named one.
func callReferenceTool(t *testing.T, toolName string, params interface{}) map[string]interface{} {
	t.Helper()
	reg := newTestServer(t)
	tools.RegisterReferenceTools(reg.Server())

	raw, err := json.Marshal(params)
	require.NoError(t, err)

	result, err := reg.Call(context.Background(), toolName, raw)
	require.NoError(t, err)

	out, err := json.Marshal(result)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &m))
	return m
}

// --- meridian_topics_list tests ---

func TestTopicsList_NoFilter_ReturnsAllTopics(t *testing.T) {
	result := callReferenceTool(t, "meridian_topics_list", map[string]interface{}{})

	allTopics := topics.All()
	count := result["count"].(float64)
	assert.Equal(t, float64(len(allTopics)), count)

	topicList := result["topics"].([]interface{})
	assert.Len(t, topicList, len(allTopics))
	require.NotEmpty(t, topicList, "topics.All() must return at least one topic")

	// Verify each entry has required fields.
	first := topicList[0].(map[string]interface{})
	assert.Contains(t, first, "topic")
	assert.Contains(t, first, "service")
	assert.Contains(t, first, "trigger")
}

func TestTopicsList_ServiceFilter_ReturnsFilteredTopics(t *testing.T) {
	result := callReferenceTool(t, "meridian_topics_list", map[string]interface{}{
		"service_filter": "position-keeping",
	})

	topicList := result["topics"].([]interface{})
	count := result["count"].(float64)
	assert.Equal(t, float64(len(topicList)), count)
	assert.Greater(t, len(topicList), 0)

	for _, entry := range topicList {
		e := entry.(map[string]interface{})
		assert.Equal(t, "position-keeping", e["service"])
		assert.True(t, len(e["trigger"].(string)) > 0)
	}
}

func TestTopicsList_UnknownService_ReturnsEmpty(t *testing.T) {
	result := callReferenceTool(t, "meridian_topics_list", map[string]interface{}{
		"service_filter": "nonexistent-service",
	})

	count := result["count"].(float64)
	assert.Equal(t, float64(0), count)

	topicList := result["topics"].([]interface{})
	assert.Empty(t, topicList)
}

func TestTopicsList_TriggerFormat(t *testing.T) {
	result := callReferenceTool(t, "meridian_topics_list", map[string]interface{}{})

	topicList := result["topics"].([]interface{})
	for _, entry := range topicList {
		e := entry.(map[string]interface{})
		trigger := e["trigger"].(string)
		topic := e["topic"].(string)
		assert.Equal(t, "event:"+topic, trigger)
	}
}

// --- meridian_starlark_reference tests ---

func TestStarlarkReference_ReturnsBindingsAndBuiltins(t *testing.T) {
	result := callReferenceTool(t, "meridian_starlark_reference", map[string]interface{}{})

	bindings := result["service_bindings"].([]interface{})
	assert.Greater(t, len(bindings), 0)
	// Check known bindings are present.
	bindingStrs := make([]string, len(bindings))
	for i, b := range bindings {
		bindingStrs[i] = b.(string)
	}
	assert.Contains(t, bindingStrs, "position_keeping")
	assert.Contains(t, bindingStrs, "financial_accounting")
	assert.Contains(t, bindingStrs, "current_account")

	builtins := result["top_level_builtins"].([]interface{})
	assert.Greater(t, len(builtins), 0)
	builtinStrs := make([]string, len(builtins))
	for i, b := range builtins {
		builtinStrs[i] = b.(string)
	}
	assert.Contains(t, builtinStrs, "Decimal")
	assert.Contains(t, builtinStrs, "input_data")
	assert.Contains(t, builtinStrs, "invoke_handler")

	notes := result["notes"].([]interface{})
	assert.Greater(t, len(notes), 0)
}

func TestStarlarkReference_BindingsAreSorted(t *testing.T) {
	result := callReferenceTool(t, "meridian_starlark_reference", map[string]interface{}{})

	bindings := result["service_bindings"].([]interface{})
	for i := 1; i < len(bindings); i++ {
		prev := bindings[i-1].(string)
		curr := bindings[i].(string)
		assert.LessOrEqual(t, prev, curr, "service_bindings should be sorted")
	}

	builtins := result["top_level_builtins"].([]interface{})
	for i := 1; i < len(builtins); i++ {
		prev := builtins[i-1].(string)
		curr := builtins[i].(string)
		assert.LessOrEqual(t, prev, curr, "top_level_builtins should be sorted")
	}
}

// --- meridian_manifest_schema tests ---

func TestManifestSchema_ReturnsExpectedFields(t *testing.T) {
	result := callReferenceTool(t, "meridian_manifest_schema", map[string]interface{}{})

	assert.Contains(t, result, "instrument_types")
	assert.Contains(t, result, "normal_balance_values")
	assert.Contains(t, result, "valuation_methods")
	assert.Contains(t, result, "trigger_patterns")
	assert.Contains(t, result, "field_constraints")
	assert.Contains(t, result, "payment_rail_modes")

	instrumentTypes := result["instrument_types"].([]interface{})
	assert.Contains(t, instrumentTypes, "CURRENCY")
	assert.Contains(t, instrumentTypes, "ENERGY")

	normalBalances := result["normal_balance_values"].([]interface{})
	assert.Contains(t, normalBalances, "DEBIT")
	assert.Contains(t, normalBalances, "CREDIT")

	triggerPatterns := result["trigger_patterns"].(map[string]interface{})
	assert.Contains(t, triggerPatterns, "api")
	assert.Contains(t, triggerPatterns, "webhook")
	assert.Contains(t, triggerPatterns, "scheduled")
	assert.Contains(t, triggerPatterns, "event")
}

func TestManifestSchema_FieldConstraints(t *testing.T) {
	result := callReferenceTool(t, "meridian_manifest_schema", map[string]interface{}{})

	constraints := result["field_constraints"].(map[string]interface{})
	assert.Contains(t, constraints, "instrument_code")
	assert.Contains(t, constraints, "account_type_code")
	assert.Contains(t, constraints, "saga_name")
	assert.Contains(t, constraints, "precision")
}

// --- meridian_gateway_guide tests ---

func TestGatewayGuide_ReturnsGatewayInfo(t *testing.T) {
	result := callReferenceTool(t, "meridian_gateway_guide", map[string]interface{}{})

	assert.Contains(t, result, "financial_gateway")
	assert.Contains(t, result, "operational_gateway")
	assert.Contains(t, result, "decision_tree")

	fg := result["financial_gateway"].(map[string]interface{})
	assert.Contains(t, fg, "description")
	assert.Contains(t, fg, "use_when")
	assert.Contains(t, fg, "events")

	fgEvents := fg["events"].([]interface{})
	assert.Greater(t, len(fgEvents), 0)
	assert.Contains(t, fgEvents, "financial-gateway.payment-captured.v1")

	og := result["operational_gateway"].(map[string]interface{})
	assert.Contains(t, og, "description")
	assert.Contains(t, og, "use_when")
	assert.Contains(t, og, "events")

	ogEvents := og["events"].([]interface{})
	assert.Greater(t, len(ogEvents), 0)
	assert.Contains(t, ogEvents, "operational-gateway.instruction-created.v1")
}

func TestGatewayGuide_DecisionTree(t *testing.T) {
	result := callReferenceTool(t, "meridian_gateway_guide", map[string]interface{}{})

	tree := result["decision_tree"].([]interface{})
	assert.Greater(t, len(tree), 0)

	for _, node := range tree {
		n := node.(map[string]interface{})
		assert.Contains(t, n, "question")
		assert.Contains(t, n, "yes")
		assert.Contains(t, n, "no")
	}
}

// --- Registration tests ---

func TestRegisterReferenceTools_AllToolsRegistered(t *testing.T) {
	reg := newTestServer(t)
	tools.RegisterReferenceTools(reg.Server())

	toolList := reg.List(context.Background())
	names := make([]string, len(toolList))
	for i, tl := range toolList {
		names[i] = tl.Name
	}

	assert.Contains(t, names, "meridian_topics_list")
	assert.Contains(t, names, "meridian_starlark_reference")
	assert.Contains(t, names, "meridian_manifest_schema")
	assert.Contains(t, names, "meridian_gateway_guide")
}
