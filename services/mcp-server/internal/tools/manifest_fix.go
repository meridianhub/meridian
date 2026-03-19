// Package tools provides tool handlers for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// RegisterManifestFixTool registers the meridian_manifest_fix tool onto the SDK server.
func RegisterManifestFixTool(srv *mcp.Server, schemaRegistry *schema.Registry) {
	addTool(srv, buildManifestFixTool(schemaRegistry))
}

// buildManifestFixTool returns the meridian_manifest_fix Tool definition.
func buildManifestFixTool(schemaRegistry *schema.Registry) Tool {
	return Tool{
		Name:     "meridian_manifest_fix",
		Category: CategorySimulate,
		Description: "Auto-convert deprecated handler calls in a manifest's saga scripts. " +
			"Returns the fixed manifest with updated handler names and parameter mappings, " +
			"plus a list of conversions applied. Does NOT apply the manifest.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"type":        "object",
					"description": "The manifest JSON object containing sagas to fix.",
				},
			},
			"required": []interface{}{"manifest"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestFix(ctx, schemaRegistry, params)
		},
	}
}

// manifestFixParams holds parsed parameters for meridian_manifest_fix.
type manifestFixParams struct {
	Manifest json.RawMessage `json:"manifest"`
}

// manifestFixConversion records a single conversion applied to a saga script.
type manifestFixConversion struct {
	Saga    string `json:"saga"`
	Message string `json:"message"`
}

// handleManifestFix implements the meridian_manifest_fix handler logic.
func handleManifestFix(_ context.Context, schemaRegistry *schema.Registry, params json.RawMessage) (interface{}, error) {
	var p manifestFixParams
	if err := json.Unmarshal(params, &p); err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid params: %v", err),
		}, nil
	}

	// Parse the manifest as a generic map
	var manifest map[string]interface{}
	if err := json.Unmarshal(p.Manifest, &manifest); err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("invalid manifest JSON: %v", err),
		}, nil
	}

	conversions, err := fixManifestSagas(manifest, schemaRegistry)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // error is surfaced in the tool response, not returned as a Go error
			"error": err.Error(),
		}, nil
	}

	// Build conversions as []interface{} for JSON
	convResults := make([]interface{}, 0, len(conversions))
	for _, c := range conversions {
		convResults = append(convResults, map[string]interface{}{
			"saga":    c.Saga,
			"message": c.Message,
		})
	}

	return map[string]interface{}{
		"manifest":    manifest,
		"conversions": convResults,
	}, nil
}

// fixManifestSagas iterates over sagas in the manifest and converts deprecated handler calls.
// It mutates the manifest in-place and returns a list of conversions applied.
func fixManifestSagas(manifest map[string]interface{}, schemaRegistry *schema.Registry) ([]manifestFixConversion, error) {
	sagasRaw, ok := manifest["sagas"]
	if !ok {
		return nil, nil
	}

	sagas, ok := sagasRaw.([]interface{})
	if !ok {
		return nil, nil
	}

	var allConversions []manifestFixConversion

	for _, sagaRaw := range sagas {
		saga, ok := sagaRaw.(map[string]interface{})
		if !ok {
			continue
		}

		scriptRaw, ok := saga["script"]
		if !ok {
			continue
		}
		script, ok := scriptRaw.(string)
		if !ok {
			continue
		}

		sagaName, _ := saga["name"].(string)

		fixedScript, conversions := fixScriptDeprecatedCalls(script, sagaName, schemaRegistry)
		if len(conversions) > 0 {
			saga["script"] = fixedScript
			allConversions = append(allConversions, conversions...)
		}
	}

	return allConversions, nil
}

// deprecatedInfo holds the current handler name and conversion rule for a deprecated handler.
type deprecatedInfo struct {
	currentName string
	rule        *schema.ConversionRule
}

// fixScriptDeprecatedCalls scans a Starlark script for deprecated handler calls and
// rewrites them to use the current handler name and parameter names.
// Uses text-level replacement to preserve script formatting.
func fixScriptDeprecatedCalls(script string, sagaName string, schemaRegistry *schema.Registry) (string, []manifestFixConversion) {
	deprecatedHandlers := collectDeprecatedHandlers(schemaRegistry)
	if len(deprecatedHandlers) == 0 {
		return script, nil
	}

	// Sort deprecated handler names by length (longest first) to avoid partial replacements
	sortedNames := make([]string, 0, len(deprecatedHandlers))
	for name := range deprecatedHandlers {
		sortedNames = append(sortedNames, name)
	}
	sort.Slice(sortedNames, func(i, j int) bool {
		return len(sortedNames[i]) > len(sortedNames[j])
	})

	var conversions []manifestFixConversion
	result := script

	for _, oldName := range sortedNames {
		info := deprecatedHandlers[oldName]
		// Use regex to detect calls with optional whitespace before '('
		callPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldName) + `\s*\(`)
		if !callPattern.MatchString(result) {
			continue
		}

		result = applyHandlerConversion(result, oldName, info)
		conversions = append(conversions, manifestFixConversion{
			Saga:    sagaName,
			Message: buildConversionMessage(oldName, info),
		})
	}

	return result, conversions
}

// collectDeprecatedHandlers builds a map of deprecated handler names to their replacement info.
func collectDeprecatedHandlers(schemaRegistry *schema.Registry) map[string]deprecatedInfo {
	metadata := schemaRegistry.BuildLinterMetadata()
	result := make(map[string]deprecatedInfo)
	for name, meta := range metadata {
		if meta.IsDeprecated && meta.ReplacedBy != "" {
			if mapping := schemaRegistry.LookupDeprecated(name); mapping != nil {
				result[name] = deprecatedInfo{
					currentName: mapping.CurrentName,
					rule:        mapping.ConversionRule,
				}
			}
		}
	}
	return result
}

// applyHandlerConversion replaces the deprecated handler call and its parameter names in the script.
// Parameter renaming is scoped to each call site's argument list to avoid corrupting other calls.
func applyHandlerConversion(script string, oldName string, info deprecatedInfo) string {
	newCall := info.currentName + "("
	// Regex matches oldName (with word boundary) followed by optional whitespace and '('
	callRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldName) + `\s*\(`)

	// Build reverse mapping: old param name -> new param name.
	// Empty when no param mapping exists — handler name is still replaced
	// using the string/comment-aware loop below to avoid modifying literals.
	var reverseMapping map[string]string
	if info.rule != nil && len(info.rule.ParamMapping) > 0 {
		reverseMapping = make(map[string]string, len(info.rule.ParamMapping))
		for newParam, oldParam := range info.rule.ParamMapping {
			reverseMapping[oldParam] = newParam
		}
	}

	// Process each occurrence of the deprecated call, replacing handler name
	// and renaming params only within the call's parentheses.
	// Skip occurrences inside string literals or comments.
	var result strings.Builder
	remaining := script
	for {
		loc := callRe.FindStringIndex(remaining)
		if loc == nil {
			result.WriteString(remaining)
			break
		}

		// Check if the match is inside a string literal or comment
		if isInsideStringOrComment(remaining, loc[0]) {
			// Copy through the matched text without modification
			result.WriteString(remaining[:loc[1]])
			remaining = remaining[loc[1]:]
			continue
		}

		// Write text before the call and the new handler name
		result.WriteString(remaining[:loc[0]])
		result.WriteString(newCall)
		remaining = remaining[loc[1]:]

		// Find the matching closing paren, tracking nesting depth
		callBody, rest := extractCallBody(remaining)
		// Rename top-level kwargs within the call body (only when mapping exists)
		if len(reverseMapping) > 0 {
			callBody = renameTopLevelKwargs(callBody, reverseMapping)
		}
		result.WriteString(callBody)
		remaining = rest
	}

	return result.String()
}

// extractCallBody splits text at the matching closing paren for an already-opened call.
// Handles strings (single/double/triple-quoted) and comments so that parens inside
// literals do not affect depth tracking.
// Returns (bodyIncludingCloseParen, rest). If no matching paren is found, returns (all, "").
func extractCallBody(s string) (string, string) {
	depth := 1
	i := 0
	for i < len(s) {
		switch s[i] {
		case '#':
			// Skip to end of line (comment)
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case '"', '\'':
			i = skipString(s, i)
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
			i++
		default:
			i++
		}
	}
	return s, ""
}

// skipString advances past a string literal starting at position i.
// Handles triple-quoted strings (""", ”') and single-quoted strings with escapes.
func skipString(s string, i int) int {
	quote := s[i]
	// Check for triple-quoted string
	if i+2 < len(s) && s[i+1] == quote && s[i+2] == quote {
		triple := string([]byte{quote, quote, quote})
		end := strings.Index(s[i+3:], triple)
		if end >= 0 {
			return i + 3 + end + 3
		}
		return len(s)
	}
	// Single-quoted string: advance past closing quote, handling escapes
	i++
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == quote {
			return i + 1
		}
		i++
	}
	return len(s)
}

// isInsideStringOrComment returns true if position pos in s falls inside
// a string literal or a line comment. It scans from the beginning of s,
// tracking string and comment boundaries.
func isInsideStringOrComment(s string, pos int) bool {
	i := 0
	for i < pos {
		switch s[i] {
		case '#':
			// Line comment extends to end of line or end of string.
			// If pos is anywhere in the comment, it's inside.
			for i < len(s) && s[i] != '\n' {
				if i == pos {
					return true
				}
				i++
			}
		case '"', '\'':
			start := i
			i = skipString(s, i)
			// If pos falls within the string literal range, it's inside.
			if pos >= start && pos < i {
				return true
			}
		default:
			i++
		}
	}
	return false
}

// kwargScanner holds state for scanning a call body and renaming top-level kwargs.
type kwargScanner struct {
	src            string
	reverseMapping map[string]string
	result         strings.Builder
	depth          int
	pos            int
}

// renameTopLevelKwargs renames keyword argument names at the top level of a call body.
// Only renames "name=" patterns that appear at kwarg position, not inside nested calls,
// strings, or as suffixes of longer identifiers.
func renameTopLevelKwargs(callBody string, reverseMapping map[string]string) string {
	s := &kwargScanner{src: callBody, reverseMapping: reverseMapping}
	s.scan()
	return s.result.String()
}

func (s *kwargScanner) scan() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case '#':
			s.copyComment()
		case '"', '\'':
			s.copyStringLiteral()
		case '(':
			s.depth++
			s.result.WriteByte('(')
			s.pos++
		case ')':
			if s.depth > 0 {
				s.depth--
			}
			s.result.WriteByte(')')
			s.pos++
		default:
			if s.depth == 0 && isIdentStart(s.src[s.pos]) {
				s.handleTopLevelIdent()
			} else {
				s.result.WriteByte(s.src[s.pos])
				s.pos++
			}
		}
	}
}

func (s *kwargScanner) copyComment() {
	start := s.pos
	for s.pos < len(s.src) && s.src[s.pos] != '\n' {
		s.pos++
	}
	s.result.WriteString(s.src[start:s.pos])
}

func (s *kwargScanner) copyStringLiteral() {
	start := s.pos
	s.pos = skipString(s.src, s.pos)
	s.result.WriteString(s.src[start:s.pos])
}

func (s *kwargScanner) handleTopLevelIdent() {
	start := s.pos
	for s.pos < len(s.src) && isIdentContinue(s.src[s.pos]) {
		s.pos++
	}
	ident := s.src[start:s.pos]

	// Match "ident=" or "ident =" but not "ident==" (comparison operator).
	// Skip optional whitespace between identifier and '='.
	eqPos := s.pos
	for eqPos < len(s.src) && (s.src[eqPos] == ' ' || s.src[eqPos] == '\t') {
		eqPos++
	}
	if eqPos < len(s.src) && s.src[eqPos] == '=' &&
		(eqPos+1 >= len(s.src) || s.src[eqPos+1] != '=') &&
		!isPrecededByIdent(s.src, start) {
		if newName, ok := s.reverseMapping[ident]; ok {
			s.result.WriteString(newName)
			return
		}
	}
	s.result.WriteString(ident)
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentContinue(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

// isPrecededByIdent checks if the character immediately before pos is an identifier char.
// This prevents matching "total_amount" when looking for "amount".
func isPrecededByIdent(s string, pos int) bool {
	if pos == 0 {
		return false
	}
	return isIdentContinue(s[pos-1])
}

// buildConversionMessage creates a human-readable description of a handler conversion.
func buildConversionMessage(oldName string, info deprecatedInfo) string {
	msg := fmt.Sprintf("Converted %s -> %s", oldName, info.currentName)
	if info.rule != nil && len(info.rule.ParamMapping) > 0 {
		mappings := make([]string, 0, len(info.rule.ParamMapping))
		for newParam, oldParam := range info.rule.ParamMapping {
			mappings = append(mappings, fmt.Sprintf("%s->%s", oldParam, newParam))
		}
		sort.Strings(mappings)
		msg += fmt.Sprintf(" (params: %s)", strings.Join(mappings, ", "))
	}
	if info.rule != nil && info.rule.Sunset != "" {
		msg += fmt.Sprintf(" [deprecated, removal in v%s]", info.rule.Sunset)
	}
	return msg
}
