// Package csv provides CSV parsing capabilities for the position-tool bulk import.
// It implements schema-driven column mapping based on instrument definitions.
package csv

import (
	"regexp"
	"sort"
	"strings"
)

// attributePattern matches CEL attribute references in the form:
//   - attributes.key
//   - attributes["key"]
//   - attributes['key']
var attributePattern = regexp.MustCompile(`attributes(?:\.(\w+)|\[["'](\w+)["']\])`)

// ExtractAttributeKeys parses a CEL fungibility key expression and extracts
// all attribute key references. This enables schema-driven CSV column discovery
// without requiring external configuration files.
//
// Supported patterns:
//   - attributes.region -> "region"
//   - attributes["grade"] -> "grade"
//   - attributes['batch_id'] -> "batch_id"
//
// Example:
//
//	ExtractAttributeKeys(`bucket_key([attributes.region, attributes.grade])`)
//	// Returns: ["grade", "region"]
//
// The returned keys are deduplicated and sorted alphabetically for deterministic ordering.
func ExtractAttributeKeys(celExpression string) []string {
	if celExpression == "" {
		return nil
	}

	matches := attributePattern.FindAllStringSubmatch(celExpression, -1)
	if len(matches) == 0 {
		return nil
	}

	// Use a map for deduplication
	keySet := make(map[string]struct{})
	for _, match := range matches {
		// match[1] is from attributes.key pattern
		// match[2] is from attributes["key"] or attributes['key'] pattern
		key := match[1]
		if key == "" {
			key = match[2]
		}
		if key != "" {
			keySet[key] = struct{}{}
		}
	}

	// Convert to sorted slice for deterministic output
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// ValidateAttributeKey checks if a key conforms to the attribute key format.
// Valid keys are snake_case: lowercase letters, digits, and underscores,
// starting with a letter.
var attributeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateAttributeKey returns true if the key is a valid attribute key.
func ValidateAttributeKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	return attributeKeyPattern.MatchString(key)
}

// NormalizeHeaderToAttributeKey normalizes a CSV header to a valid attribute key.
// Converts to lowercase and replaces spaces/dashes with underscores.
// Returns empty string if the normalized result is not a valid key.
func NormalizeHeaderToAttributeKey(header string) string {
	// Trim whitespace
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}

	// Convert to lowercase
	normalized := strings.ToLower(header)

	// Replace common separators with underscores
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")

	// Collapse multiple underscores
	for strings.Contains(normalized, "__") {
		normalized = strings.ReplaceAll(normalized, "__", "_")
	}

	// Trim leading/trailing underscores
	normalized = strings.Trim(normalized, "_")

	// Validate the result
	if !ValidateAttributeKey(normalized) {
		return ""
	}

	return normalized
}
