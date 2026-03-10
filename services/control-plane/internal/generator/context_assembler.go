package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/protobuf/encoding/protojson"
)

// ErrBlankDescription is returned when AssembleContext is called with an empty description.
var ErrBlankDescription = errors.New("description is required")

// ErrMissingCurrentManifest is returned when IncludeCurrentEconomy is true but CurrentManifest is nil.
var ErrMissingCurrentManifest = errors.New("CurrentManifest is required when IncludeCurrentEconomy is true")

// ErrMissingRegistry is returned when AssembleContext is called with a nil schema registry.
var ErrMissingRegistry = errors.New("registry is required")

// ContextAssemblerOptions configures the context assembly process.
type ContextAssemblerOptions struct {
	// Description is the tenant's business description (required).
	Description string
	// Industry is an optional industry hint used to improve pattern matching.
	Industry string
	// IncludePatterns controls whether cookbook patterns are matched and included.
	// Defaults to true in the constructor helper.
	IncludePatterns bool
	// MaxPatterns is the maximum number of patterns to include. Defaults to 3.
	MaxPatterns int
	// IncludeCurrentEconomy controls whether the current manifest is serialized and included.
	// Used in amend mode.
	IncludeCurrentEconomy bool
	// CurrentManifest is the current tenant manifest. Required when IncludeCurrentEconomy is true.
	CurrentManifest *controlplanev1.Manifest
	// RelationshipGraph is the economy graph JSON summary (optional, included if non-empty).
	RelationshipGraph string
}

// AssembledContext holds the fully assembled generation prompt and metadata.
type AssembledContext struct {
	// Prompt is the complete LLM prompt ready for submission.
	Prompt string
	// MatchedPatterns are the cookbook patterns selected during assembly.
	MatchedPatterns []PatternMatch
	// TokenEstimate is a rough estimate of prompt token count (words * 1.3).
	TokenEstimate int
	// PatternMatchError is non-nil when pattern matching failed. Generation
	// proceeds without patterns but callers can inspect or log this error.
	PatternMatchError error
}

// AssembleContext combines handler reference, topic list, schema summary, and cookbook
// patterns into a complete LLM generation prompt. Pattern matching failures are non-fatal
// and result in an empty pattern list rather than an error.
func AssembleContext(opts ContextAssemblerOptions, registry *schema.Registry, cookbookFS fs.FS) (*AssembledContext, error) {
	// Validate required fields.
	if strings.TrimSpace(opts.Description) == "" {
		return nil, ErrBlankDescription
	}
	if registry == nil {
		return nil, ErrMissingRegistry
	}
	if opts.IncludeCurrentEconomy && opts.CurrentManifest == nil {
		return nil, ErrMissingCurrentManifest
	}

	// Apply defaults.
	if opts.MaxPatterns <= 0 {
		opts.MaxPatterns = 3
	}

	// Build static sections from registry and schema.
	handlerRef := BuildHandlerReferenceCard(registry)
	topicList := BuildTopicList()
	schemaSummary := BuildManifestSchemaSummary()

	// Match patterns. Failure is non-fatal — generation can proceed without patterns.
	var matched []PatternMatch
	var patternMatchErr error
	if opts.IncludePatterns && cookbookFS != nil {
		matched, patternMatchErr = MatchPatterns(cookbookFS, opts.Description, opts.Industry, opts.MaxPatterns)
		if patternMatchErr != nil {
			matched = nil
		}
	}

	// Serialize current manifest to JSON (amend mode).
	currentEconomyJSON := ""
	if opts.IncludeCurrentEconomy && opts.CurrentManifest != nil {
		marshaler := protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: false}
		data, err := marshaler.Marshal(opts.CurrentManifest)
		if err != nil {
			return nil, fmt.Errorf("serialize current manifest: %w", err)
		}
		currentEconomyJSON = string(data)
	}

	prompt := buildPrompt(opts, handlerRef, topicList, schemaSummary, matched, currentEconomyJSON)

	return &AssembledContext{
		Prompt:            prompt,
		MatchedPatterns:   matched,
		TokenEstimate:     estimateTokens(prompt),
		PatternMatchError: patternMatchErr,
	}, nil
}

// buildPrompt assembles the complete LLM prompt from the provided context components.
func buildPrompt(
	opts ContextAssemblerOptions,
	handlerRef, topicList, schemaSummary string,
	patterns []PatternMatch,
	currentEconomyJSON string,
) string {
	var sb strings.Builder

	sb.WriteString("You are generating a Meridian economy manifest.\n\n")

	// Business Description
	sb.WriteString("## Business Description\n\n")
	sb.WriteString(opts.Description)
	sb.WriteString("\n\n")

	// Manifest Schema Reference
	sb.WriteString(schemaSummary)
	sb.WriteString("\n")

	// Available Handlers
	sb.WriteString(handlerRef)
	sb.WriteString("\n")

	// Available Event Topics
	sb.WriteString(topicList)

	writePatternsSection(&sb, patterns)
	writeCurrentEconomySection(&sb, currentEconomyJSON)
	writeRelationshipGraphSection(&sb, opts.RelationshipGraph)
	writeInstructionsSection(&sb, patterns, currentEconomyJSON)

	return sb.String()
}

// writePatternsSection writes the Relevant Patterns section to sb, or nothing if empty.
func writePatternsSection(sb *strings.Builder, patterns []PatternMatch) {
	if len(patterns) == 0 {
		return
	}
	sb.WriteString("## Relevant Patterns (copy and adapt)\n\n")
	for _, p := range patterns {
		fmt.Fprintf(sb, "### Pattern: %s\n\n", p.Title)
		if p.ManifestFragment != "" {
			sb.WriteString("**Manifest Fragment:**\n\n```yaml\n")
			sb.WriteString(p.ManifestFragment)
			if !strings.HasSuffix(p.ManifestFragment, "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("```\n\n")
		}
		if p.SagaScript != "" {
			sb.WriteString("**Saga Script:**\n\n```python\n")
			sb.WriteString(p.SagaScript)
			if !strings.HasSuffix(p.SagaScript, "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("```\n\n")
		}
	}
}

// writeCurrentEconomySection writes the Current Economy section to sb, or nothing if empty.
func writeCurrentEconomySection(sb *strings.Builder, currentEconomyJSON string) {
	if currentEconomyJSON == "" {
		return
	}
	sb.WriteString("## Current Economy\n\n")
	sb.WriteString("The following is the tenant's existing manifest. Amend it according to the business description above.\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(currentEconomyJSON)
	sb.WriteString("\n```\n\n")
}

// writeRelationshipGraphSection writes the Economy Relationship Graph section to sb, or nothing if empty.
func writeRelationshipGraphSection(sb *strings.Builder, graph string) {
	if graph == "" {
		return
	}
	sb.WriteString("## Economy Relationship Graph\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(graph)
	sb.WriteString("\n```\n\n")
}

// writeInstructionsSection writes the Instructions section to sb.
func writeInstructionsSection(sb *strings.Builder, patterns []PatternMatch, currentEconomyJSON string) {
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Generate a complete Meridian manifest YAML that implements the business description above.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Use only handlers listed in the Handler Reference Card above.\n")
	sb.WriteString("- Use only event topic names listed in the Available Event Topics section for saga triggers.\n")
	sb.WriteString("- All saga scripts must be valid Starlark (no while loops, no recursion).\n")
	sb.WriteString("- Follow the Manifest Schema Reference for field names and types.\n")
	if len(patterns) > 0 {
		sb.WriteString("- Copy and adapt the Relevant Patterns where applicable.\n")
	}
	if currentEconomyJSON != "" {
		sb.WriteString("- Preserve existing instruments, account types, and sagas from the Current Economy unless explicitly asked to change them.\n")
	}
	sb.WriteString("- Output only the YAML manifest. Do not include explanations or markdown fencing.\n")
}

// estimateTokens returns a rough token estimate for the prompt.
// Token count is approximated as word count * 1.3.
func estimateTokens(prompt string) int {
	words := len(strings.Fields(prompt))
	return int(float64(words) * 1.3)
}
