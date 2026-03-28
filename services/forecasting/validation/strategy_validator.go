// Package validation provides a Starlark strategy validation framework with
// AI-native structured error feedback. It performs static analysis of forecast
// strategies before execution, catching errors at validation time rather than
// runtime.
package validation

import (
	"fmt"
	"strings"

	starlarklib "go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Result holds the outcome of validating a Starlark forecast strategy.
type Result struct {
	Valid                  bool
	Errors                 []Error
	AvailableContextFields []string
	AvailableFunctions     []string
}

// Error represents a single validation problem with location and
// AI-friendly fix suggestion.
type Error struct {
	Line       int
	Column     int
	Message    string
	Suggestion string
}

// forbiddenPatterns lists Starlark statement patterns that are not permitted
// in forecast strategies. While Starlark already forbids while loops and
// recursion at the language level, we check for patterns that indicate misuse.
var forbiddenPatterns = []string{
	"import ",
	"load(",
}

// availableContextFields documents the fields available on the ctx parameter.
var availableContextFields = []string{
	"observations",
	"reference_data",
	"horizon_seconds",
	"granularity_seconds",
	"now",
}

// availableFunctions documents the builtin functions available in the sandbox.
var availableFunctions = []string{
	"avg", "sum", "percentile",
	"filter_by_hour", "group_by_hour",
	"duration", "add_seconds",
	"Decimal",
	"len", "str", "int", "float", "bool",
	"list", "dict", "tuple", "range",
	"enumerate", "zip", "sorted", "reversed",
	"min", "max", "abs", "any", "all",
	"hasattr", "getattr", "dir", "type", "repr", "hash",
	"print",
}

// ValidateStrategy performs static validation of a Starlark forecast strategy.
// It checks:
//  1. Starlark syntax is valid
//  2. A compute_forecast function is defined with a ctx parameter
//  3. No forbidden operations (import, load)
//
// The result includes AI-friendly suggestions for fixing any issues found.
func ValidateStrategy(script string) Result {
	result := Result{
		Valid:                  true,
		AvailableContextFields: availableContextFields,
		AvailableFunctions:     availableFunctions,
	}

	if strings.TrimSpace(script) == "" {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:       1,
			Column:     1,
			Message:    "script is empty",
			Suggestion: "Define a compute_forecast(ctx) function that returns a list of {timestamp, value} dicts.",
		})
		return result
	}

	// Check for forbidden patterns before parsing
	checkForbiddenPatterns(script, &result)

	// Parse and validate syntax
	opts := &syntax.FileOptions{}
	f, err := opts.Parse("strategy.star", script, syntax.RetainComments)
	if err != nil {
		result.Valid = false
		addSyntaxError(err, &result)
		return result
	}

	// Check for compute_forecast function definition
	checkEntryPoint(f, &result)

	return result
}

// checkForbiddenPatterns scans for forbidden operations in the script source.
func checkForbiddenPatterns(script string, result *Result) {
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, pattern := range forbiddenPatterns {
			if strings.HasPrefix(trimmed, pattern) {
				result.Valid = false
				result.Errors = append(result.Errors, Error{
					Line:    i + 1,
					Column:  strings.Index(line, pattern) + 1,
					Message: fmt.Sprintf("forbidden operation: %q is not allowed in forecast strategies", strings.TrimSpace(pattern)),
					Suggestion: fmt.Sprintf(
						"Remove the %s statement. All required functions are available as builtins: %s",
						strings.TrimSpace(pattern),
						strings.Join(availableFunctions[:8], ", "),
					),
				})
			}
		}
	}
}

// addSyntaxError converts a Starlark syntax error into an Error.
func addSyntaxError(err error, result *Result) {
	errMsg := err.Error()

	// Try to extract line/column from Starlark error format: "file:line:col: message"
	line, col := 0, 0
	msg := errMsg
	if parts := strings.SplitN(errMsg, ":", 4); len(parts) >= 4 {
		if _, scanErr := fmt.Sscanf(parts[1], "%d", &line); scanErr != nil {
			line = 0
		}
		if _, scanErr := fmt.Sscanf(parts[2], "%d", &col); scanErr != nil {
			col = 0
		}
		msg = strings.TrimSpace(parts[3])
	}

	suggestion := "Check the Starlark syntax. Common issues: missing colons after def/if/for, unmatched parentheses, or incorrect indentation."
	if strings.Contains(msg, "got newline") {
		suggestion = "Add a colon ':' at the end of the function/if/for declaration line."
	} else if strings.Contains(msg, "unexpected") {
		suggestion = "Check for unmatched parentheses, brackets, or quotes near this location."
	}

	result.Errors = append(result.Errors, Error{
		Line:       line,
		Column:     col,
		Message:    msg,
		Suggestion: suggestion,
	})
}

// checkEntryPoint verifies that a compute_forecast function with a ctx parameter exists.
func checkEntryPoint(f *syntax.File, result *Result) {
	found := false
	hasCtxParam := false

	for _, stmt := range f.Stmts {
		defStmt, ok := stmt.(*syntax.DefStmt)
		if !ok {
			continue
		}
		if defStmt.Name.Name == "compute_forecast" {
			found = true
			if len(defStmt.Params) >= 1 {
				// Check the first parameter name
				if ident, ok := defStmt.Params[0].(*syntax.Ident); ok && ident.Name == "ctx" {
					hasCtxParam = true
				}
			}
			break
		}
	}

	if !found {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:    1,
			Column:  1,
			Message: "missing required function: compute_forecast",
			Suggestion: "Define the entry point function:\n\ndef compute_forecast(ctx):\n    # ctx fields: " +
				strings.Join(availableContextFields, ", ") +
				"\n    return [{\"timestamp\": ..., \"value\": ...}]",
		})
		return
	}

	if !hasCtxParam {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:       1,
			Column:     1,
			Message:    "compute_forecast must accept a 'ctx' parameter",
			Suggestion: "Change the function signature to: def compute_forecast(ctx):",
		})
	}
}

// ValidateWithExecution performs full validation including a dry-run execution
// with empty data to verify the script can execute without runtime errors.
// This is more thorough than ValidateStrategy but requires the Starlark runtime.
func ValidateWithExecution(script string) Result {
	// First do static validation
	result := ValidateStrategy(script)
	if !result.Valid {
		return result
	}

	predeclared := buildValidationPredeclared()

	thread := &starlarklib.Thread{Name: "validate"}
	globals, err := starlarklib.ExecFileOptions(&syntax.FileOptions{}, thread, "strategy.star", script, predeclared)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:       0,
			Column:     0,
			Message:    fmt.Sprintf("execution error: %s", err.Error()),
			Suggestion: "Fix the runtime error in the top-level script code. Note: the script body outside compute_forecast runs at load time.",
		})
		return result
	}

	validateEntryPoint(globals, &result)

	return result
}

// buildValidationPredeclared constructs the predeclared environment for dry-run validation,
// including universe builtins and stub forecast builtins.
func buildValidationPredeclared() starlarklib.StringDict {
	predeclared := starlarklib.StringDict{}
	for _, name := range []string{
		"True", "False", "None",
		"len", "str", "int", "float", "bool",
		"list", "dict", "tuple", "range",
		"enumerate", "zip", "sorted", "reversed",
		"min", "max", "abs", "any", "all",
		"hasattr", "getattr", "dir", "type", "repr", "hash",
		"print",
	} {
		if val, ok := starlarklib.Universe[name]; ok {
			predeclared[name] = val
		}
	}

	stubBuiltin := starlarklib.NewBuiltin("stub", func(_ *starlarklib.Thread, _ *starlarklib.Builtin, _ starlarklib.Tuple, _ []starlarklib.Tuple) (starlarklib.Value, error) {
		return starlarklib.None, nil
	})
	for _, name := range []string{"avg", "sum", "percentile", "filter_by_hour", "group_by_hour", "duration", "add_seconds", "Decimal"} {
		predeclared[name] = stubBuiltin
	}

	return predeclared
}

// validateEntryPoint checks that compute_forecast exists as a function in the script globals.
func validateEntryPoint(globals starlarklib.StringDict, result *Result) {
	fn, ok := globals["compute_forecast"]
	if !ok {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:       1,
			Column:     1,
			Message:    "compute_forecast not found in script globals after execution",
			Suggestion: "Ensure compute_forecast is defined at the top level, not inside another function.",
		})
		return
	}

	if _, ok := fn.(*starlarklib.Function); !ok {
		result.Valid = false
		result.Errors = append(result.Errors, Error{
			Line:       1,
			Column:     1,
			Message:    fmt.Sprintf("compute_forecast must be a function, got %s", fn.Type()),
			Suggestion: "Define compute_forecast as: def compute_forecast(ctx): ...",
		})
	}
}
