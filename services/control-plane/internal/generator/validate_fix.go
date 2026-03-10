package generator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// errUnexpectedLoopExit is returned when the validate-fix loop exits unexpectedly.
// This is a sentinel for an unreachable code path.
var errUnexpectedLoopExit = errors.New("validate-fix: unexpected loop exit")

// ManifestValidator abstracts the validation pipeline for the validate-fix loop.
// It performs a dry-run validation returning structured errors without persisting anything.
type ManifestValidator interface {
	// ValidateDryRun validates the manifest YAML and returns structured results.
	// No side effects are performed — this is a pure validation check.
	ValidateDryRun(ctx context.Context, manifestYAML string) (*ValidationResult, error)
}

// ValidationResult holds the structured outcome of a dry-run validation.
type ValidationResult struct {
	// Valid is true when there are no error-severity findings.
	Valid bool

	// Errors contains all error-severity findings that block activation.
	Errors []ValidationError

	// Warnings contains all warning-severity findings.
	Warnings []ValidationError
}

// ValidateFixOptions configures the validate-fix loop.
type ValidateFixOptions struct {
	// MaxIterations is the maximum number of LLM fix iterations to attempt.
	// A value of 0 means no LLM fix attempts are made (validate only).
	MaxIterations int

	// LLMClient is used to request fixes from the LLM after validation failures.
	LLMClient LLMClient

	// Validator performs dry-run manifest validation.
	Validator ManifestValidator

	// SchemaRegistry is used for error enrichment (available handlers, topics).
	// May be nil — enrichment is skipped when no registry is provided.
	SchemaRegistry *schema.Registry
}

// ValidateFixResult is the outcome of a validate-fix loop run.
type ValidateFixResult struct {
	// FinalManifest is the last manifest produced (may still have errors if the loop exhausted).
	FinalManifest string

	// Valid is true when the final manifest passed validation with no errors.
	Valid bool

	// Errors contains any remaining error-severity findings after all iterations.
	Errors []ValidationError

	// Warnings contains any warning-severity findings from the final validation.
	Warnings []ValidationError

	// IterationsUsed is the number of LLM fix iterations consumed (0 if valid on first pass).
	IterationsUsed int
}

// ValidateAndFix runs the validate-fix feedback loop:
//  1. Apply mutating phase (auto-convert deprecated handlers)
//  2. Validate via ManifestValidator.ValidateDryRun
//  3. If valid: return success with warnings
//  4. If errors: enrich errors, send to LLM via LLMClient.Fix, repeat
//  5. After MaxIterations: return with remaining errors and Valid=false
func ValidateAndFix(ctx context.Context, manifestYAML string, opts ValidateFixOptions) (*ValidateFixResult, error) {
	current := manifestYAML

	for iter := 0; iter <= opts.MaxIterations; iter++ {
		// Step 1: Apply mutating phase (auto-fix deprecated handlers)
		current = applyMutatingPhase(current, opts.SchemaRegistry)

		// Step 2: Validate
		result, err := opts.Validator.ValidateDryRun(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("validate manifest (iteration %d): %w", iter, err)
		}

		// Step 3: If valid, we are done
		if result.Valid {
			return &ValidateFixResult{
				FinalManifest:  current,
				Valid:          true,
				Errors:         nil,
				Warnings:       result.Warnings,
				IterationsUsed: iter,
			}, nil
		}

		// Step 4: Errors remain — if we've used all iterations, stop
		if iter >= opts.MaxIterations {
			return &ValidateFixResult{
				FinalManifest:  current,
				Valid:          false,
				Errors:         result.Errors,
				Warnings:       result.Warnings,
				IterationsUsed: iter,
			}, nil
		}

		// Step 5: Enrich errors and ask LLM to fix
		enriched := enrichErrors(result.Errors, opts.SchemaRegistry)
		fixed, err := opts.LLMClient.Fix(ctx, current, enriched)
		if err != nil {
			return nil, fmt.Errorf("fix manifest (iteration %d): %w", iter, err)
		}
		current = fixed
	}

	// Unreachable: the loop always returns inside the body.
	return nil, errUnexpectedLoopExit
}

// applyMutatingPhase auto-converts deprecated handler calls in Starlark scripts
// embedded in the manifest YAML. When no schema registry is provided or no
// deprecated handlers are present, the manifest is returned unchanged.
//
// The function operates on YAML text by finding saga script blocks and applying
// the same text-level handler conversion logic used by the MCP manifest_fix tool.
func applyMutatingPhase(manifestYAML string, registry *schema.Registry) string {
	if registry == nil {
		return manifestYAML
	}

	deprecatedHandlers := collectDeprecatedHandlersFromRegistry(registry)
	if len(deprecatedHandlers) == 0 {
		return manifestYAML
	}

	// Find and replace deprecated handler calls within Starlark script blocks.
	// YAML multi-line strings appear after "script: |" or "script: |-" markers.
	// We process the full YAML text line-by-line, isolating script blocks.
	lines := strings.Split(manifestYAML, "\n")
	result := make([]string, 0, len(lines))
	inScript := false
	var scriptIndent int
	var scriptLines []string
	var scriptStartIdx int

	for i, line := range lines {
		if !inScript {
			// Detect "script: |" or "script: |-" at any indentation level
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "script: |") {
				indent := len(line) - len(trimmed)
				inScript = true
				scriptIndent = indent
				scriptLines = nil
				scriptStartIdx = i
				result = append(result, line)
				continue
			}
			result = append(result, line)
			continue
		}

		// Inside a script block: collect lines until de-indented
		if line == "" {
			scriptLines = append(scriptLines, line)
			continue
		}

		currentIndent := len(line) - len(strings.TrimLeft(line, " \t"))
		if currentIndent <= scriptIndent && strings.TrimSpace(line) != "" {
			// End of script block — apply fixes to the accumulated script lines
			script := strings.Join(scriptLines, "\n")
			fixed := applyDeprecatedHandlerFixes(script, deprecatedHandlers)
			fixedLines := strings.Split(fixed, "\n")
			// Replace the script lines in result (they were already appended as empty during collection)
			// We need to backfill: scriptLines were NOT yet appended — we deferred them
			result = append(result, fixedLines...)
			inScript = false
			scriptLines = nil
			_ = scriptStartIdx
			result = append(result, line)
			continue
		}

		scriptLines = append(scriptLines, line)
	}

	// Flush remaining script lines if YAML ended while inside a script block
	if inScript && len(scriptLines) > 0 {
		script := strings.Join(scriptLines, "\n")
		fixed := applyDeprecatedHandlerFixes(script, deprecatedHandlers)
		fixedLines := strings.Split(fixed, "\n")
		result = append(result, fixedLines...)
	}

	return strings.Join(result, "\n")
}

// deprecatedHandlerInfo holds conversion info for a deprecated handler.
type deprecatedHandlerInfo struct {
	currentName string
	rule        *schema.ConversionRule
}

// collectDeprecatedHandlersFromRegistry builds a map of deprecated handler names.
func collectDeprecatedHandlersFromRegistry(registry *schema.Registry) map[string]deprecatedHandlerInfo {
	metadata := registry.BuildLinterMetadata()
	result := make(map[string]deprecatedHandlerInfo)
	for name, meta := range metadata {
		if meta.IsDeprecated && meta.ReplacedBy != "" {
			if mapping := registry.LookupDeprecated(name); mapping != nil {
				result[name] = deprecatedHandlerInfo{
					currentName: mapping.CurrentName,
					rule:        mapping.ConversionRule,
				}
			}
		}
	}
	return result
}

// applyDeprecatedHandlerFixes applies all deprecated handler conversions to a Starlark script string.
// It replaces deprecated handler call names with current names and renames parameters as needed.
func applyDeprecatedHandlerFixes(script string, deprecated map[string]deprecatedHandlerInfo) string {
	if len(deprecated) == 0 {
		return script
	}

	// Sort by name length (longest first) to avoid partial replacements
	sorted := make([]string, 0, len(deprecated))
	for name := range deprecated {
		sorted = append(sorted, name)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})

	result := script
	for _, oldName := range sorted {
		info := deprecated[oldName]
		result = replaceDeprecatedHandler(result, oldName, info)
	}
	return result
}

// replaceDeprecatedHandler replaces a single deprecated handler call in a Starlark script.
// It replaces the call name and renames keyword arguments using parameter mapping.
func replaceDeprecatedHandler(script string, oldName string, info deprecatedHandlerInfo) string {
	// Quick check: does the old name appear at all?
	if !strings.Contains(script, oldName) {
		return script
	}

	newCall := info.currentName + "("

	// Build reverse mapping: old param -> new param
	var reverseMapping map[string]string
	if info.rule != nil && len(info.rule.ParamMapping) > 0 {
		reverseMapping = make(map[string]string, len(info.rule.ParamMapping))
		for newParam, oldParam := range info.rule.ParamMapping {
			reverseMapping[oldParam] = newParam
		}
	}

	var result strings.Builder
	remaining := script
	for {
		// Find the next occurrence of oldName followed by optional whitespace and '('
		idx := findHandlerCall(remaining, oldName)
		if idx < 0 {
			result.WriteString(remaining)
			break
		}

		// Write text before the match
		result.WriteString(remaining[:idx])
		result.WriteString(newCall)

		// Advance past "oldName(" (find the '(' after the name)
		parenIdx := strings.Index(remaining[idx+len(oldName):], "(")
		remaining = remaining[idx+len(oldName)+parenIdx+1:]

		// Extract call body up to matching closing paren
		callBody, rest := splitAtMatchingParen(remaining)
		if len(reverseMapping) > 0 {
			callBody = renameKwargs(callBody, reverseMapping)
		}
		result.WriteString(callBody)
		remaining = rest
	}
	return result.String()
}

// findHandlerCall finds the index of oldName followed by optional whitespace and '(' in s.
// Returns -1 if not found. Only matches at word boundaries (preceded by non-ident or start of string).
func findHandlerCall(s, oldName string) int {
	idx := 0
	for {
		pos := strings.Index(s[idx:], oldName)
		if pos < 0 {
			return -1
		}
		pos += idx

		// Check word boundary before
		if pos > 0 && isScriptIdentChar(s[pos-1]) {
			idx = pos + 1
			continue
		}

		// Check that something follows: optional whitespace then '('
		rest := s[pos+len(oldName):]
		wsEnd := 0
		for wsEnd < len(rest) && (rest[wsEnd] == ' ' || rest[wsEnd] == '\t') {
			wsEnd++
		}
		if wsEnd >= len(rest) || rest[wsEnd] != '(' {
			idx = pos + 1
			continue
		}

		return pos
	}
}

func isScriptIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// splitAtMatchingParen splits s at the matching closing paren for an already-opened call.
// Returns (bodyIncludingCloseParen, rest).
func splitAtMatchingParen(s string) (string, string) {
	depth := 1
	i := 0
	for i < len(s) {
		switch s[i] {
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
			i++
		case '"', '\'':
			i = advancePastString(s, i)
		case '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		default:
			i++
		}
	}
	return s, ""
}

// advancePastString advances past a string literal starting at i.
func advancePastString(s string, i int) int {
	q := s[i]
	if i+2 < len(s) && s[i+1] == q && s[i+2] == q {
		// Triple-quoted
		triple := string([]byte{q, q, q})
		end := strings.Index(s[i+3:], triple)
		if end >= 0 {
			return i + 3 + end + 3
		}
		return len(s)
	}
	i++
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			return i + 1
		}
		i++
	}
	return len(s)
}

// renameKwargs renames keyword arguments in a call body string.
// Only renames top-level (depth=0) identifier=value patterns.
func renameKwargs(callBody string, reverseMapping map[string]string) string {
	var result strings.Builder
	i := 0
	for i < len(callBody) {
		i = renameKwargsStep(&result, callBody, i, reverseMapping)
	}
	return result.String()
}

// renameKwargsStep processes one token at position i and returns the next position.
func renameKwargsStep(result *strings.Builder, s string, i int, reverseMapping map[string]string) int {
	ch := s[i]
	if ch == '"' || ch == '\'' {
		start := i
		i = advancePastString(s, i)
		result.WriteString(s[start:i])
		return i
	}
	if ch == '#' {
		start := i
		for i < len(s) && s[i] != '\n' {
			i++
		}
		result.WriteString(s[start:i])
		return i
	}
	if isIdentStart(ch) {
		return renameKwargIdent(result, s, i, reverseMapping)
	}
	result.WriteByte(ch)
	return i + 1
}

// renameKwargIdent handles an identifier at position i, renaming it if it is a kwarg.
func renameKwargIdent(result *strings.Builder, s string, i int, reverseMapping map[string]string) int {
	start := i
	for i < len(s) && isScriptIdentChar(s[i]) {
		i++
	}
	ident := s[start:i]

	// Check for "ident=" (not "ident==")
	eqIdx := i
	for eqIdx < len(s) && (s[eqIdx] == ' ' || s[eqIdx] == '\t') {
		eqIdx++
	}
	isKwarg := eqIdx < len(s) && s[eqIdx] == '=' && (eqIdx+1 >= len(s) || s[eqIdx+1] != '=')
	if isKwarg {
		if newName, ok := reverseMapping[ident]; ok {
			result.WriteString(newName)
			return i
		}
	}
	result.WriteString(ident)
	return i
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// enrichErrors augments validation errors with additional context to help the LLM
// produce better fixes. When no schema registry is provided, errors are returned unchanged.
func enrichErrors(errs []ValidationError, registry *schema.Registry) []ValidationError {
	if len(errs) == 0 {
		return errs
	}

	enriched := make([]ValidationError, len(errs))
	copy(enriched, errs)

	for i := range enriched {
		switch enriched[i].Code {
		case "UNKNOWN_HANDLER":
			enrichUnknownHandler(&enriched[i], registry)
		case "UNKNOWN_EVENT_TOPIC":
			enrichUnknownEventTopic(&enriched[i])
		case "MISSING_REQUIRED_PARAM":
			enrichMissingRequiredParam(&enriched[i], registry)
		case "WRONG_PARAM_TYPE":
			enrichWrongParamType(&enriched[i], registry)
		}
	}

	return enriched
}

func enrichUnknownHandler(e *ValidationError, registry *schema.Registry) {
	if registry != nil {
		e.AvailableFields = registry.ListHandlers()
	}
}

func enrichUnknownEventTopic(e *ValidationError) {
	allTopics := topics.All()
	if e.Suggestion == "" {
		closest := findClosestTopicMatch(e.Message, allTopics)
		if closest != "" {
			e.Suggestion = closest
		}
	}
	if len(e.AvailableFields) == 0 {
		e.AvailableFields = allTopics
	}
}

func enrichMissingRequiredParam(e *ValidationError, registry *schema.Registry) {
	if registry == nil {
		return
	}
	handlerName := extractHandlerName(e.Path)
	if handlerName == "" {
		return
	}
	h, err := registry.GetHandler(handlerName)
	if err != nil {
		return
	}
	var required []string
	for paramName, field := range h.Params {
		if field.Required {
			required = append(required, fmt.Sprintf("%s (%s)", paramName, field.Type))
		}
	}
	sort.Strings(required)
	if len(required) > 0 {
		e.Message = fmt.Sprintf("%s. Required params: %s", e.Message, strings.Join(required, ", "))
	}
}

func enrichWrongParamType(e *ValidationError, registry *schema.Registry) {
	if registry == nil {
		return
	}
	handlerName := extractHandlerName(e.Path)
	paramName := extractParamName(e.Path)
	if handlerName == "" || paramName == "" {
		return
	}
	h, err := registry.GetHandler(handlerName)
	if err != nil {
		return
	}
	if field, ok := h.Params[paramName]; ok {
		e.Message = fmt.Sprintf("%s. Expected type: %s", e.Message, field.Type)
	}
}

// findClosestTopicMatch finds the most similar topic name to any word in the message.
// Returns empty string if no candidate is close enough.
func findClosestTopicMatch(message string, allTopics []string) string {
	if message == "" || len(allTopics) == 0 {
		return ""
	}

	// Try to find a word in the message that looks like a topic name (contains dots or underscores)
	words := strings.Fields(message)
	for _, word := range words {
		// Strip surrounding quotes and punctuation
		word = strings.Trim(word, `"'`+"`.,;:()")
		if strings.ContainsAny(word, "._") && len(word) > 3 {
			best := findClosestTopicString(word, allTopics)
			if best != "" {
				return best
			}
		}
	}
	return ""
}

// findClosestTopicString finds the closest topic name using Levenshtein distance.
func findClosestTopicString(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1

	for _, candidate := range candidates {
		dist := levenshteinDist(strings.ToLower(target), strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}
	return bestMatch
}

// levenshteinDist computes edit distance between two strings.
func levenshteinDist(a, b string) int {
	la := len(a)
	lb := len(b)

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := 0; i <= la; i++ {
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

// extractHandlerName extracts the handler name from an error path like
// "sagas[0].script:position_keeping.initiate_log" or "sagas[0]:position_keeping.initiate_log".
// Returns empty string when no handler name can be determined.
func extractHandlerName(path string) string {
	// Look for a pattern like "service_name.handler_name" in the path
	colon := strings.LastIndex(path, ":")
	if colon >= 0 && colon < len(path)-1 {
		candidate := path[colon+1:]
		if strings.Contains(candidate, ".") {
			return candidate
		}
	}
	// Fallback: find the last dotted segment if no colon delimiter
	parts := strings.Split(path, ".")
	if len(parts) >= 2 {
		// Return "service.method" from the last two path components
		last := parts[len(parts)-1]
		prev := parts[len(parts)-2]
		if !strings.Contains(prev, "[") {
			return prev + "." + last
		}
	}
	return ""
}

// extractParamName extracts the parameter name from an error path like
// "sagas[0]:position_keeping.initiate_log#amount".
// Returns empty string when no parameter name can be determined.
func extractParamName(path string) string {
	hash := strings.LastIndex(path, "#")
	if hash >= 0 && hash < len(path)-1 {
		return path[hash+1:]
	}
	return ""
}
