package generator

// Export private helpers for black-box testing.

// ExtractYAML is the exported test hook for extractYAML.
var ExtractYAML = extractYAML

// BuildFixPrompt is the exported test hook for buildFixPrompt.
var BuildFixPrompt = buildFixPrompt

// Model returns the model name used by this client.
func (c *ClaudeLLMClient) Model() string {
	return c.model
}
