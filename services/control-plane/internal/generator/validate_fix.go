package generator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// Sentinel errors for ValidateAndFix option validation and loop guards.
var (
	errUnexpectedLoopExit        = errors.New("validate-fix: unexpected loop exit")
	errNegativeMaxIterations     = errors.New("validate-fix: MaxIterations must be >= 0")
	errValidatorRequired         = errors.New("validate-fix: validator is required")
	errLLMClientRequiredForFixes = errors.New("validate-fix: LLM client is required when MaxIterations > 0")
	errNilValidatorResult        = errors.New("validate-fix: nil result from validator")
)

// ManifestValidator abstracts the validation pipeline for the validate-fix loop.
// It performs a dry-run validation returning structured errors without persisting anything.
type ManifestValidator interface {
	// ValidateDryRun validates the manifest YAML and returns structured results.
	// No side effects are performed - this is a pure validation check.
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

	// SchemaRegistry is used for schema-backed enrichment and deprecated-handler rewrites.
	// May be nil - handler enrichment and mutating-phase rewrites are skipped when absent.
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
	if opts.MaxIterations < 0 {
		return nil, errNegativeMaxIterations
	}
	if opts.Validator == nil {
		return nil, errValidatorRequired
	}
	if opts.MaxIterations > 0 && opts.LLMClient == nil {
		return nil, errLLMClientRequiredForFixes
	}

	current := manifestYAML

	for iter := 0; iter <= opts.MaxIterations; iter++ {
		// Step 1: Apply mutating phase (auto-fix deprecated handlers)
		current = applyMutatingPhase(current, opts.SchemaRegistry)

		// Step 2: Validate
		result, err := opts.Validator.ValidateDryRun(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("validate manifest (iteration %d): %w", iter, err)
		}
		if result == nil {
			return nil, fmt.Errorf("validate manifest (iteration %d): %w", iter, errNilValidatorResult)
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

		// Step 4: Errors remain - if we've used all iterations, stop
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

	for _, line := range lines {
		if !inScript {
			// Detect "script: |" or "script: |-" at any indentation level
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "script: |") {
				indent := len(line) - len(trimmed)
				inScript = true
				scriptIndent = indent
				scriptLines = nil
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
			// End of script block - apply fixes to the accumulated script lines
			script := strings.Join(scriptLines, "\n")
			fixed := applyDeprecatedHandlerFixes(script, deprecatedHandlers)
			fixedLines := strings.Split(fixed, "\n")
			result = append(result, fixedLines...)
			inScript = false
			scriptLines = nil
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
		// Apply ConversionRule.Defaults: inject default values for any required args
		// that the old caller did not provide (e.g., a new param added in the replacement).
		if info.rule != nil && len(info.rule.Defaults) > 0 {
			callBody = injectMissingDefaults(callBody, info.rule.Defaults)
		}
		result.WriteString(callBody)
		remaining = rest
	}
	return result.String()
}
