package saga

import (
	"go.starlark.net/syntax"
	"google.golang.org/protobuf/proto"
)

// LintIssueType categorizes the kind of lint issue detected.
type LintIssueType string

const (
	// LintIssueTypeDecimalArithmetic indicates Decimal math that should be in CEL.
	LintIssueTypeDecimalArithmetic LintIssueType = "DECIMAL_ARITHMETIC"

	// LintIssueTypeMagicNumber indicates a hardcoded numeric literal.
	LintIssueTypeMagicNumber LintIssueType = "MAGIC_NUMBER"

	// LintIssueTypeNestedConditional indicates excessive if/else nesting.
	LintIssueTypeNestedConditional LintIssueType = "NESTED_CONDITIONAL"

	// LintIssueTypeHardcodedCode indicates a hardcoded instrument/account code.
	LintIssueTypeHardcodedCode LintIssueType = "HARDCODED_CODE"

	// LintIssueTypeMissingPreCheck indicates an external step without verify_external_state.
	LintIssueTypeMissingPreCheck LintIssueType = "MISSING_PRE_CHECK"

	// LintIssueTypeMissingCompensationStrategy indicates a handler call without declared compensation handling.
	LintIssueTypeMissingCompensationStrategy LintIssueType = "MISSING_COMPENSATION_STRATEGY"
)

// LintSeverity indicates how severe the lint issue is.
type LintSeverity string

const (
	// LintSeverityWarning allows save/activation but logs a warning.
	LintSeverityWarning LintSeverity = "WARNING"

	// LintSeverityError blocks activation.
	LintSeverityError LintSeverity = "ERROR"
)

// EnforcementLevel configures how strictly a rule is enforced.
type EnforcementLevel string

const (
	// EnforcementLevelWarning logs warnings but allows activation.
	EnforcementLevelWarning EnforcementLevel = "WARNING"

	// EnforcementLevelError blocks activation.
	EnforcementLevelError EnforcementLevel = "ERROR"
)

// LintIssue represents a single lint finding.
type LintIssue struct {
	Type         LintIssueType
	Severity     LintSeverity
	LineNumber   int
	Message      string
	SuggestedFix string
}

// HandlerCategory categorizes handlers by their operational role for conservation rule enforcement.
type HandlerCategory string

const (
	// HandlerCategoryIngestion represents handlers that ingest external data (e.g., meter readings, trades).
	// These handlers typically produce Physics instruments (KWH, GAS) from external sources.
	HandlerCategoryIngestion HandlerCategory = "ingestion"

	// HandlerCategorySettlement represents handlers that settle positions and create financial postings.
	// Conservation Rule: Settlement sagas triggered by Physics instruments cannot produce Physics instruments.
	HandlerCategorySettlement HandlerCategory = "settlement"

	// HandlerCategoryValuation represents handlers that perform valuation calculations.
	// These handlers typically convert between instruments using market rates.
	HandlerCategoryValuation HandlerCategory = "valuation"
)

// OverrideFieldType is a string alias for schema.FieldType, used to avoid circular imports
// between saga and saga/schema packages. Values should match schema.FieldType constants
// (e.g., "string", "Decimal", "uuid", "int64", "enum", etc.).
type OverrideFieldType = string

// ParamOverride defines Starlark-specific behavior overrides for a handler parameter.
// These allow handler authors to customize how proto-derived parameters appear in Starlark.
type ParamOverride struct {
	// Type overrides the proto-derived type (e.g., "Decimal" for string fields
	// that actually represent decimal values in Starlark). Use schema.FieldType values.
	Type OverrideFieldType

	// Alias provides an alternative name for the parameter in Starlark scripts.
	Alias string

	// Derived indicates the parameter is computed and should not appear in Starlark input.
	Derived bool

	// Deprecated marks the parameter as deprecated with a migration message.
	Deprecated string

	// Required overrides the proto-derived requiredness. Nil means use proto default.
	Required *bool
}

// HandlerConversion defines how to migrate from an older handler version or name.
type HandlerConversion struct {
	// FromVersion is the version number being migrated from.
	FromVersion int

	// FromName is the previous handler name (for handler renames).
	FromName string

	// ParamMapping maps old parameter names to new parameter names.
	ParamMapping map[string]string

	// Defaults provides default values for new parameters not present in old versions.
	Defaults map[string]string

	// Sunset is an ISO date string after which the old version is no longer accepted.
	Sunset string
}

// HandlerMetadata describes a step handler's characteristics.
type HandlerMetadata struct {
	// IsExternal indicates the handler calls external systems (non-idempotent).
	IsExternal bool

	// RequiresPreCheck indicates verify_external_state must be called before this handler.
	RequiresPreCheck bool

	// ResourceType identifies the RBAC resource type this handler operates on
	// (e.g., "payment_order", "position", "account"). When set, the saga runtime
	// checks that the executing user has RequiredPermission on this resource type
	// before allowing invocation. Empty means no authorization check (backward compat).
	ResourceType string

	// RequiredPermission is the RBAC permission required to invoke this handler
	// (e.g., "write", "read", "execute"). Only checked when ResourceType is non-empty.
	RequiredPermission string

	// Category indicates the handler's operational role (ingestion, settlement, valuation).
	// Used for conservation rule enforcement (FR-Conservation).
	Category HandlerCategory

	// ProducesInstruments lists the instrument codes this handler can create/produce.
	// Example: ["KWH", "GAS"] for meter reading ingestion, ["USD", "NZD"] for financial settlement.
	// Empty means the handler doesn't produce instruments.
	// Used for conservation rule enforcement to prevent causation loops.
	ProducesInstruments []string

	// CompensationStrategy indicates how compensation is handled ("auto", "saga_managed", "none", or "").
	CompensationStrategy string

	// HasAutoCompensation indicates the handler has a compensate: field.
	HasAutoCompensation bool

	// Compensate is the name of the compensation handler (from handler schema).
	Compensate string

	// ProtoRequestType is a nil instance of the handler's proto request message for reflection.
	ProtoRequestType proto.Message

	// ProtoResponseType is a nil instance of the handler's proto response message for reflection.
	ProtoResponseType proto.Message

	// Description is a human-readable description of the handler's purpose.
	Description string

	// ParamOverrides customizes Starlark-specific behavior for individual parameters.
	// Keys are parameter names as they appear in the proto message.
	ParamOverrides map[string]ParamOverride

	// Version is the handler's schema version for evolution tracking.
	Version int

	// Conversions defines migration rules from older handler versions or names.
	Conversions []HandlerConversion

	// DeprecatedMessage, when non-empty, indicates this handler is deprecated.
	// The value provides a migration message (e.g., "use handler_v2 instead").
	// Empty string means the handler is not deprecated.
	DeprecatedMessage string

	// RetryPolicy overrides the global SAGA_RETRY_BASE_DELAY / SAGA_RETRY_MAX_DELAY
	// for this specific handler. Used by saga_executor.resolveRetryBounds when
	// computing next_retry_at on transient failures. Nil means "use global defaults".
	RetryPolicy *RetryPolicy
}

// SemanticLinter performs AST-based semantic analysis on Starlark scripts.
// It detects financial math, complex logic, and Pre-Step Check violations.
type SemanticLinter struct {
	// enforcementLevels maps issue types to their enforcement level.
	enforcementLevels map[LintIssueType]EnforcementLevel

	// handlerMetadata maps handler names to their metadata.
	handlerMetadata map[string]HandlerMetadata
}

// NewSemanticLinter creates a new linter with default enforcement levels.
func NewSemanticLinter() *SemanticLinter {
	return &SemanticLinter{
		enforcementLevels: map[LintIssueType]EnforcementLevel{
			LintIssueTypeDecimalArithmetic:           EnforcementLevelWarning,
			LintIssueTypeMagicNumber:                 EnforcementLevelWarning,
			LintIssueTypeNestedConditional:           EnforcementLevelWarning,
			LintIssueTypeHardcodedCode:               EnforcementLevelWarning,
			LintIssueTypeMissingPreCheck:             EnforcementLevelError, // External handlers require pre-check
			LintIssueTypeMissingCompensationStrategy: EnforcementLevelWarning,
		},
		handlerMetadata: make(map[string]HandlerMetadata),
	}
}

// SetEnforcementLevel configures the enforcement level for a specific issue type.
func (l *SemanticLinter) SetEnforcementLevel(issueType LintIssueType, level EnforcementLevel) {
	l.enforcementLevels[issueType] = level
}

// SetHandlerMetadata configures the known handler metadata for Pre-Step Check validation.
func (l *SemanticLinter) SetHandlerMetadata(metadata map[string]HandlerMetadata) {
	l.handlerMetadata = metadata
}

// Analyze parses and lints a Starlark script, returning any issues found.
func (l *SemanticLinter) Analyze(script string) ([]LintIssue, error) {
	if script == "" {
		return nil, nil
	}

	fileOpts := &syntax.FileOptions{}
	file, err := fileOpts.Parse("script.star", script, 0)
	if err != nil {
		return nil, err
	}

	visitor := &lintVisitor{
		linter:           l,
		issues:           make([]LintIssue, 0),
		ifDepth:          0,
		verifiedHandlers: make(map[string]bool),
		decimalVars:      make(map[string]bool),
		counterVars:      make(map[string]bool),
		inLoopInit:       false,
	}

	for _, stmt := range file.Stmts {
		visitor.walkStmt(stmt)
	}

	return visitor.issues, nil
}

// HasBlockingIssues returns true if any issues have ERROR severity.
func (l *SemanticLinter) HasBlockingIssues(issues []LintIssue) bool {
	for _, issue := range issues {
		if issue.Severity == LintSeverityError {
			return true
		}
	}
	return false
}
