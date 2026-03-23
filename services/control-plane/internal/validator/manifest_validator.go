// Package validator provides manifest validation for the control plane.
// It performs structural schema validation, CEL type-checking for policy
// expressions, Starlark compilation for saga scripts, cross-reference
// validation, and immutability checks. All errors are structured with
// location paths and suggestions for AI-friendly feedback loops.
package validator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// Severity indicates the severity of a validation finding.
type Severity string

const (
	// SeverityError blocks manifest activation.
	SeverityError Severity = "error"
	// SeverityWarning allows activation but should be reviewed.
	SeverityWarning Severity = "warning"
)

// ValidationError represents a single validation finding with structured
// location information and optional suggestions for AI feedback.
type ValidationError struct {
	// Severity indicates whether this blocks activation.
	Severity Severity `json:"severity"`

	// Path is the location within the manifest (e.g., "instruments[0].code").
	Path string `json:"path"`

	// Code is a machine-readable error code (e.g., "CEL_TYPE_ERROR").
	Code string `json:"code"`

	// Message is a human-readable description of the issue.
	Message string `json:"message"`

	// Line is the source line number (for script errors).
	Line int `json:"line,omitempty"`

	// Column is the source column number (for script errors).
	Column int `json:"column,omitempty"`

	// Suggestion is a "Did you mean...?" hint for typos.
	Suggestion string `json:"suggestion,omitempty"`

	// AvailableFields lists valid field names when an unknown field is referenced.
	AvailableFields []string `json:"available_fields,omitempty"`

	// ResourceType is the manifest section type where the error occurred
	// (e.g., "instrument", "account_type", "saga", "valuation_rule").
	ResourceType string `json:"resource_type,omitempty"`

	// ResourceID is the identifier of the specific resource that caused the error
	// (e.g., the instrument code, account type code, or saga name).
	ResourceID string `json:"resource_id,omitempty"`
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	msg := fmt.Sprintf("[%s] %s: %s", e.Severity, e.Path, e.Message)
	if e.Suggestion != "" {
		msg += fmt.Sprintf(" (suggestion: %s)", e.Suggestion)
	}
	return msg
}

// ValidationResult contains the outcome of manifest validation.
type ValidationResult struct {
	// Valid is true when there are no errors (warnings are allowed).
	Valid bool `json:"valid"`

	// Errors contains all error-severity findings.
	Errors []ValidationError `json:"errors"`

	// Warnings contains all warning-severity findings.
	Warnings []ValidationError `json:"warnings"`

	// Graph contains the relationship graph extracted during validation.
	// Only populated when validation succeeds (no errors).
	Graph *RelationshipGraph `json:"graph,omitempty"`
}

// ManifestValidator validates Meridian manifests.
type ManifestValidator struct {
	protoValidator       protovalidate.Validator
	celEnv               *cel.Env
	bucketCelEnv         *cel.Env
	partyTypeCelEnv      *cel.Env
	mappingCelEnv        *cel.Env
	eventFilterEnv       *cel.Env
	channelRegistry      map[string]bool
	schemaRegistry       *schema.Registry
	apiPathRegistry      map[string]bool            // valid API endpoint paths from OpenAPI spec
	asyncAPISchemas      map[string]map[string]bool // topic -> set of payload field names
	apiPathsExplicit     bool                       // true when set via option
	asyncSchemasExplicit bool                       // true when set via option
}

// Option configures a ManifestValidator.
type Option func(*ManifestValidator)

// WithOpenAPIPaths sets the valid API paths for API trigger validation.
// Pass nil to disable API trigger validation (skip filesystem loading).
func WithOpenAPIPaths(paths map[string]bool) Option {
	return func(v *ManifestValidator) {
		v.apiPathRegistry = paths
		v.apiPathsExplicit = true
	}
}

// WithSchemaRegistry injects a pre-built schema registry for Starlark handler validation.
// When not set, the validator defaults to an empty registry: Starlark scripts still compile
// but handler calls (e.g. position_keeping.initiate_log) are treated as undefined names
// rather than being validated against typed service modules. Use WithDerivedSchema for
// production to enable typed parameter validation.
func WithSchemaRegistry(reg *schema.Registry) Option {
	return func(v *ManifestValidator) {
		if reg == nil {
			return // keep default empty registry
		}
		v.schemaRegistry = reg
	}
}

// WithDerivedSchema populates the validator's schema registry from a proto-derived Schema.
// This is the preferred option for production use where handlers are registered with
// proto type annotations.
func WithDerivedSchema(derivedSchema *schema.Schema) Option {
	return func(v *ManifestValidator) {
		v.schemaRegistry = schema.NewRegistryFromSchema(derivedSchema)
	}
}

// WithAsyncAPISchemas sets the event payload schemas for CEL field validation.
// The map keys are topic names; values are sets of valid field names.
// Pass nil to disable AsyncAPI field validation (skip filesystem loading).
func WithAsyncAPISchemas(schemas map[string]map[string]bool) Option {
	return func(v *ManifestValidator) {
		v.asyncAPISchemas = schemas
		v.asyncSchemasExplicit = true
	}
}

// New creates a new ManifestValidator.
func New(opts ...Option) (*ManifestValidator, error) {
	pv, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create proto validator: %w", err)
	}

	// CEL environment for account type validation policies.
	// These expressions operate on balance state.
	// Use DynType for numeric fields to allow both int and double literals
	// (e.g., "amount > 0" and "amount > 0.0" both work).
	celEnv, err := cel.NewEnv(
		cel.Variable("quantity", cel.DynType),
		cel.Variable("instrument", cel.StringType),
		cel.Variable("bucket_id", cel.StringType),
		cel.Variable("as_of", cel.TimestampType),
		cel.Variable("amount", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	// CEL environment for bucketing expressions.
	bucketCelEnv, err := cel.NewEnv(
		cel.Variable("instrument_code", cel.StringType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket CEL environment: %w", err)
	}

	// CEL environment for party type validation/eligibility expressions.
	// These expressions operate on party attributes.
	partyTypeCelEnv, err := cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("party_type", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create party type CEL environment: %w", err)
	}

	// CEL environment for mapping validation/transformation expressions.
	// These expressions operate on arbitrary payload fields.
	mappingCelEnv, err := cel.NewEnv(
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("value", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping CEL environment: %w", err)
	}

	// CEL environment for event trigger filter expressions.
	// Filters operate on a dynamic event map representing the domain event payload.
	eventFilterEnv, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create event filter CEL environment: %w", err)
	}

	// Build channel registry from the canonical topic list.
	allTopics := topics.All()
	channelRegistry := make(map[string]bool, len(allTopics))
	for _, topic := range allTopics {
		channelRegistry[topic] = true
	}

	mv := &ManifestValidator{
		protoValidator:  pv,
		celEnv:          celEnv,
		bucketCelEnv:    bucketCelEnv,
		partyTypeCelEnv: partyTypeCelEnv,
		mappingCelEnv:   mappingCelEnv,
		eventFilterEnv:  eventFilterEnv,
		channelRegistry: channelRegistry,
		schemaRegistry:  schema.NewRegistry(),
	}

	for _, opt := range opts {
		opt(mv)
	}

	// Best-effort auto-loading from repo checkout. When the spec files are not
	// reachable (e.g. running outside a repo checkout), the corresponding checks
	// degrade gracefully: format and duplicate checks still run, only the
	// endpoint-existence and field-reference checks are skipped. For deterministic
	// behavior in tests, use WithOpenAPIPaths / WithAsyncAPISchemas.
	if !mv.apiPathsExplicit {
		mv.apiPathRegistry = tryLoadOpenAPIPaths()
	}
	if !mv.asyncSchemasExplicit {
		mv.asyncAPISchemas = tryLoadAsyncAPISchemas()
	}

	return mv, nil
}

// ValidateOption configures validation behavior.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	forceDestructiveChanges bool
	skipImmutabilityChecks  bool
}

// WithForceDestructiveChanges converts destructive change errors into warnings,
// allowing removal of in-use resources when explicitly requested.
func WithForceDestructiveChanges() ValidateOption {
	return func(c *validateConfig) {
		c.forceDestructiveChanges = true
	}
}

// WithSkipImmutabilityChecks skips immutability enforcement and destructive
// change detection. Use when validating a manifest intended for a new tenant
// (create mode) that has no existing state to compare against.
func WithSkipImmutabilityChecks() ValidateOption {
	return func(c *validateConfig) {
		c.skipImmutabilityChecks = true
	}
}

// Validate performs full validation of a manifest.
// If previousManifest is non-nil, immutability and destructive change checks are also performed.
func (v *ManifestValidator) Validate(
	manifest *controlplanev1.Manifest,
	previousManifest *controlplanev1.Manifest,
	opts ...ValidateOption,
) *ValidationResult {
	cfg := &validateConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	result := &ValidationResult{Valid: true}

	// 1. Structural validation (protobuf constraints)
	v.validateStructural(manifest, result)

	// 2. Duplicate code detection
	v.validateDuplicates(manifest, result)

	// 3. CEL expression validation
	v.validateCELExpressions(manifest, result)

	// 4. Starlark script compilation
	callLogs := v.validateStarlarkScripts(manifest, result)

	// 5. Cross-reference validation
	v.validateCrossReferences(manifest, result)

	// 6. Payment rails validation
	v.validatePaymentRails(manifest, result)

	// 7. Party types validation
	v.validatePartyTypes(manifest, result)

	// 8. Mappings validation
	v.validateMappings(manifest, result)

	// 9. Event trigger channel and filter validation
	v.validateEventTriggers(manifest, result)

	// 10. Webhook trigger validation against provider connections
	v.validateWebhookTriggers(manifest, result)

	// 11. Scheduled trigger name uniqueness
	v.validateScheduledTriggers(manifest, result)

	// 12. API trigger validation against OpenAPI spec
	v.validateAPITriggers(manifest, result)

	// 13. Operational gateway orphan detection
	v.validateOperationalGatewayOrphans(manifest, result)

	// 14. Semantic validations
	v.validateSettlementCompleteness(manifest, result)
	v.validateSagaHandlerCompleteness(manifest, result)
	v.validateValuationRuleCycles(manifest, result)
	v.validateInstrumentAccountTypeConsistency(manifest, result)
	v.validateOrphanedInstruments(manifest, result)

	// 15. Immutability checks (skipped when validating for a new tenant)
	if previousManifest != nil && !cfg.skipImmutabilityChecks {
		v.validateImmutability(manifest, previousManifest, result)
	}

	// 16. Destructive change detection (skipped when validating for a new tenant)
	// Use the previous manifest's call logs for graph construction so that
	// dependencies that existed in the previous version are correctly detected,
	// even if a saga was modified or removed in the current manifest.
	if previousManifest != nil && !cfg.skipImmutabilityChecks {
		previousCallLogs := v.validateStarlarkScripts(previousManifest, &ValidationResult{})
		v.validateDestructiveChanges(manifest, previousManifest, previousCallLogs, cfg, result)
	}

	// Set valid flag based on error count
	result.Valid = len(result.Errors) == 0

	// Extract relationship graph when validation succeeds
	if result.Valid {
		result.Graph = ExtractRelationshipGraph(manifest, callLogs)
	}

	return result
}

// validateStructural uses protovalidate to check protobuf constraints.
func (v *ManifestValidator) validateStructural(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	err := v.protoValidator.Validate(manifest)
	if err == nil {
		return
	}

	var valErr *protovalidate.ValidationError
	if errors.As(err, &valErr) {
		for _, violation := range valErr.Violations {
			path := buildFieldPath(violation)
			message := ""
			if violation.Proto != nil {
				message = violation.Proto.GetMessage()
			}
			if message == "" {
				message = violation.String()
			}
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     path,
				Code:     "PROTO_VALIDATION",
				Message:  message,
			})
		}
	} else {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     "",
			Code:     "PROTO_VALIDATION",
			Message:  err.Error(),
		})
	}
}

// ─── Shared Helpers ─────────────────────────────────────────────────────────

// findRepoFile walks up from the current working directory to find a relative file path.
// Returns the absolute path if found, empty string otherwise.
func findRepoFile(relPath string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// buildFieldPath extracts a dotted field path from a protovalidate Violation.
func buildFieldPath(violation *protovalidate.Violation) string {
	if violation.Proto == nil || violation.Proto.GetField() == nil {
		return ""
	}
	elements := violation.Proto.GetField().GetElements()
	if len(elements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(elements))
	for _, elem := range elements {
		parts = append(parts, elem.GetFieldName())
	}
	return strings.Join(parts, ".")
}

// addError appends a validation error to the result in the appropriate list.
func addError(result *ValidationResult, ve ValidationError) {
	if ve.Severity == SeverityWarning {
		result.Warnings = append(result.Warnings, ve)
	} else {
		result.Errors = append(result.Errors, ve)
	}
}

// extractUndeclaredReference extracts the field name from a CEL undeclared reference error.
// Example: "undeclared reference to 'quanity'" -> "quanity"
func extractUndeclaredReference(errMsg string) string {
	const prefix = "undeclared reference to '"
	idx := strings.Index(errMsg, prefix)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(prefix):]
	endIdx := strings.Index(rest, "'")
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// extractUndefinedStarlarkName extracts the name from a Starlark "undefined: X" error.
func extractUndefinedStarlarkName(errMsg string) string {
	const marker = "undefined: "
	idx := strings.Index(errMsg, marker)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(marker):]
	// The name goes until the end of the line or end of string
	endIdx := strings.IndexAny(rest, " \n\t")
	if endIdx < 0 {
		return rest
	}
	return rest[:endIdx]
}

// extractWebhookSource extracts the provider connection source from a webhook trigger string.
// Returns empty string for non-webhook triggers.
func extractWebhookSource(trigger string) string {
	if !strings.HasPrefix(trigger, "webhook:") {
		return ""
	}
	remainder := strings.TrimPrefix(trigger, "webhook:")
	if dotIdx := strings.Index(remainder, "."); dotIdx > 0 {
		return remainder[:dotIdx]
	}
	return remainder
}

// findClosestMatch finds the closest string in candidates to the target using
// Levenshtein distance. Returns empty string if no candidate is close enough.
func findClosestMatch(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1 // Threshold: must be within half the target length

	for _, candidate := range candidates {
		dist := levenshteinDistance(strings.ToLower(target), strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}

	return bestMatch
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
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
			curr[i] = del
			if ins < curr[i] {
				curr[i] = ins
			}
			if sub < curr[i] {
				curr[i] = sub
			}
		}
		prev, curr = curr, prev
	}

	return prev[la]
}

// mapKeys returns the sorted keys of a map[string]bool.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
