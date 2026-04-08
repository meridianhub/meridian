package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReportFormatter formats a ValidationResult into a string representation.
type ReportFormatter interface {
	Format(result *ValidationResult) string
}

// HumanReadableFormatter formats validation results for CLI output with terminal colors.
type HumanReadableFormatter struct {
	// AvailableHandlers is the list of registered handler names for suggesting corrections.
	AvailableHandlers []string
}

// Format returns a human-readable string representation of the validation result.
func (f *HumanReadableFormatter) Format(result *ValidationResult) string {
	var output strings.Builder

	useColors := shouldUseColors()

	if result.Success {
		// Success format
		if useColors {
			output.WriteString("\033[32m✅ Validation Passed\033[0m\n")
		} else {
			output.WriteString("✅ Validation Passed\n")
		}

		// Show metrics
		complexityScore := calculateComplexityScore(result.Metrics.HandlerCallCount)
		complexityLabel := getComplexityLabel(complexityScore)

		fmt.Fprintf(&output, "   • %d handlers called\n", result.Metrics.HandlerCallCount)
		fmt.Fprintf(&output, "   • Complexity: %d/10 (%s)\n", complexityScore, complexityLabel)

		// Show estimated duration
		durationMs := result.Metrics.EstimatedDuration.Milliseconds()
		fmt.Fprintf(&output, "   • Estimated execution: <%dms\n", durationMs)

		output.WriteString("\nScript ready for deployment.\n")
	} else {
		// Failure format
		if useColors {
			output.WriteString("\033[31m❌ Validation Failed:\033[0m\n")
		} else {
			output.WriteString("❌ Validation Failed:\n")
		}

		// Show error count
		errorCount := len(result.Errors)
		if errorCount == 1 {
			output.WriteString("   1 error found:\n\n")
		} else {
			fmt.Fprintf(&output, "   %d errors found:\n\n", errorCount)
		}

		// Show each error
		for _, err := range result.Errors {
			f.formatError(&output, err)
		}

		output.WriteString("\nScript rejected. Fix errors and resubmit.\n")
	}

	return output.String()
}

// formatError formats a single validation error with context and suggestions.
func (f *HumanReadableFormatter) formatError(output *strings.Builder, err ValidationError) {
	// Show line/column if available
	if err.Line > 0 {
		fmt.Fprintf(output, "   Line %d", err.Line)
		if err.Column > 0 {
			fmt.Fprintf(output, ", Column %d", err.Column)
		}
		output.WriteString(":\n")
	}

	// Show message
	fmt.Fprintf(output, "   %s\n", err.Message)

	// Add suggestions for undefined handlers
	if err.Category == CategoryUndefinedHandler && len(f.AvailableHandlers) > 0 {
		// Extract handler name from error message
		handlerName := extractHandlerName(err.Message)
		if handlerName != "" {
			suggestion := findClosestHandler(handlerName, f.AvailableHandlers)
			if suggestion != "" {
				fmt.Fprintf(output, "   Suggestion: Did you mean '%s'?\n", suggestion)
			}
		}
	}

	output.WriteString("\n")
}

// JSONFormatter formats validation results as JSON for API responses.
type JSONFormatter struct {
	// AvailableHandlers is the list of registered handler names for suggesting corrections.
	AvailableHandlers []string
}

// Format returns a JSON string representation of the validation result.
func (f *JSONFormatter) Format(result *ValidationResult) string {
	report := JSONReport{
		Success: result.Success,
		Errors:  make([]JSONError, 0, len(result.Errors)),
		Metrics: JSONMetrics{
			HandlerCallCount:    result.Metrics.HandlerCallCount,
			ComplexityScore:     calculateComplexityScore(result.Metrics.HandlerCallCount),
			EstimatedDurationMs: int(result.Metrics.EstimatedDuration.Milliseconds()),
		},
	}

	// Convert errors
	for _, err := range result.Errors {
		jsonErr := JSONError{
			Line:     err.Line,
			Column:   err.Column,
			Message:  err.Message,
			Category: string(err.Category),
		}

		// Add suggestions for undefined handlers
		if err.Category == CategoryUndefinedHandler && len(f.AvailableHandlers) > 0 {
			handlerName := extractHandlerName(err.Message)
			if handlerName != "" {
				suggestion := findClosestHandler(handlerName, f.AvailableHandlers)
				if suggestion != "" {
					jsonErr.Suggestion = fmt.Sprintf("Did you mean '%s'?", suggestion)
				}
			}
		}

		report.Errors = append(report.Errors, jsonErr)
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		// Fallback to simple error JSON using proper marshaling to avoid escaping issues
		fallback := JSONReport{
			Success: false,
			Errors: []JSONError{
				{
					Message:  fmt.Sprintf("Failed to format report: %s", err.Error()),
					Category: "INTERNAL_ERROR",
				},
			},
		}
		// If this also fails, return a minimal hardcoded fallback
		fallbackData, fallbackErr := json.Marshal(fallback)
		if fallbackErr != nil {
			return `{"success":false,"errors":[{"message":"Report formatting failed","category":"INTERNAL_ERROR"}]}`
		}
		return string(fallbackData)
	}

	return string(data)
}

// JSONReport represents the JSON schema for validation reports.
type JSONReport struct {
	Success bool        `json:"success"`
	Errors  []JSONError `json:"errors,omitempty"`
	Metrics JSONMetrics `json:"metrics"`
}

// JSONError represents a single validation error in JSON format.
type JSONError struct {
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Message    string `json:"message"`
	Category   string `json:"category"`
	Suggestion string `json:"suggestion,omitempty"`
}

// JSONMetrics represents complexity metrics in JSON format.
type JSONMetrics struct {
	HandlerCallCount    int `json:"handler_call_count"`
	ComplexityScore     int `json:"complexity_score"`
	EstimatedDurationMs int `json:"estimated_duration_ms"`
}

// calculateComplexityScore calculates a 0-10 complexity score based on handler call count.
// Formula: min(10, HandlerCallCount / 2)
func calculateComplexityScore(handlerCallCount int) int {
	score := handlerCallCount / 2
	if score > 10 {
		return 10
	}
	return score
}

// getComplexityLabel returns a human-readable label for a complexity score.
func getComplexityLabel(score int) string {
	if score >= 7 {
		return "High"
	}
	if score >= 4 {
		return "Medium"
	}
	return "Low"
}

// shouldUseColors determines if terminal colors should be used.
// Returns false if running in CI environment.
func shouldUseColors() bool {
	// Check if CI environment variable is set
	ci := os.Getenv("CI")
	return ci == "" || ci == "false"
}

// extractHandlerName extracts the handler name from an error message.
// Examples:
//   - "handler 'payment_order.create_lien' not found" → "payment_order.create_lien"
//   - "undefined: position_keeping.foo" → "position_keeping.foo"
func extractHandlerName(message string) string {
	// Try to extract from single quotes
	if start := strings.Index(message, "'"); start != -1 {
		end := strings.Index(message[start+1:], "'")
		if end != -1 {
			return message[start+1 : start+1+end]
		}
	}

	// Try to extract from "not found in registry" pattern
	if strings.Contains(message, "not found in registry") {
		parts := strings.Fields(message)
		for i, part := range parts {
			if part == "handler" && i+1 < len(parts) {
				return strings.Trim(parts[i+1], "'\"")
			}
		}
	}

	return ""
}

// findClosestHandler finds the handler with the smallest Levenshtein distance.
// For qualified handler names (service.method), it tries two strategies:
// 1. Compare full handler names (strict match)
// 2. Compare only method names (lenient match for wrong service)
// Returns empty string if no handler is within distance 3.
func findClosestHandler(target string, available []string) string {
	if len(available) == 0 {
		return ""
	}

	// Strategy 1: Compare full handler names
	closestHandler, minDistance := findClosestByFullName(target, available)

	// If full name match is good enough (≤ 3), return it
	if minDistance <= 3 {
		return closestHandler
	}

	// Strategy 2: For qualified names, compare method names only
	closestHandler, minDistance = findClosestByMethodName(target, available, minDistance)

	// Only suggest if distance is reasonable (≤ 3)
	if minDistance <= 3 {
		return closestHandler
	}

	return ""
}

// findClosestByFullName finds the handler with smallest Levenshtein distance using full names.
func findClosestByFullName(target string, available []string) (string, int) {
	minDistance := 999999
	closestHandler := ""

	for _, handler := range available {
		distance := levenshteinDistance(target, handler)
		if distance < minDistance {
			minDistance = distance
			closestHandler = handler
		}
	}

	return closestHandler, minDistance
}

// findClosestByMethodName finds the handler with matching or similar method name.
// This handles cases like "payment_order.create_lien" vs "position_keeping.create_lien"
// where the user got the service wrong but the method is correct.
func findClosestByMethodName(target string, available []string, currentMinDistance int) (string, int) {
	targetParts := strings.Split(target, ".")
	if len(targetParts) != 2 {
		return "", currentMinDistance
	}

	targetMethod := targetParts[1]
	minDistance := currentMinDistance
	closestHandler := ""

	for _, handler := range available {
		handlerParts := strings.Split(handler, ".")
		if len(handlerParts) != 2 {
			continue
		}

		handlerMethod := handlerParts[1]

		// If methods match exactly, suggest this handler
		if targetMethod == handlerMethod {
			return handler, 0
		}

		// If methods are close (≤ 3), suggest this handler
		distance := levenshteinDistance(targetMethod, handlerMethod)
		if distance < minDistance && distance <= 3 {
			minDistance = distance
			closestHandler = handler
		}
	}

	return closestHandler, minDistance
}

// levenshteinDistance calculates the Levenshtein distance between two strings.
// This is the minimum number of single-character edits (insertions, deletions, or substitutions)
// required to change one string into the other.
func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}

	if len(a) == 0 {
		return len(b)
	}

	if len(b) == 0 {
		return len(a)
	}

	// Create a matrix to store distances
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
	}

	// Initialize first row and column
	for i := 0; i <= len(a); i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		matrix[0][j] = j
	}

	// Fill the matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			matrix[i][j] = min3(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

// min3 returns the minimum of three integers.
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
