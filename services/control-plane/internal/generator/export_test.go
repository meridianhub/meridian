package generator

import "github.com/meridianhub/meridian/shared/pkg/saga/schema"

// Export private helpers for black-box testing.

// ExtractYAML is the exported test hook for extractYAML.
var ExtractYAML = extractYAML

// BuildFixPrompt is the exported test hook for buildFixPrompt.
var BuildFixPrompt = buildFixPrompt

// EnrichErrors is the exported test hook for enrichErrors.
var EnrichErrors = enrichErrors

// ApplyMutatingPhase is the exported test hook for applyMutatingPhase.
var ApplyMutatingPhase = applyMutatingPhase

// ReplaceDeprecatedHandler is the exported test hook for replaceDeprecatedHandler.
var ReplaceDeprecatedHandler = replaceDeprecatedHandler

// DeprecatedHandlerInfo is the exported type for deprecatedHandlerInfo.
type DeprecatedHandlerInfo = deprecatedHandlerInfo

// NewDeprecatedHandlerInfo creates a deprecatedHandlerInfo for tests.
func NewDeprecatedHandlerInfo(currentName string) deprecatedHandlerInfo {
	return deprecatedHandlerInfo{currentName: currentName}
}

// NewDeprecatedHandlerInfoWithDefaults creates a deprecatedHandlerInfo with a ConversionRule for tests.
func NewDeprecatedHandlerInfoWithDefaults(currentName string, defaults map[string]string) deprecatedHandlerInfo {
	return deprecatedHandlerInfo{
		currentName: currentName,
		rule:        &schema.ConversionRule{Defaults: defaults},
	}
}

// FindHandlerCall is the exported test hook for findHandlerCall.
var FindHandlerCall = findHandlerCall

// InjectMissingDefaults is the exported test hook for injectMissingDefaults.
var InjectMissingDefaults = injectMissingDefaults

// ExtractHandlerName is the exported test hook for extractHandlerName.
var ExtractHandlerName = extractHandlerName

// NewEmptySchemaRegistry returns an empty schema registry for test use.
func NewEmptySchemaRegistry() *schema.Registry {
	return schema.NewRegistry()
}

// Model returns the model name used by this client.
func (c *ClaudeLLMClient) Model() string {
	return c.model
}

// YAMLToProtoManifest is the exported test hook for yamlToProtoManifest.
var YAMLToProtoManifest = yamlToProtoManifest
