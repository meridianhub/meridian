// Package mcperrors provides structured error formatting for the MCP server.
// It transforms gRPC errors and validation failures into structured JSON with
// line numbers, suggestions, and actionable feedback for the Write-Validate-Fix loop.
package mcperrors

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/grpc/status"
)

// Error type constants identify the source of a formatted error.
const (
	// TypeCELCompilation is produced by CEL expression compilation failures.
	TypeCELCompilation = "cel_compilation"
	// TypeStarlarkSyntax is produced by Starlark script parsing or execution failures.
	TypeStarlarkSyntax = "starlark_syntax"
	// TypeManifestValidation is produced by manifest structural or proto validation.
	TypeManifestValidation = "manifest_validation"
	// TypeProtoValidation is produced by protovalidate field constraint violations.
	TypeProtoValidation = "proto_validation"
	// TypeGeneric is used when the error type cannot be determined.
	TypeGeneric = "generic"
)

// ErrorDetail describes a single error with structured location and suggestion data.
type ErrorDetail struct {
	// Type categorizes the error source (cel_compilation, starlark_syntax, etc.).
	Type string `json:"type"`
	// Line is the 1-based source line number, when available.
	Line int `json:"line,omitempty"`
	// Column is the 1-based source column number, when available.
	Column int `json:"column,omitempty"`
	// Message is a human-readable description of the issue.
	Message string `json:"message"`
	// Suggestion is an optional "Did you mean...?" hint for typos.
	Suggestion string `json:"suggestion,omitempty"`
	// Path is the dotted field path within a manifest (e.g., "instruments[0].code").
	Path string `json:"path,omitempty"`
}

// FormattedError is the top-level response returned by FormatGRPCError.
type FormattedError struct {
	// Valid is true when there are no errors.
	Valid bool `json:"valid"`
	// Errors contains all extracted error details.
	Errors []ErrorDetail `json:"errors,omitempty"`
}

// knownServiceBindings lists Starlark service modules injected by the saga runtime.
var knownServiceBindings = []string{
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

// knownStarlarkBuiltins lists the top-level names available in saga Starlark scripts.
var knownStarlarkBuiltins = []string{
	"input_data",
	"invoke_handler",
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

// celAvailableFields lists the CEL variable names commonly available in account-type policy expressions.
var celAvailableFields = []string{
	"quantity",
	"instrument",
	"bucket_id",
	"as_of",
	"amount",
	"instrument_code",
	"attributes",
	"payload",
	"value",
	"party_type",
}

// celErrorPattern matches CEL error messages like "ERROR: :1:15: message".
// Group 1 = line, Group 2 = column, Group 3 = message.
var celErrorPattern = regexp.MustCompile(`ERROR:\s*[^:]*:(\d+):(\d+):\s*(.+)`)

// starlarkErrorPattern matches Starlark error locations like "file.star:5:10: message".
// Group 1 = line, Group 2 = column, Group 3 = message.
var starlarkErrorPattern = regexp.MustCompile(`[^:]*\.star:(\d+):(\d+):\s*(.+)`)

// undeclaredReferencePattern matches "undeclared reference to 'name'".
var undeclaredReferencePattern = regexp.MustCompile(`undeclared reference to '([^']+)'`)

// undefinedNamePattern matches "undefined: name".
var undefinedNamePattern = regexp.MustCompile(`undefined:\s*(\S+)`)

// validationJSONPattern matches the JSON array embedded in manifest validation error messages.
// The control-plane grpc_handler embeds validation errors as JSON in the message.
var validationJSONPattern = regexp.MustCompile(`\[(\{.+})\]`)

// manifestValidationEntry is used to deserialise JSON embedded in manifest validation errors.
type manifestValidationEntry struct {
	Severity   string `json:"severity"`
	Path       string `json:"path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// FormatGRPCError formats a gRPC (or plain) error into a structured FormattedError.
// It detects the error type from the message content and delegates to the
// appropriate parser, enriching the result with line numbers and suggestions.
func FormatGRPCError(err error) FormattedError {
	if err == nil {
		return FormattedError{Valid: true}
	}

	msg := extractMessage(err)

	switch {
	case isCELError(msg):
		return FormattedError{Valid: false, Errors: parseCELError(msg)}
	case isStarlarkError(msg):
		return FormattedError{Valid: false, Errors: parseStarlarkError(msg)}
	case isManifestValidationError(msg):
		return FormattedError{Valid: false, Errors: parseManifestValidationError(msg)}
	default:
		return FormattedError{
			Valid: false,
			Errors: []ErrorDetail{
				{Type: TypeGeneric, Message: msg},
			},
		}
	}
}

// extractMessage returns the underlying error message regardless of whether
// the error is a gRPC status error or a plain error.
func extractMessage(err error) string {
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}

// isCELError returns true when the message looks like a CEL compilation error.
func isCELError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "cel") && strings.Contains(msg, "ERROR:")
}

// isStarlarkError returns true when the message looks like a Starlark error.
func isStarlarkError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "starlark") || strings.Contains(msg, ".star:")
}

// isManifestValidationError returns true when the message embeds a JSON validation array.
func isManifestValidationError(msg string) bool {
	return strings.Contains(msg, "manifest validation") ||
		validationJSONPattern.MatchString(msg)
}

// parseCELError parses a CEL compilation error message.
// CEL errors look like: "cel compilation failed: ERROR: :1:15: undeclared reference to 'atributes'"
func parseCELError(msg string) []ErrorDetail {
	detail := ErrorDetail{Type: TypeCELCompilation, Message: msg}

	if m := celErrorPattern.FindStringSubmatch(msg); m != nil {
		detail.Line, _ = strconv.Atoi(m[1])
		detail.Column, _ = strconv.Atoi(m[2])
		detail.Message = strings.TrimSpace(m[3])
	}

	// Typo suggestion for undeclared references.
	if m := undeclaredReferencePattern.FindStringSubmatch(msg); m != nil {
		if suggestion := findClosestMatch(m[1], celAvailableFields); suggestion != "" {
			detail.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
	}

	return []ErrorDetail{detail}
}

// parseStarlarkError parses a Starlark compilation or execution error.
// Starlark errors look like: "transfer.star:5:10: got end of file, want expression"
func parseStarlarkError(msg string) []ErrorDetail {
	detail := ErrorDetail{Type: TypeStarlarkSyntax, Message: msg}

	if m := starlarkErrorPattern.FindStringSubmatch(msg); m != nil {
		detail.Line, _ = strconv.Atoi(m[1])
		detail.Column, _ = strconv.Atoi(m[2])
		detail.Message = strings.TrimSpace(m[3])
	}

	// Suggestion for undefined names.
	if m := undefinedNamePattern.FindStringSubmatch(msg); m != nil {
		allNames := make([]string, 0, len(knownServiceBindings)+len(knownStarlarkBuiltins))
		allNames = append(allNames, knownServiceBindings...)
		allNames = append(allNames, knownStarlarkBuiltins...)
		if suggestion := findClosestMatch(m[1], allNames); suggestion != "" {
			detail.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
	}

	return []ErrorDetail{detail}
}

// parseManifestValidationError extracts structured validation errors embedded as JSON
// in the gRPC error message by the control-plane ApplyManifestHandler.
func parseManifestValidationError(msg string) []ErrorDetail {
	// Try to extract JSON array from the message.
	if m := validationJSONPattern.FindStringSubmatch(msg); m != nil {
		jsonBytes := []byte("[" + m[1] + "]")
		var entries []manifestValidationEntry
		if err := json.Unmarshal(jsonBytes, &entries); err == nil && len(entries) > 0 {
			details := make([]ErrorDetail, 0, len(entries))
			for _, e := range entries {
				detail := ErrorDetail{
					Type:       TypeManifestValidation,
					Message:    e.Message,
					Path:       e.Path,
					Suggestion: e.Suggestion,
				}
				details = append(details, detail)
			}
			return details
		}
	}

	// Fallback: return as a single generic manifest validation error.
	return []ErrorDetail{
		{Type: TypeManifestValidation, Message: msg},
	}
}

// findClosestMatch returns the candidate from candidates that has the smallest
// Levenshtein distance to target, provided the distance is within half of
// the target length. Returns empty string if no suitable match is found.
func findClosestMatch(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	threshold := len(target)/2 + 1

	for _, candidate := range candidates {
		dist := levenshteinDistance(strings.ToLower(target), strings.ToLower(candidate))
		if dist < threshold {
			threshold = dist
			bestMatch = candidate
		}
	}

	return bestMatch
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	la := len(a)
	lb := len(b)

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := range prev {
		prev[i] = i
	}

	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}

	return prev[la]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
