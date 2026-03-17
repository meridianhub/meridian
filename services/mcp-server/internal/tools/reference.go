// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// RegisterReferenceTools registers all static reference tools that return
// platform metadata. These tools are tenant-agnostic and require no gRPC clients.
func RegisterReferenceTools(srv *mcp.Server) {
	allTools := []Tool{
		buildTopicsListTool(),
		buildStarlarkReferenceTool(),
		buildManifestSchemaTool(),
		buildGatewayGuideTool(),
	}

	for _, t := range allTools {
		addTool(srv, t)
	}
}

// topicEntry describes a single event topic.
type topicEntry struct {
	Topic   string `json:"topic"`
	Service string `json:"service"`
	Trigger string `json:"trigger"`
}

// serviceFromTopic extracts the service name from a topic following the
// convention <service>.<event-name>.<version>.
func serviceFromTopic(topic string) string {
	parts := strings.SplitN(topic, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// buildTopicsListTool creates the meridian_topics_list tool.
func buildTopicsListTool() Tool {
	return Tool{
		Name:        "meridian_topics_list",
		Description: "Returns all registered event topics in the platform. Supports optional service_filter to show only topics for a specific service.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"service_filter": map[string]interface{}{
					"type":        "string",
					"description": "Filter topics by service prefix (e.g., 'position-keeping', 'financial-accounting'). Omit to return all.",
				},
			},
		},
		Handler: func(_ context.Context, params json.RawMessage) (interface{}, error) {
			var p struct {
				ServiceFilter string `json:"service_filter"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return formatError("invalid parameters: " + err.Error()), nil //nolint:nilerr // tool errors are returned in the result
			}

			all := topics.All()
			entries := make([]topicEntry, 0, len(all))
			for _, t := range all {
				svc := serviceFromTopic(t)
				if p.ServiceFilter != "" && svc != p.ServiceFilter {
					continue
				}
				entries = append(entries, topicEntry{
					Topic:   t,
					Service: svc,
					Trigger: "event:" + t,
				})
			}

			return map[string]interface{}{
				"topics": entries,
				"count":  len(entries),
			}, nil
		},
	}
}

// knownServiceBindingsRef mirrors the known service bindings from the manifest validator.
// These are the service modules available on the saga context object (ctx.<service>).
var knownServiceBindingsRef = []string{
	"position_keeping",
	"financial_accounting",
	"current_account",
	"valuation_engine",
	"repository",
	"notification",
	"payment_order",
	"reconciliation",
	"reference_data",
}

// knownStarlarkBuiltinsRef mirrors the known Starlark builtins from the manifest validator.
var knownStarlarkBuiltinsRef = []string{
	"input_data",

	"party_scope",
	"Decimal",
	"print",
	"len",
	"range",
	"str",
	"int",
	"float",
	"bool",
	"list",
	"dict",
	"tuple",
	"type",
	"True",
	"False",
	"None",
	"hasattr",
	"getattr",
	"enumerate",
	"zip",
	"sorted",
	"reversed",
	"min",
	"max",
	"any",
	"all",
	"hash",
	"repr",
	"fail",
}

// buildStarlarkReferenceTool creates the meridian_starlark_reference tool.
func buildStarlarkReferenceTool() Tool {
	return Tool{
		Name:        "meridian_starlark_reference",
		Description: "Returns the available Starlark service module bindings (ctx.* methods) and built-in functions for writing saga scripts.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]interface{}{},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			bindings := make([]string, len(knownServiceBindingsRef))
			copy(bindings, knownServiceBindingsRef)
			sort.Strings(bindings)

			builtins := make([]string, len(knownStarlarkBuiltinsRef))
			copy(builtins, knownStarlarkBuiltinsRef)
			sort.Strings(builtins)

			return map[string]interface{}{
				"service_bindings":   bindings,
				"top_level_builtins": builtins,
				"notes": []string{
					"Service bindings are accessed via ctx.<service_name> in saga scripts.",
					"Starlark does not support while loops or recursion — all programs are guaranteed to terminate.",
					"Use Decimal() for financial arithmetic to avoid floating-point errors.",
					"Use input_data to access the saga trigger payload.",
					"Use typed service modules for handler invocation (e.g., payment_order.create_lien(...)).",
					"Use party_scope(party_id) to scope operations to a specific party.",
				},
			}, nil
		},
	}
}

// buildManifestSchemaTool creates the meridian_manifest_schema tool.
func buildManifestSchemaTool() Tool {
	return Tool{
		Name:        "meridian_manifest_schema",
		Description: "Returns manifest schema metadata including instrument types, normal balance values, valuation methods, trigger patterns, and field constraints.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]interface{}{},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return map[string]interface{}{
				"instrument_types": []string{
					"CURRENCY",
					"COMMODITY",
					"ENERGY",
					"COMPUTE",
					"CARBON_CREDIT",
					"VOUCHER",
					"CUSTOM",
				},
				"normal_balance_values": []string{
					"DEBIT",
					"CREDIT",
				},
				"valuation_methods": []string{
					"MARKET_DATA",
					"FIXED_RATE",
					"FORMULA",
				},
				"trigger_patterns": map[string]interface{}{
					"api":       "api:<path> — triggered by an API call to the specified path",
					"webhook":   "webhook:<path> — triggered by an incoming webhook",
					"scheduled": "scheduled:<cron-expression> — triggered on a cron schedule",
					"event":     "event:<topic-name> — triggered by a Kafka event topic",
				},
				"field_constraints": map[string]interface{}{
					"instrument_code":   "uppercase alphanumeric with underscores, max 32 chars (e.g., USD, KWH, CARBON_CREDIT)",
					"account_type_code": "uppercase alphanumeric with underscores, max 64 chars (e.g., CUSTOMER_CURRENT, REVENUE)",
					"saga_name":         "lowercase alphanumeric with underscores, max 128 chars (e.g., current_account_deposit)",
					"precision":         "integer 0-18, defines decimal places for instrument amounts",
				},
				"payment_rail_modes": []string{
					"LIVE",
					"SANDBOX",
					"SIMULATION",
				},
			}, nil
		},
	}
}

// buildGatewayGuideTool creates the meridian_gateway_guide tool.
func buildGatewayGuideTool() Tool {
	return Tool{
		Name:        "meridian_gateway_guide",
		Description: "Returns guidance on choosing between the financial gateway and the operational gateway, including when to use each, their events, and a decision tree.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]interface{}{},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return map[string]interface{}{
				"financial_gateway": map[string]interface{}{
					"description": "Handles payment processing through financial providers (e.g., Stripe). Manages payment intents, captures, refunds, and disputes.",
					"use_when": []string{
						"Processing card payments or bank transfers",
						"Handling payment refunds or disputes",
						"Integrating with Stripe, PayPal, or similar payment processors",
						"Managing payment lifecycle (authorize, capture, refund)",
					},
					"events": []string{
						topics.FinancialGatewayPaymentCapturedV1,
						topics.FinancialGatewayPaymentFailedV1,
						topics.FinancialGatewayPaymentRefundedV1,
						topics.FinancialGatewayPaymentDisputedV1,
					},
				},
				"operational_gateway": map[string]interface{}{
					"description": "Dispatches arbitrary instructions to external systems via provider connections. Handles retries, rate limiting, and delivery tracking.",
					"use_when": []string{
						"Sending notifications to external systems",
						"Dispatching KYC verification requests",
						"Triggering webhooks to third-party services",
						"Any outbound instruction that is not a payment",
					},
					"events": []string{
						topics.OperationalGatewayInstructionCreatedV1,
						topics.OperationalGatewayInstructionDispatchedV1,
						topics.OperationalGatewayInstructionDeliveredV1,
						topics.OperationalGatewayInstructionAcknowledgedV1,
						topics.OperationalGatewayInstructionFailedV1,
						topics.OperationalGatewayInstructionExpiredV1,
						topics.OperationalGatewayInstructionCancelledV1,
					},
				},
				"decision_tree": []map[string]string{
					{"question": "Is this a payment (card charge, bank transfer, refund)?", "yes": "Use Financial Gateway", "no": "Continue to next question"},
					{"question": "Does the external system need to receive a structured instruction?", "yes": "Use Operational Gateway", "no": "Continue to next question"},
					{"question": "Do you need retry logic, rate limiting, or delivery tracking?", "yes": "Use Operational Gateway", "no": "Consider direct HTTP call from saga script"},
				},
			}, nil
		},
	}
}
