// Package saga provides saga definition management including reference validation.
package saga

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"go.starlark.net/syntax"
)

// Validation error types.
var (
	// ErrHandlerNotRegistered is returned when a step handler is not found in the registry.
	ErrHandlerNotRegistered = errors.New("step handler not registered")

	// ErrReferenceValidationFailed is returned when saga reference validation fails.
	ErrReferenceValidationFailed = errors.New("reference validation failed")

	// ErrPoolNotConfigured is returned when database pool is not configured for reference persistence.
	ErrPoolNotConfigured = errors.New("database pool not configured")

	// ErrHandlerParamValidationFailed is returned when handler parameter validation fails.
	ErrHandlerParamValidationFailed = errors.New("handler parameter validation failed")
)

// Validation status constants.
const (
	statusReady    = "READY"
	statusWarnings = "WARNINGS"
	statusBlocked  = "BLOCKED"
)

// Report icon constants.
const (
	iconOK      = "[OK]"
	iconError   = "[X]"
	iconWarning = "[!]"
)

// ReferenceType identifies the type of external reference in a saga script.
type ReferenceType string

const (
	// ReferenceTypeStepHandler represents a reference to a registered step handler.
	ReferenceTypeStepHandler ReferenceType = "step_handler"

	// ReferenceTypeInstrument represents a reference to an instrument via resolve_instrument().
	ReferenceTypeInstrument ReferenceType = "instrument"

	// ReferenceTypeAccount represents a reference to an account via resolve_account().
	ReferenceTypeAccount ReferenceType = "account"

	// ReferenceTypeSaga represents a reference to another saga via invoke_saga().
	ReferenceTypeSaga ReferenceType = "saga"

	// ReferenceTypeAttribute represents a reference to an instrument attribute.
	ReferenceTypeAttribute ReferenceType = "attribute"
)

// Reference represents an external reference extracted from a saga script.
type Reference struct {
	// Type identifies what kind of reference this is.
	Type ReferenceType

	// Key is the identifier for the reference (handler name, instrument code, etc.).
	Key string

	// LineNumber is the source line where this reference appears.
	LineNumber int

	// InstrumentCode is set for attribute references to indicate which instrument.
	InstrumentCode string

	// AttributeKey is set for attribute references to indicate the attribute name.
	AttributeKey string

	// Params holds extracted parameters for step handler references.
	// Keys are parameter names extracted from the step() call.
	// This enables schema validation of handler invocations.
	Params map[string]bool

	// ParamsKnown indicates whether params were statically extractable.
	// When false (e.g., params passed as a variable), skip validation.
	ParamsKnown bool
}

// ValidationError represents a validation issue found during reference checking.
type ValidationError struct {
	// Reference is the reference that failed validation.
	Reference Reference

	// Message describes the validation failure.
	Message string

	// Suggestion provides a hint for fixing the issue (e.g., "Did you mean...").
	Suggestion string

	// IsCritical indicates if this blocks activation (true) or is just a warning (false).
	IsCritical bool
}

// ValidationResult contains the outcome of saga validation.
type ValidationResult struct {
	// Status summarizes the validation outcome: "READY", "WARNINGS", or "BLOCKED".
	Status string

	// Errors contains all validation issues found.
	Errors []ValidationError

	// References contains all extracted references from the script.
	References []Reference
}

// FormatReport generates a human-readable validation report.
//
//nolint:gocognit // Report formatting has inherent complexity from multiple sections
func (r *ValidationResult) FormatReport() string {
	var b strings.Builder

	b.WriteString("=== Saga Validation Report ===\n\n")

	// Group errors by type
	errorsByType := r.groupErrorsByType()

	// Write each section
	writeSection(&b, "Step Handlers", "All step handlers are registered", errorsByType[ReferenceTypeStepHandler])
	writeSection(&b, "Instrument References", "All instrument references are valid", errorsByType[ReferenceTypeInstrument])
	writeSection(&b, "Account References", "All account references are valid", errorsByType[ReferenceTypeAccount])
	writeSection(&b, "Saga References", "All saga references are valid", errorsByType[ReferenceTypeSaga])
	writeSection(&b, "Attribute References", "All attribute references are valid", errorsByType[ReferenceTypeAttribute])

	// Summary
	r.writeSummary(&b)

	return b.String()
}

// groupErrorsByType groups validation errors by their reference type.
func (r *ValidationResult) groupErrorsByType() map[ReferenceType][]ValidationError {
	result := make(map[ReferenceType][]ValidationError)
	for _, err := range r.Errors {
		result[err.Reference.Type] = append(result[err.Reference.Type], err)
	}
	return result
}

// writeSection writes a single validation section to the builder.
func writeSection(b *strings.Builder, title, okMessage string, errors []ValidationError) {
	b.WriteString(title + ":\n")
	if len(errors) == 0 {
		_, _ = fmt.Fprintf(b, "  %s %s\n", iconOK, okMessage)
	} else {
		for _, err := range errors {
			icon := iconWarning
			if err.IsCritical {
				icon = iconError
			}
			_, _ = fmt.Fprintf(b, "  %s Line %d: %s\n", icon, err.Reference.LineNumber, err.Message)
			if err.Suggestion != "" {
				_, _ = fmt.Fprintf(b, "      Suggestion: %s\n", err.Suggestion)
			}
		}
	}
	b.WriteString("\n")
}

// writeSummary writes the summary section to the builder.
func (r *ValidationResult) writeSummary(b *strings.Builder) {
	b.WriteString("=== Summary ===\n")
	_, _ = fmt.Fprintf(b, "Status: %s\n", r.Status)
	_, _ = fmt.Fprintf(b, "Total References: %d\n", len(r.References))

	critical := 0
	warnings := 0
	for _, err := range r.Errors {
		if err.IsCritical {
			critical++
		} else {
			warnings++
		}
	}
	_, _ = fmt.Fprintf(b, "Critical Errors: %d\n", critical)
	_, _ = fmt.Fprintf(b, "Warnings: %d\n", warnings)
}

// InstrumentChecker provides methods to check instrument validity.
type InstrumentChecker interface {
	// InstrumentExists checks if an instrument with the given code exists.
	InstrumentExists(ctx context.Context, code string) (bool, error)

	// InstrumentIsActive checks if an instrument is in ACTIVE status.
	InstrumentIsActive(ctx context.Context, code string) (bool, error)

	// GetAttributeSchema returns the attribute schema for an instrument (nil if none).
	GetAttributeSchema(ctx context.Context, code string) (map[string]interface{}, error)

	// ListActiveInstrumentCodes returns all active instrument codes (for suggestions).
	ListActiveInstrumentCodes(ctx context.Context) ([]string, error)
}

// DefinitionChecker provides methods to check saga definition validity.
type DefinitionChecker interface {
	// SagaExists checks if a saga with the given name exists (any status).
	SagaExists(ctx context.Context, name string) (bool, error)

	// SagaIsActive checks if a saga with the given name has an ACTIVE version.
	SagaIsActive(ctx context.Context, name string) (bool, error)

	// ListActiveSagaNames returns all active saga names (for suggestions).
	ListActiveSagaNames(ctx context.Context) ([]string, error)
}

// ReferenceValidator validates saga scripts by extracting and checking references.
type ReferenceValidator struct {
	handlerRegistry   *pkgsaga.HandlerRegistry
	schemaRegistry    *schema.Registry
	instrumentChecker InstrumentChecker
	definitionChecker DefinitionChecker
	pool              *pgxpool.Pool
}

// NewReferenceValidator creates a new reference validator.
func NewReferenceValidator(
	handlerRegistry *pkgsaga.HandlerRegistry,
	instrumentChecker InstrumentChecker,
	definitionChecker DefinitionChecker,
	pool *pgxpool.Pool,
) *ReferenceValidator {
	return &ReferenceValidator{
		handlerRegistry:   handlerRegistry,
		instrumentChecker: instrumentChecker,
		definitionChecker: definitionChecker,
		pool:              pool,
	}
}

// SetSchemaRegistry configures the schema registry for parameter validation.
// If set, handler invocations will be validated against their schemas.
func (v *ReferenceValidator) SetSchemaRegistry(registry *schema.Registry) {
	v.schemaRegistry = registry
}

// ExtractReferences parses a Starlark script and extracts all external references.
func (v *ReferenceValidator) ExtractReferences(script string) ([]Reference, error) {
	if script == "" {
		return nil, nil
	}

	// Parse the script
	fileOpts := &syntax.FileOptions{}
	file, err := fileOpts.Parse("script.star", script, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse script: %w", err)
	}

	// Walk the AST to extract references
	extractor := &referenceExtractor{
		references: make([]Reference, 0),
	}

	for _, stmt := range file.Stmts {
		extractor.walkStmt(stmt)
	}

	return extractor.references, nil
}

// ValidateDraft validates a saga script for DRAFT status.
// Returns warnings for missing references but allows save.
// Populates saga_references table for impact analysis.
func (v *ReferenceValidator) ValidateDraft(ctx context.Context, sagaID uuid.UUID, script string) (*ValidationResult, error) {
	refs, err := v.ExtractReferences(script)
	if err != nil {
		return nil, err
	}

	result := &ValidationResult{
		Status:     statusReady,
		References: refs,
		Errors:     make([]ValidationError, 0),
	}

	// Check each reference (collect warnings, don't fail)
	for _, ref := range refs {
		if err := v.checkReference(ctx, ref, false, result); err != nil {
			return nil, err
		}
	}

	// Update status based on errors
	for _, err := range result.Errors {
		if err.IsCritical {
			result.Status = statusBlocked
			break
		}
		result.Status = statusWarnings
	}

	// Persist references for impact analysis
	if err := v.persistReferences(ctx, sagaID, refs); err != nil {
		return nil, fmt.Errorf("failed to persist references: %w", err)
	}

	return result, nil
}

// ValidateActivation validates a saga for activation.
// Re-validates ALL references (state may have changed since DRAFT).
// Returns error if any reference is invalid.
func (v *ReferenceValidator) ValidateActivation(ctx context.Context, sagaID uuid.UUID, script string) (*ValidationResult, error) {
	refs, err := v.ExtractReferences(script)
	if err != nil {
		return nil, err
	}

	result := &ValidationResult{
		Status:     statusReady,
		References: refs,
		Errors:     make([]ValidationError, 0),
	}

	// Check each reference (strict mode - all errors are critical)
	for _, ref := range refs {
		if err := v.checkReference(ctx, ref, true, result); err != nil {
			return nil, err
		}
	}

	// Update status based on errors
	for _, err := range result.Errors {
		if err.IsCritical {
			result.Status = statusBlocked
			break
		}
	}

	// Update persisted references
	if err := v.persistReferences(ctx, sagaID, refs); err != nil {
		return nil, fmt.Errorf("failed to persist references: %w", err)
	}

	return result, nil
}

// ValidateRuntime performs lightweight validation before saga execution.
// Verifies handlers are still registered.
func (v *ReferenceValidator) ValidateRuntime(_ context.Context, def *Definition) error {
	refs, err := v.ExtractReferences(def.Script)
	if err != nil {
		return err
	}

	// Only check step handlers for runtime validation (fast path)
	for _, ref := range refs {
		if ref.Type == ReferenceTypeStepHandler {
			if !v.handlerRegistry.Has(ref.Key) {
				return fmt.Errorf("%w: %s (line %d)", ErrHandlerNotRegistered, ref.Key, ref.LineNumber)
			}
		}
	}

	return nil
}

// Validate implements the Validator interface for use with PostgresRegistry.
// For platform-ref sagas (Script is empty), validates the ResolvedScript instead.
func (v *ReferenceValidator) Validate(ctx context.Context, def *Definition) error {
	script := def.Script
	if script == "" {
		script = def.ResolvedScript
	}
	result, err := v.ValidateActivation(ctx, def.ID, script)
	if err != nil {
		return err
	}

	if result.Status == statusBlocked {
		// Build error message from all critical errors
		var msgs []string
		for _, e := range result.Errors {
			if e.IsCritical {
				msgs = append(msgs, fmt.Sprintf("line %d: %s", e.Reference.LineNumber, e.Message))
			}
		}
		return fmt.Errorf("%w: %s", ErrReferenceValidationFailed, strings.Join(msgs, "; "))
	}

	return nil
}

// DeprecationImpactAnalysis finds all sagas that reference a given instrument.
func (v *ReferenceValidator) DeprecationImpactAnalysis(ctx context.Context, instrumentCode string) ([]Dependency, error) {
	if v.pool == nil {
		return nil, ErrPoolNotConfigured
	}

	query := `
		SELECT sd.id, sd.name, sd.version, sd.status, sr.line_number
		FROM saga_reference sr
		JOIN saga_definition sd ON sd.id = sr.saga_definition_id
		WHERE sr.reference_type = 'instrument' AND sr.reference_key = $1
		   OR (sr.reference_type = 'attribute' AND sr.instrument_code = $1)
		ORDER BY sd.name, sd.version`

	rows, err := v.pool.Query(ctx, query, instrumentCode)
	if err != nil {
		return nil, fmt.Errorf("failed to query saga dependencies: %w", err)
	}
	defer rows.Close()

	var deps []Dependency
	for rows.Next() {
		var dep Dependency
		var status string
		if err := rows.Scan(&dep.SagaID, &dep.SagaName, &dep.SagaVersion, &status, &dep.LineNumber); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		dep.SagaStatus = Status(status)
		deps = append(deps, dep)
	}

	return deps, rows.Err()
}

// Dependency represents a saga that depends on an external reference.
type Dependency struct {
	SagaID      uuid.UUID
	SagaName    string
	SagaVersion int
	SagaStatus  Status
	LineNumber  int
}

// checkReference validates a single reference and adds any errors to the result.
//
//nolint:gocognit // Reference checking has inherent complexity from multiple reference types
func (v *ReferenceValidator) checkReference(ctx context.Context, ref Reference, strict bool, result *ValidationResult) error {
	switch ref.Type {
	case ReferenceTypeStepHandler:
		v.checkStepHandler(ref, strict, result)
	case ReferenceTypeInstrument:
		if err := v.checkInstrument(ctx, ref, strict, result); err != nil {
			return err
		}
	case ReferenceTypeSaga:
		if err := v.checkSagaReference(ctx, ref, strict, result); err != nil {
			return err
		}
	case ReferenceTypeAttribute:
		if err := v.checkAttribute(ctx, ref, strict, result); err != nil {
			return err
		}

	case ReferenceTypeAccount:
		// Account validation would require an AccountChecker interface
		// For now, we just collect the references without validation
	}

	return nil
}

// checkStepHandler validates a step handler reference.
func (v *ReferenceValidator) checkStepHandler(ref Reference, strict bool, result *ValidationResult) {
	if !v.handlerRegistry.Has(ref.Key) {
		suggestion := v.suggestHandler(ref.Key)
		result.Errors = append(result.Errors, ValidationError{
			Reference:  ref,
			Message:    fmt.Sprintf("step handler '%s' is not registered", ref.Key),
			Suggestion: suggestion,
			IsCritical: strict,
		})
		return
	}

	// If schema registry is configured, validate parameters against schema
	if v.schemaRegistry != nil && v.schemaRegistry.HasHandler(ref.Key) {
		v.validateHandlerParams(ref, strict, result)
	}
}

// validateHandlerParams validates that extracted parameter names match the handler schema.
func (v *ReferenceValidator) validateHandlerParams(ref Reference, strict bool, result *ValidationResult) {
	// Skip validation if params weren't statically extractable (e.g., passed as variable)
	if !ref.ParamsKnown {
		return
	}

	handlerDef, err := v.schemaRegistry.GetHandler(ref.Key)
	if err != nil {
		return // Schema not found - skip param validation
	}

	// Check for missing required parameters
	for paramName, paramDef := range handlerDef.Params {
		if paramDef.Required {
			if _, provided := ref.Params[paramName]; !provided {
				result.Errors = append(result.Errors, ValidationError{
					Reference:  ref,
					Message:    fmt.Sprintf("handler '%s' missing required parameter '%s'", ref.Key, paramName),
					IsCritical: strict,
				})
			}
		}
	}

	// Check for unknown parameters (params not in schema)
	for paramName := range ref.Params {
		if _, exists := handlerDef.Params[paramName]; !exists {
			result.Errors = append(result.Errors, ValidationError{
				Reference:  ref,
				Message:    fmt.Sprintf("handler '%s' has unknown parameter '%s'", ref.Key, paramName),
				IsCritical: false, // Unknown params are warnings, not errors
			})
		}
	}
}

// checkInstrument validates an instrument reference.
func (v *ReferenceValidator) checkInstrument(ctx context.Context, ref Reference, strict bool, result *ValidationResult) error {
	if v.instrumentChecker == nil {
		return nil
	}

	exists, err := v.instrumentChecker.InstrumentExists(ctx, ref.Key)
	if err != nil {
		return err
	}

	if !exists {
		suggestion := v.suggestInstrument(ctx, ref.Key)
		result.Errors = append(result.Errors, ValidationError{
			Reference:  ref,
			Message:    fmt.Sprintf("instrument '%s' does not exist", ref.Key),
			Suggestion: suggestion,
			IsCritical: strict,
		})
		return nil
	}

	if strict {
		active, err := v.instrumentChecker.InstrumentIsActive(ctx, ref.Key)
		if err != nil {
			return err
		}
		if !active {
			result.Errors = append(result.Errors, ValidationError{
				Reference:  ref,
				Message:    fmt.Sprintf("instrument '%s' is not in ACTIVE status", ref.Key),
				IsCritical: true,
			})
		}
	}

	return nil
}

// checkSagaReference validates a saga reference.
func (v *ReferenceValidator) checkSagaReference(ctx context.Context, ref Reference, strict bool, result *ValidationResult) error {
	if v.definitionChecker == nil {
		return nil
	}

	exists, err := v.definitionChecker.SagaExists(ctx, ref.Key)
	if err != nil {
		return err
	}

	if !exists {
		suggestion := v.suggestSaga(ctx, ref.Key)
		result.Errors = append(result.Errors, ValidationError{
			Reference:  ref,
			Message:    fmt.Sprintf("saga '%s' does not exist", ref.Key),
			Suggestion: suggestion,
			IsCritical: strict,
		})
		return nil
	}

	if strict {
		active, err := v.definitionChecker.SagaIsActive(ctx, ref.Key)
		if err != nil {
			return err
		}
		if !active {
			result.Errors = append(result.Errors, ValidationError{
				Reference:  ref,
				Message:    fmt.Sprintf("saga '%s' does not have an ACTIVE version", ref.Key),
				IsCritical: true,
			})
		}
	}

	return nil
}

// checkAttribute validates an attribute reference.
func (v *ReferenceValidator) checkAttribute(ctx context.Context, ref Reference, strict bool, result *ValidationResult) error {
	if v.instrumentChecker == nil || ref.InstrumentCode == "" {
		return nil
	}

	schema, err := v.instrumentChecker.GetAttributeSchema(ctx, ref.InstrumentCode)
	if err != nil {
		return err
	}

	if schema != nil {
		if _, ok := schema[ref.AttributeKey]; !ok {
			suggestion := v.suggestAttribute(schema, ref.AttributeKey)
			result.Errors = append(result.Errors, ValidationError{
				Reference:  ref,
				Message:    fmt.Sprintf("attribute '%s' not found in instrument '%s' schema", ref.AttributeKey, ref.InstrumentCode),
				Suggestion: suggestion,
				IsCritical: strict,
			})
		}
	}

	return nil
}

// ListHandlers returns all registered handler names sorted alphabetically.
func (v *ReferenceValidator) ListHandlers() []string {
	handlers := v.handlerRegistry.List()
	sort.Strings(handlers)
	return handlers
}
