// Package tools provides tool handlers for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	starlarkSyntax "go.starlark.net/syntax"
)

// ErrUnknownCELEnvironment is returned when an unsupported CEL environment name is provided.
var ErrUnknownCELEnvironment = errors.New("unknown CEL environment")

// RegisterValidationTools registers the meridian_cel_validate and
// meridian_starlark_validate tools onto the SDK server.
func RegisterValidationTools(srv *mcp.Server) {
	addTool(srv, celValidateTool())
	addTool(srv, starlarkValidateTool())
}

// celValidateTool builds the meridian_cel_validate Tool definition.
func celValidateTool() Tool {
	return Tool{
		Name:        "meridian_cel_validate",
		Description: "Compile and validate a CEL expression. Returns result, return type, and cost estimate.",
		Category:    CategorySimulate,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "The CEL expression to validate",
				},
				"environment": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"validation", "bucket_key", "eligibility", "event_filter"},
					"description": "Which CEL environment to compile against (determines available variables)",
				},
			},
			"required": []interface{}{"expression", "environment"},
		},
		Handler: handleCELValidate,
	}
}

// starlarkValidateTool builds the meridian_starlark_validate Tool definition.
func starlarkValidateTool() Tool {
	return Tool{
		Name:        "meridian_starlark_validate",
		Description: "Validate a Starlark saga script for syntax errors.",
		Category:    CategorySimulate,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"script": map[string]interface{}{
					"type":        "string",
					"description": "The Starlark script source code",
				},
			},
			"required": []interface{}{"script"},
		},
		Handler: handleStarlarkValidate,
	}
}

// handleCELValidate is the handler for the meridian_cel_validate tool.
func handleCELValidate(_ context.Context, params json.RawMessage) (interface{}, error) {
	var input struct {
		Expression  string `json:"expression"`
		Environment string `json:"environment"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	env, envErr := createCELEnvironment(input.Environment)
	if envErr != nil {
		formatted := mcperrors.FormatGRPCError(envErr)
		return map[string]interface{}{
			"valid":  false,
			"errors": formatErrorDetails(formatted.Errors),
		}, nil
	}

	ast, issues := env.Compile(input.Expression)
	if issues != nil && issues.Err() != nil {
		// Wrap as a CEL compilation error so the formatter can parse line/column.
		// fmt.Errorf with %w produces a non-dynamic wrapped error.
		celErr := fmt.Errorf("cel compilation failed: %w", issues.Err())
		formatted := mcperrors.FormatGRPCError(celErr)
		return map[string]interface{}{
			"valid":  false,
			"errors": formatErrorDetails(formatted.Errors),
		}, nil
	}

	costEstimate := estimateCELCost(env, ast)

	return map[string]interface{}{
		"valid":         true,
		"return_type":   ast.OutputType().String(),
		"cost_estimate": costEstimate,
	}, nil
}

// createCELEnvironment creates a CEL environment for the given context name.
// Supported environments: "validation", "bucket_key", "eligibility".
func createCELEnvironment(envName string) (*cel.Env, error) {
	switch envName {
	case "validation":
		return cel.NewEnv(
			cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("amount", cel.StringType),
			cel.Variable("valid_from", cel.TimestampType),
			cel.Variable("valid_to", cel.TimestampType),
			cel.Variable("source", cel.StringType),
			sharedcel.SafeParseLib(),
		)
	case "bucket_key":
		return cel.NewEnv(
			cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
			sharedcel.SafeParseLib(),
			sharedcel.BucketKeyLib(),
		)
	case "eligibility":
		return cel.NewEnv(
			cel.Variable("party", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
		)
	case "event_filter":
		return cel.NewEnv(
			cel.Variable("event", cel.DynType),
			cel.Variable("metadata", cel.MapType(cel.StringType, cel.StringType)),
		)
	default:
		return nil, fmt.Errorf("%w: %q (must be one of validation, bucket_key, eligibility, event_filter)", ErrUnknownCELEnvironment, envName)
	}
}

// estimateCELCost computes a static cost estimate for the compiled AST.
// Returns a map with "min" and "max" keys. Falls back to zero values on error.
func estimateCELCost(env *cel.Env, ast *cel.Ast) map[string]interface{} {
	est, err := env.EstimateCost(ast, &zeroCostEstimator{})
	if err != nil {
		// Cost estimation is best-effort; return a zero estimate on failure.
		return map[string]interface{}{"min": uint64(0), "max": uint64(0)}
	}
	return map[string]interface{}{
		"min": est.Min,
		"max": est.Max,
	}
}

// zeroCostEstimator implements checker.CostEstimator with zero-size estimates.
// It provides no additional context about variable sizes, so the estimator
// uses CEL's default cost model.
type zeroCostEstimator struct{}

func (*zeroCostEstimator) EstimateSize(_ checker.AstNode) *checker.SizeEstimate { return nil }

func (*zeroCostEstimator) EstimateCallCost(_, _ string, _ *checker.AstNode, _ []checker.AstNode) *checker.CallEstimate {
	return nil
}

// handleStarlarkValidate is the handler for the meridian_starlark_validate tool.
func handleStarlarkValidate(_ context.Context, params json.RawMessage) (interface{}, error) {
	var input struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	opts := starlarkSyntax.FileOptions{}
	file, parseErr := opts.Parse("saga.star", input.Script, starlarkSyntax.RetainComments)
	if parseErr != nil {
		errDetails := formatStarlarkError(parseErr)
		return map[string]interface{}{
			"valid":  false,
			"errors": errDetails,
		}, nil
	}

	if issues := checkStarlarkTermination(file); len(issues) > 0 {
		return map[string]interface{}{
			"valid":  false,
			"errors": issues,
		}, nil
	}

	return map[string]interface{}{
		"valid":   true,
		"message": "Script syntax valid",
	}, nil
}

// formatStarlarkError converts a Starlark parse error into an error detail slice.
// It extracts line/column from the starlarkSyntax.Error struct when available, and falls
// back to parsing the canonical "filename:line:col: msg" error string via the
// existing mcperrors formatter. FormatGRPCError is called with the original error
// so no dynamic intermediate error is created.
func formatStarlarkError(err error) []interface{} {
	var synErr starlarkSyntax.Error
	if errors.As(err, &synErr) {
		// Direct extraction from the structured error type.
		detail := mcperrors.ErrorDetail{
			Type:    mcperrors.TypeStarlarkSyntax,
			Line:    int(synErr.Pos.Line),
			Column:  int(synErr.Pos.Col),
			Message: synErr.Msg,
		}
		return formatErrorDetails([]mcperrors.ErrorDetail{detail})
	}

	// Fallback: the error string matches "<filename>.star:<line>:<col>: <msg>".
	// Prefix with "starlark" so FormatGRPCError's isStarlarkError detector fires.
	prefixedErr := fmt.Errorf("starlark error: %w", err)
	formatted := mcperrors.FormatGRPCError(prefixedErr)
	return formatErrorDetails(formatted.Errors)
}

// checkStarlarkTermination walks the parsed AST and returns error details for
// any forbidden constructs. Currently detects while loops; the Starlark parser
// permits while syntactically but Meridian forbids them for termination guarantees.
func checkStarlarkTermination(file *starlarkSyntax.File) []interface{} {
	var issues []interface{}
	starlarkSyntax.Walk(file, func(n starlarkSyntax.Node) bool {
		if while, ok := n.(*starlarkSyntax.WhileStmt); ok {
			start, _ := while.Span()
			issues = append(issues, map[string]interface{}{
				"type":    mcperrors.TypeStarlarkSyntax,
				"line":    int(start.Line),
				"column":  int(start.Col),
				"message": "while loops are not permitted in Starlark saga scripts (termination guarantee violation)",
			})
		}
		return true
	})
	return issues
}

// formatErrorDetails converts a slice of mcperrors.ErrorDetail into a slice of
// map[string]interface{} suitable for JSON serialization in tool responses.
func formatErrorDetails(details []mcperrors.ErrorDetail) []interface{} {
	result := make([]interface{}, 0, len(details))
	for _, d := range details {
		m := map[string]interface{}{
			"type":    d.Type,
			"message": d.Message,
		}
		if d.Line > 0 {
			m["line"] = d.Line
		}
		if d.Column > 0 {
			m["column"] = d.Column
		}
		if d.Suggestion != "" {
			m["suggestion"] = d.Suggestion
		}
		if d.Path != "" {
			m["path"] = d.Path
		}
		result = append(result, m)
	}
	return result
}
