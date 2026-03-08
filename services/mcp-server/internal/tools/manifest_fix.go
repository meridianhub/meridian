// Package tools provides tool handlers for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// RegisterManifestFixTool registers the meridian_manifest_fix tool into the provided Registry.
func RegisterManifestFixTool(r *Registry, schemaRegistry *schema.Registry) {
	tool := buildManifestFixTool(schemaRegistry)
	if err := r.Register(tool); err != nil {
		panic(fmt.Sprintf("failed to register manifest fix tool: %v", err))
	}
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
		return map[string]interface{}{ //nolint:nilerr // unmarshal error is surfaced in the tool response
			"error": fmt.Sprintf("invalid manifest JSON: %v", err),
		}, nil
	}

	conversions, err := fixManifestSagas(manifest, schemaRegistry)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // fix error is surfaced in the tool response
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
		oldCall := oldName + "("
		if !strings.Contains(result, oldCall) {
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
func applyHandlerConversion(script string, oldName string, info deprecatedInfo) string {
	result := strings.ReplaceAll(script, oldName+"(", info.currentName+"(")
	if info.rule != nil {
		for newParam, oldParam := range info.rule.ParamMapping {
			result = strings.ReplaceAll(result, oldParam+"=", newParam+"=")
		}
	}
	return result
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
