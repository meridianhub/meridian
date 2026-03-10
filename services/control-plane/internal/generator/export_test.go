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

// NewEmptySchemaRegistry returns an empty schema registry for test use.
func NewEmptySchemaRegistry() *schema.Registry {
	return schema.NewRegistry()
}

// Model returns the model name used by this client.
func (c *ClaudeLLMClient) Model() string {
	return c.model
}
