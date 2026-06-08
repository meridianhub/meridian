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

// NewDeprecatedHandlerInfoWithRule creates a deprecatedHandlerInfo with a fully-specified
// ConversionRule (param mapping plus defaults) for tests.
func NewDeprecatedHandlerInfoWithRule(currentName string, paramMapping, defaults map[string]string) deprecatedHandlerInfo {
	return deprecatedHandlerInfo{
		currentName: currentName,
		rule:        &schema.ConversionRule{ParamMapping: paramMapping, Defaults: defaults},
	}
}

// CollectDeprecatedHandlersFromRegistry is the exported test hook for
// collectDeprecatedHandlersFromRegistry.
var CollectDeprecatedHandlersFromRegistry = collectDeprecatedHandlersFromRegistry

// DeprecatedHandlerCurrentName returns the current handler name recorded in a
// deprecatedHandlerInfo, for test assertions on the unexported field.
func DeprecatedHandlerCurrentName(info deprecatedHandlerInfo) string {
	return info.currentName
}

// SplitAtMatchingParen is the exported test hook for splitAtMatchingParen.
var SplitAtMatchingParen = splitAtMatchingParen

// AdvancePastString is the exported test hook for advancePastString.
var AdvancePastString = advancePastString

// RenameKwargs is the exported test hook for renameKwargs.
var RenameKwargs = renameKwargs

// FindUnquotedComment is the exported test hook for findUnquotedComment.
var FindUnquotedComment = findUnquotedComment

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

// SetBuildStatics replaces the buildStatics function on a CachedContextAssembler for testing.
// This allows tests to count how many times static sections are actually recomputed.
func (c *CachedContextAssembler) SetBuildStatics(fn func(registry *schema.Registry) staticComponents) {
	c.buildStatics = fn
}

// StaticComponents is the exported type for staticComponents.
type StaticComponents = staticComponents

// Model returns the model name used by this client.
func (c *ClaudeLLMClient) Model() string {
	return c.model
}

// YAMLToProtoManifest is the exported test hook for yamlToProtoManifest.
var YAMLToProtoManifest = yamlToProtoManifest

// ExtractManifestMetadata is the exported test hook for extractManifestMetadata.
var ExtractManifestMetadata = extractManifestMetadata

// PatternNames is the exported test hook for patternNames.
var PatternNames = patternNames

// ToProtoValidationErrors is the exported test hook for toProtoValidationErrors.
var ToProtoValidationErrors = toProtoValidationErrors

// ConvertYAMLToJSONCompatible is the exported test hook for convertYAMLToJSONCompatible.
var ConvertYAMLToJSONCompatible = convertYAMLToJSONCompatible

// ApplyAmendImpact is the exported test hook for applyAmendImpact.
var ApplyAmendImpactFn = applyAmendImpact

// FindClosestTopicMatch is the exported test hook for findClosestTopicMatch.
var FindClosestTopicMatch = findClosestTopicMatch

// LevenshteinDist is the exported test hook for levenshteinDist.
var LevenshteinDist = levenshteinDist

// ExtractParamName is the exported test hook for extractParamName.
var ExtractParamName = extractParamName
