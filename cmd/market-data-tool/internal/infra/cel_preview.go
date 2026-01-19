package infra

import (
	"sync/atomic"

	"github.com/google/cel-go/cel"
)

// CELPreview provides non-authoritative CEL validation preview.
// The actual validation is performed by the Market Information Service.
// This preview is provided for user feedback only.
type CELPreview struct {
	expression   string
	program      cel.Program
	warningCount int64
	enabled      bool
}

// NewCELPreview creates a new CEL preview evaluator.
// If the expression is empty or compilation fails, preview is disabled.
func NewCELPreview(expression string) *CELPreview {
	preview := &CELPreview{
		expression: expression,
	}

	if expression == "" {
		return preview
	}

	// Compile the CEL expression
	env, err := cel.NewEnv(
		cel.Variable("value", cel.StringType),
		cel.Variable("dataset_code", cel.StringType),
		cel.Variable("quality_level", cel.StringType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return preview
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return preview
	}

	program, err := env.Program(ast)
	if err != nil {
		return preview
	}

	preview.program = program
	preview.enabled = true
	return preview
}

// PreviewResult contains the result of a CEL preview evaluation.
type PreviewResult struct {
	// Valid indicates if the expression evaluated to true.
	Valid bool

	// ErrorMessage contains a custom error message if the validation failed.
	// This is from the error_message_expression, not the validation expression.
	ErrorMessage string

	// Warning contains any warning about the preview (e.g., "preview may differ from service").
	Warning string
}

// Evaluate runs the CEL expression preview.
// Returns a warning if the preview is disabled or evaluation fails.
// NOTE: This is NOT authoritative - the service validation is the source of truth.
func (p *CELPreview) Evaluate(value, datasetCode, qualityLevel string, attributes map[string]string) PreviewResult {
	if !p.enabled || p.program == nil {
		return PreviewResult{
			Valid:   true, // Assume valid if preview is disabled
			Warning: "CEL preview disabled - service validation is authoritative",
		}
	}

	input := map[string]interface{}{
		"value":         value,
		"dataset_code":  datasetCode,
		"quality_level": qualityLevel,
		"attributes":    attributes,
	}

	out, _, err := p.program.Eval(input)
	if err != nil {
		atomic.AddInt64(&p.warningCount, 1)
		return PreviewResult{
			Valid:   true, // Assume valid on error - let service decide
			Warning: "CEL preview evaluation failed - service validation is authoritative",
		}
	}

	// Check if the result is a boolean
	if boolVal, ok := out.Value().(bool); ok {
		if !boolVal {
			atomic.AddInt64(&p.warningCount, 1)
		}
		return PreviewResult{
			Valid:   boolVal,
			Warning: "CEL preview result - service validation is authoritative",
		}
	}

	// Non-boolean result, assume valid
	return PreviewResult{
		Valid:   true,
		Warning: "CEL preview returned non-boolean - service validation is authoritative",
	}
}

// WarningCount returns the number of warnings generated during preview.
func (p *CELPreview) WarningCount() int {
	return int(atomic.LoadInt64(&p.warningCount))
}

// Expression returns the CEL expression being previewed.
func (p *CELPreview) Expression() string {
	return p.expression
}

// IsEnabled returns true if the preview is enabled.
func (p *CELPreview) IsEnabled() bool {
	return p.enabled
}
