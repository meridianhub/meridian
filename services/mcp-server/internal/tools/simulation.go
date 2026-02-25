// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

// CELEvaluator evaluates CEL expressions against named environments with
// optional variable bindings. Implementations must not produce side effects.
type CELEvaluator interface {
	// Evaluate compiles and evaluates expression in the named environment.
	// variables is a map of variable name → value (string, bool, or numeric).
	// Returns the evaluation result or an error (compilation or evaluation failure).
	Evaluate(expression, environment string, variables map[string]interface{}) (interface{}, error)
}

// ManifestDiffer computes a structured diff between two manifest JSON documents.
// Implementations must not modify either manifest or produce side effects.
type ManifestDiffer interface {
	// Diff compares current and proposed manifest JSON and returns a structured
	// change summary with added, removed, and changed entries by section.
	Diff(current, proposed json.RawMessage) (interface{}, error)
}

// ValuationSimulator executes a valuation dry-run without persisting the result.
// Implementations must not persist any data or trigger downstream workflows.
type ValuationSimulator interface {
	// Simulate runs the valuation engine for req and returns the result.
	// The operation is read-only: no data is written.
	Simulate(ctx context.Context, req *valuation.Request) (*valuation.Response, error)
}

// SagaSimulator executes a Starlark saga script in a sandboxed dry-run mode.
// All service calls are stubbed — no real operations are performed.
type SagaSimulator interface {
	// Simulate runs script with inputData and returns an execution trace.
	// The returned value should describe the step-by-step execution outcome.
	Simulate(ctx context.Context, script string, inputData map[string]interface{}) (interface{}, error)
}

// SimulationDeps groups all dependencies for simulation tools.
// Any nil field causes that tool to be silently skipped during registration.
type SimulationDeps struct {
	CELEvaluator       CELEvaluator
	ManifestDiffer     ManifestDiffer
	ValuationSimulator ValuationSimulator
	SagaSimulator      SagaSimulator
}

// RegisterSimulationTools registers simulation tools into r using deps.
// Tools whose corresponding dependency is nil are silently skipped.
// Panics if registration fails for a non-nil dependency (schema error).
func RegisterSimulationTools(r *Registry, deps SimulationDeps) {
	candidates := []Tool{}
	if deps.CELEvaluator != nil {
		candidates = append(candidates, buildCELEvaluateTool(deps.CELEvaluator))
	}
	if deps.ManifestDiffer != nil {
		candidates = append(candidates, buildManifestDiffTool(deps.ManifestDiffer))
	}
	if deps.ValuationSimulator != nil {
		candidates = append(candidates, buildValuationSimulateTool(deps.ValuationSimulator))
	}
	if deps.SagaSimulator != nil {
		candidates = append(candidates, buildSagaSimulateTool(deps.SagaSimulator))
	}
	for _, t := range candidates {
		if err := r.Register(t); err != nil {
			panic(fmt.Sprintf("failed to register simulation tool %q: %v", t.Name, err))
		}
	}
}

// buildCELEvaluateTool returns the meridian_cel_evaluate tool.
func buildCELEvaluateTool(evaluator CELEvaluator) Tool {
	return Tool{
		Name:     "meridian_cel_evaluate",
		Category: CategorySimulate,
		Description: "Evaluate a CEL expression against a named environment with optional variable bindings. " +
			"Returns the result value without persisting any state. " +
			"Use this to test validation rules, bucketing expressions, or eligibility conditions before deploying them.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "The CEL expression to evaluate (e.g., \"amount > 0 && amount <= 1000000\").",
					"minLength":   1,
				},
				"environment": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"validation", "bucket_key", "eligibility"},
					"description": "The CEL environment that defines available variables: validation (amount, attributes, valid_from, valid_to, source), bucket_key (attributes), eligibility (party, attributes).",
				},
				"variables": map[string]interface{}{
					"type":        "object",
					"description": "Variable bindings to inject into the expression (key-value pairs, values as strings or numbers).",
				},
			},
			"required": []interface{}{"expression", "environment"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleCELEvaluate(ctx, evaluator, params)
		},
	}
}

// handleCELEvaluate implements the meridian_cel_evaluate handler.
func handleCELEvaluate(_ context.Context, evaluator CELEvaluator, params json.RawMessage) (interface{}, error) {
	var p struct {
		Expression  string                 `json:"expression"`
		Environment string                 `json:"environment"`
		Variables   map[string]interface{} `json:"variables"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if p.Variables == nil {
		p.Variables = make(map[string]interface{})
	}

	result, err := evaluator.Evaluate(p.Expression, p.Environment, p.Variables)
	if err != nil {
		formatted := mcperrors.FormatGRPCError(err)
		return map[string]interface{}{
			"error":       err.Error(),
			"errors":      formatted.Errors,
			"expression":  p.Expression,
			"environment": p.Environment,
		}, nil
	}

	return map[string]interface{}{
		"result":      result,
		"expression":  p.Expression,
		"environment": p.Environment,
	}, nil
}

// buildManifestDiffTool returns the meridian_manifest_diff tool.
func buildManifestDiffTool(differ ManifestDiffer) Tool {
	return Tool{
		Name:     "meridian_manifest_diff",
		Category: CategorySimulate,
		Description: "Compare two tenant manifests and return a structured change summary. " +
			"Shows added, removed, and changed sections (instruments, account_types, valuation_rules, sagas, payment_rails). " +
			"Use this to preview the impact of manifest changes before applying them.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"current": map[string]interface{}{
					"type":        "object",
					"description": "The currently applied manifest (JSON object).",
				},
				"proposed": map[string]interface{}{
					"type":        "object",
					"description": "The proposed new manifest to compare against the current one (JSON object).",
				},
			},
			"required": []interface{}{"current", "proposed"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestDiff(ctx, differ, params)
		},
	}
}

// manifestDiffParams holds the raw JSON for current and proposed manifests.
type manifestDiffParams struct {
	Current  json.RawMessage `json:"current"`
	Proposed json.RawMessage `json:"proposed"`
}

// handleManifestDiff implements the meridian_manifest_diff handler.
func handleManifestDiff(_ context.Context, differ ManifestDiffer, params json.RawMessage) (interface{}, error) {
	var p manifestDiffParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	result, err := differ.Diff(p.Current, p.Proposed)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // differ error is surfaced in the tool response, not returned as a Go error
			"error": err.Error(),
		}, nil
	}

	return result, nil
}

// buildValuationSimulateTool returns the meridian_valuation_simulate tool.
func buildValuationSimulateTool(simulator ValuationSimulator) Tool {
	return Tool{
		Name:     "meridian_valuation_simulate",
		Category: CategorySimulate,
		Description: "Simulate a valuation dry-run to convert an input quantity to a valued amount. " +
			"Returns the calculated output value, instrument, and the full calculation path audit trail. " +
			"No data is persisted. Use this to test valuation methods before activating them in production.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the ValuationMethod to simulate.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
				"input_instrument": map[string]interface{}{
					"type":        "string",
					"description": "Instrument code for the input quantity (e.g., \"KWH\", \"TONNE_CO2E\").",
					"minLength":   1,
				},
				"input_amount": map[string]interface{}{
					"type":        "string",
					"description": "Decimal amount of the input quantity (e.g., \"100.00\").",
					"minLength":   1,
				},
				"account_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the account context for valuation.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
				"party_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the party context for valuation.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
				"parameters": map[string]interface{}{
					"type":        "object",
					"description": "Method-specific parameters (e.g., {\"tier\": \"Gold\", \"gsp\": \"P\"}).",
				},
			},
			"required": []interface{}{"method_id", "input_instrument", "input_amount", "account_id", "party_id"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleValuationSimulate(ctx, simulator, params)
		},
	}
}

// valuationSimulateParams holds parsed parameters for meridian_valuation_simulate.
type valuationSimulateParams struct {
	MethodID        string                 `json:"method_id"`
	InputInstrument string                 `json:"input_instrument"`
	InputAmount     string                 `json:"input_amount"`
	AccountID       string                 `json:"account_id"`
	PartyID         string                 `json:"party_id"`
	Parameters      map[string]interface{} `json:"parameters"`
}

// handleValuationSimulate implements the meridian_valuation_simulate handler.
func handleValuationSimulate(ctx context.Context, simulator ValuationSimulator, params json.RawMessage) (interface{}, error) {
	var p valuationSimulateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	methodID, err := uuid.Parse(p.MethodID)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid method_id: %v", err),
		}, nil
	}

	accountID, err := uuid.Parse(p.AccountID)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid account_id: %v", err),
		}, nil
	}

	partyID, err := uuid.Parse(p.PartyID)
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid party_id: %v", err),
		}, nil
	}

	req := &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    methodID,
		AccountID:   accountID,
		PartyID:     partyID,
		KnowledgeAt: time.Now(),
		Quantity: valuation.Quantity{
			InstrumentCode: p.InputInstrument,
		},
		Parameters: p.Parameters,
	}

	resp, err := simulator.Simulate(ctx, req)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // simulator error is surfaced in the tool response, not returned as a Go error
			"error": err.Error(),
		}, nil
	}

	return formatValuationResponse(resp), nil
}

// formatValuationResponse formats a valuation.Response for LLM consumption.
func formatValuationResponse(resp *valuation.Response) map[string]interface{} {
	result := map[string]interface{}{
		"valued_amount": map[string]interface{}{
			"amount":          resp.ValuedAmount.Amount.String(),
			"instrument_code": resp.ValuedAmount.InstrumentCode,
		},
		"cache_hit":   resp.CacheHit,
		"computed_at": resp.ComputedAt.Format(time.RFC3339),
	}

	if resp.Analysis != nil {
		path := make([]map[string]interface{}, 0, len(resp.Analysis.CalculationPath))
		for _, entry := range resp.Analysis.CalculationPath {
			pathEntry := map[string]interface{}{
				"description": entry.Description,
				"timestamp":   entry.Timestamp.Format(time.RFC3339),
			}
			if len(entry.Data) > 0 {
				pathEntry["data"] = entry.Data
			}
			path = append(path, pathEntry)
		}
		result["calculation_path"] = path

		if len(resp.Analysis.PoliciesExecuted) > 0 {
			policies := make([]map[string]interface{}, 0, len(resp.Analysis.PoliciesExecuted))
			for _, pe := range resp.Analysis.PoliciesExecuted {
				policies = append(policies, map[string]interface{}{
					"policy_name":    pe.PolicyName,
					"policy_version": pe.PolicyVersion,
					"cost_units":     pe.CostUnits,
				})
			}
			result["policies_executed"] = policies
		}

		if len(resp.Analysis.Warnings) > 0 {
			result["warnings"] = resp.Analysis.Warnings
		}
	}

	return result
}

// buildSagaSimulateTool returns the meridian_saga_simulate tool.
func buildSagaSimulateTool(simulator SagaSimulator) Tool {
	return Tool{
		Name:     "meridian_saga_simulate",
		Category: CategorySimulate,
		Description: "Dry-run a Starlark saga script to trace its execution steps without performing real operations. " +
			"All service calls are stubbed — no accounts are modified, no ledger entries are created. " +
			"Returns a step-by-step execution trace with intermediate values. " +
			"Use this to validate saga logic before deploying to production.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"script": map[string]interface{}{
					"type":        "string",
					"description": "The Starlark saga script to simulate.",
					"minLength":   1,
				},
				"input_data": map[string]interface{}{
					"type":        "object",
					"description": "Input data passed to the saga as the 'input_data' variable (optional).",
				},
			},
			"required": []interface{}{"script"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleSagaSimulate(ctx, simulator, params)
		},
	}
}

// handleSagaSimulate implements the meridian_saga_simulate handler.
func handleSagaSimulate(ctx context.Context, simulator SagaSimulator, params json.RawMessage) (interface{}, error) {
	var p struct {
		Script    string                 `json:"script"`
		InputData map[string]interface{} `json:"input_data"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if p.InputData == nil {
		p.InputData = make(map[string]interface{})
	}

	result, err := simulator.Simulate(ctx, p.Script, p.InputData)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // simulator error is surfaced in the tool response, not returned as a Go error
			"error": err.Error(),
		}, nil
	}

	return result, nil
}
