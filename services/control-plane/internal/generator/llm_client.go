// Package generator provides LLM-assisted manifest generation for the control plane.
// It uses Claude to generate and fix economy manifest YAML based on tenant context
// and validation feedback, enabling an AI-assisted configuration workflow.
package generator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Sentinel errors for empty LLM responses.
var (
	errGenerateEmptyResponse = errors.New("generate manifest: empty response from LLM")
	errFixEmptyResponse      = errors.New("fix manifest: empty response from LLM")
)

const (
	// DefaultModel is the default Claude model used for manifest generation.
	DefaultModel = "claude-opus-4-5"

	// defaultMaxTokens is the maximum tokens for a generation response.
	// Manifests can be large, so we set a generous limit.
	defaultMaxTokens = 8192

	// systemPrompt guides the LLM to produce valid manifest YAML.
	systemPrompt = `You are an expert at generating Meridian economy manifest YAML files.

Meridian manifests define the configuration for a tenant's economy, including:
- instruments: financial and non-financial assets (currencies, tokens, energy units, etc.)
- account_types: categories of accounts with associated instruments
- sagas: Starlark-based workflow definitions for business transactions
- mappings: CEL-based data transformation rules
- provider_connections: external service integrations
- instruction_routes: routing rules for transaction instructions

When generating a manifest:
1. Always produce valid YAML
2. Follow the Meridian manifest schema precisely
3. Use meaningful, descriptive names and codes
4. Include only what is necessary for the described use case
5. Ensure all cross-references (e.g., instrument codes in account types) are consistent

Return ONLY the manifest YAML, optionally wrapped in ` + "```yaml" + ` code fences. Do not include explanations or commentary outside the YAML.`
)

// ValidationError represents a single validation finding with structured
// location information and optional suggestions for AI feedback.
// This mirrors validator.ValidationError to avoid a circular import.
type ValidationError struct {
	// Code is a machine-readable error code (e.g., "CEL_TYPE_ERROR").
	Code string

	// Path is the location within the manifest (e.g., "instruments[0].code").
	Path string

	// Message is a human-readable description of the issue.
	Message string

	// Suggestion is a "Did you mean...?" hint for typos.
	Suggestion string

	// Available lists valid values when an unknown value is referenced.
	Available []string
}

// LLMClient abstracts LLM interactions for manifest generation.
type LLMClient interface {
	// Generate sends a prompt and returns the generated manifest YAML.
	Generate(ctx context.Context, prompt string) (string, error)

	// Fix sends a manifest with validation errors and returns a corrected manifest.
	Fix(ctx context.Context, manifest string, errors []ValidationError) (string, error)
}

// ClaudeLLMClient implements LLMClient using the Anthropic Messages API.
type ClaudeLLMClient struct {
	client *anthropic.Client
	model  string
}

// NewClaudeLLMClient creates a new ClaudeLLMClient with the given API key and model.
// If model is empty, DefaultModel is used.
func NewClaudeLLMClient(apiKey string, model string) *ClaudeLLMClient {
	if model == "" {
		model = DefaultModel
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &ClaudeLLMClient{
		client: &client,
		model:  model,
	}
}

// Generate sends a prompt to Claude and returns the generated manifest YAML.
// It strips markdown code fences from the response if present.
func (c *ClaudeLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	message, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: defaultMaxTokens,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("generate manifest: %w", err)
	}

	raw := extractText(message)
	if raw == "" {
		return "", errGenerateEmptyResponse
	}

	return extractYAML(raw), nil
}

// Fix sends the current manifest and its validation errors to Claude and returns
// a corrected manifest YAML.
func (c *ClaudeLLMClient) Fix(ctx context.Context, manifest string, errors []ValidationError) (string, error) {
	prompt := buildFixPrompt(manifest, errors)

	message, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: defaultMaxTokens,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("fix manifest: %w", err)
	}

	raw := extractText(message)
	if raw == "" {
		return "", errFixEmptyResponse
	}

	return extractYAML(raw), nil
}

// extractText pulls the text content from the first text block in a message response.
func extractText(msg *anthropic.Message) string {
	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// extractYAML finds YAML content between ```yaml and ``` markers in the given text.
// If no markers are found, the full text is returned trimmed of surrounding whitespace.
// If multiple code blocks are present, the first yaml-fenced block is returned.
func extractYAML(text string) string {
	const openFence = "```yaml"
	const closeFence = "```"

	start := strings.Index(text, openFence)
	if start == -1 {
		// No yaml fence — return trimmed full text.
		return strings.TrimSpace(text)
	}

	// Advance past the opening fence and any trailing newline.
	content := text[start+len(openFence):]
	if len(content) > 0 && content[0] == '\n' {
		content = content[1:]
	}

	end := strings.Index(content, closeFence)
	if end == -1 {
		// Unclosed fence — return everything after the opening fence.
		return strings.TrimSpace(content)
	}

	return strings.TrimSpace(content[:end])
}

// buildFixPrompt constructs the prompt sent to the LLM when asking it to fix
// a manifest that failed validation.
func buildFixPrompt(manifest string, errors []ValidationError) string {
	var b strings.Builder

	b.WriteString("The following Meridian manifest has validation errors that must be fixed.\n\n")
	b.WriteString("## Current Manifest\n\n")
	b.WriteString("```yaml\n")
	b.WriteString(manifest)
	if !strings.HasSuffix(manifest, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```\n\n")
	b.WriteString("## Validation Errors\n\n")

	for i, e := range errors {
		b.WriteString(fmt.Sprintf("%d. **[%s]** at `%s`\n", i+1, e.Code, e.Path))
		b.WriteString(fmt.Sprintf("   - Message: %s\n", e.Message))
		if e.Suggestion != "" {
			b.WriteString(fmt.Sprintf("   - Suggestion: %s\n", e.Suggestion))
		}
		if len(e.Available) > 0 {
			b.WriteString(fmt.Sprintf("   - Available values: %s\n", strings.Join(e.Available, ", ")))
		}
	}

	b.WriteString("\nPlease return a corrected manifest that resolves all of the above errors. ")
	b.WriteString("Return ONLY the fixed manifest YAML.")

	return b.String()
}
