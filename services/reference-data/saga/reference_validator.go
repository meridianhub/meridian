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
	handlerRegistry   *pkgsaga.DomainHandlerRegistry
	schemaRegistry    *schema.Registry
	instrumentChecker InstrumentChecker
	definitionChecker DefinitionChecker
	pool              *pgxpool.Pool
}

// NewReferenceValidator creates a new reference validator.
func NewReferenceValidator(
	handlerRegistry *pkgsaga.DomainHandlerRegistry,
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
func (v *ReferenceValidator) Validate(ctx context.Context, def *Definition) error {
	result, err := v.ValidateActivation(ctx, def.ID, def.Script)
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

// suggestHandler finds a similar handler name for suggestions.
func (v *ReferenceValidator) suggestHandler(name string) string {
	handlers := v.handlerRegistry.List()
	if match := findSimilar(name, handlers); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestInstrument finds a similar instrument code for suggestions.
func (v *ReferenceValidator) suggestInstrument(ctx context.Context, code string) string {
	if v.instrumentChecker == nil {
		return ""
	}
	codes, err := v.instrumentChecker.ListActiveInstrumentCodes(ctx)
	if err != nil {
		return ""
	}
	if match := findSimilar(code, codes); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestSaga finds a similar saga name for suggestions.
func (v *ReferenceValidator) suggestSaga(ctx context.Context, name string) string {
	if v.definitionChecker == nil {
		return ""
	}
	names, err := v.definitionChecker.ListActiveSagaNames(ctx)
	if err != nil {
		return ""
	}
	if match := findSimilar(name, names); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestAttribute finds a similar attribute key for suggestions.
func (v *ReferenceValidator) suggestAttribute(schema map[string]interface{}, key string) string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	if match := findSimilar(key, keys); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// findSimilar finds the most similar string in candidates using Levenshtein distance.
func findSimilar(target string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	target = strings.ToLower(target)
	var bestMatch string
	bestScore := -1

	for _, candidate := range candidates {
		score := similarity(target, strings.ToLower(candidate))
		if score > bestScore {
			bestScore = score
			bestMatch = candidate
		}
	}

	// Only suggest if similarity is above threshold (at least 50% similar)
	if bestScore >= len(target)/2 {
		return bestMatch
	}
	return ""
}

// similarity calculates the similarity between two strings.
// Returns higher scores for more similar strings.
func similarity(a, b string) int {
	if a == b {
		return len(a) * 2
	}

	// Simple prefix/suffix matching
	score := 0

	// Common prefix
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen && a[i] == b[i]; i++ {
		score++
	}

	// Common suffix (only if different from prefix)
	for i := 1; i <= minLen && a[len(a)-i] == b[len(b)-i]; i++ {
		if len(a)-i >= score || len(b)-i >= score { // Don't double-count
			score++
		}
	}

	// Substring matching
	if strings.Contains(a, b) || strings.Contains(b, a) {
		score += minLen / 2
	}

	return score
}

// persistReferences saves extracted references to the saga_reference table.
func (v *ReferenceValidator) persistReferences(ctx context.Context, sagaID uuid.UUID, refs []Reference) error {
	if v.pool == nil {
		return nil // No database configured, skip persistence
	}

	// Start a transaction
	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Delete existing references for this saga
	_, err = tx.Exec(ctx, "DELETE FROM saga_reference WHERE saga_definition_id = $1", sagaID)
	if err != nil {
		return fmt.Errorf("failed to delete existing references: %w", err)
	}

	// Insert new references
	for _, ref := range refs {
		_, err = tx.Exec(ctx, `
			INSERT INTO saga_reference (saga_definition_id, reference_type, reference_key, instrument_code, attribute_key, line_number)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (saga_definition_id, reference_type, reference_key) DO UPDATE
			SET instrument_code = EXCLUDED.instrument_code,
				attribute_key = EXCLUDED.attribute_key,
				line_number = EXCLUDED.line_number,
				extracted_at = now()`,
			sagaID, string(ref.Type), ref.Key, nullString(ref.InstrumentCode), nullString(ref.AttributeKey), ref.LineNumber)
		if err != nil {
			return fmt.Errorf("failed to insert reference: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// referenceExtractor walks the Starlark AST to extract references.
type referenceExtractor struct {
	references []Reference
}

// walkStmt walks a statement node.
func (e *referenceExtractor) walkStmt(stmt syntax.Stmt) {
	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		e.walkExpr(s.X)

	case *syntax.AssignStmt:
		e.walkExpr(s.LHS)
		e.walkExpr(s.RHS)

	case *syntax.DefStmt:
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.IfStmt:
		e.walkExpr(s.Cond)
		for _, stmt := range s.True {
			e.walkStmt(stmt)
		}
		for _, stmt := range s.False {
			e.walkStmt(stmt)
		}

	case *syntax.ForStmt:
		e.walkExpr(s.X)
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.WhileStmt:
		e.walkExpr(s.Cond)
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.ReturnStmt:
		if s.Result != nil {
			e.walkExpr(s.Result)
		}
	}
}

// walkExpr walks an expression node and extracts references.
//
//nolint:gocognit,gocyclo // AST walking requires handling many expression types
func (e *referenceExtractor) walkExpr(expr syntax.Expr) {
	if expr == nil {
		return
	}

	switch ex := expr.(type) {
	case *syntax.CallExpr:
		// Check for specific function calls
		if ident, ok := ex.Fn.(*syntax.Ident); ok {
			switch ident.Name {
			case "resolve_instrument":
				// Extract instrument code from first argument
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						// Remove quotes from string literal
						code := strings.Trim(lit.Raw, `"'`)
						e.references = append(e.references, Reference{
							Type:       ReferenceTypeInstrument,
							Key:        code,
							LineNumber: int(ident.NamePos.Line),
						})
					}
				}

			case "resolve_account":
				// Extract account reference from first argument
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						code := strings.Trim(lit.Raw, `"'`)
						e.references = append(e.references, Reference{
							Type:       ReferenceTypeAccount,
							Key:        code,
							LineNumber: int(ident.NamePos.Line),
						})
					}
				}

			case "invoke_saga":
				// Extract saga name from first argument (positional or keyword "saga_name")
				var sagaName string
				lineNum := int(ident.NamePos.Line)

				// Check positional arguments first
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						sagaName = strings.Trim(lit.Raw, `"'`)
					}
				}

				if sagaName != "" {
					e.references = append(e.references, Reference{
						Type:       ReferenceTypeSaga,
						Key:        sagaName,
						LineNumber: lineNum,
					})
				}

			case "step":
				// Extract step handler and params from keyword arguments
				var handler string
				var lineNum int
				paramNames := make(map[string]bool)
				paramsKnown := true // Assume known unless we encounter non-literal params

				for _, kwarg := range ex.Args {
					if binExpr, ok := kwarg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
						if nameIdent, ok := binExpr.X.(*syntax.Ident); ok {
							switch nameIdent.Name {
							case "action":
								if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
									handler = strings.Trim(lit.Raw, `"'`)
									lineNum = int(ident.NamePos.Line)
								}
							case "params":
								// Extract param names from the params dict
								if dictExpr, ok := binExpr.Y.(*syntax.DictExpr); ok {
									for _, entry := range dictExpr.List {
										if dictEntry, ok := entry.(*syntax.DictEntry); ok {
											keyLit, ok := dictEntry.Key.(*syntax.Literal)
											if !ok || keyLit.Token != syntax.STRING {
												// Non-literal key (e.g., variable) - can't extract
												paramsKnown = false
												continue
											}
											paramName := strings.Trim(keyLit.Raw, `"'`)
											paramNames[paramName] = true
										}
									}
								} else {
									// params is not a literal dict (e.g., a variable)
									paramsKnown = false
								}
							}
						}
					}
				}

				if handler != "" {
					e.references = append(e.references, Reference{
						Type:        ReferenceTypeStepHandler,
						Key:         handler,
						LineNumber:  lineNum,
						Params:      paramNames,
						ParamsKnown: paramsKnown,
					})
				}
			}
		}

		// Walk function expression and arguments
		e.walkExpr(ex.Fn)
		for _, arg := range ex.Args {
			e.walkExpr(arg)
		}

	case *syntax.IndexExpr:
		// Check for attribute access pattern: ctx.position.attributes["key"]
		// or instrument.attributes["key"]
		e.walkExpr(ex.X)
		e.walkExpr(ex.Y)

		// Try to extract attribute reference
		if e.isAttributeAccess(ex) {
			if lit, ok := ex.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
				attrKey := strings.Trim(lit.Raw, `"'`)
				// Try to extract instrument code from the expression
				instrumentCode := e.extractInstrumentCode(ex.X)
				e.references = append(e.references, Reference{
					Type:           ReferenceTypeAttribute,
					Key:            attrKey,
					AttributeKey:   attrKey,
					InstrumentCode: instrumentCode,
					LineNumber:     int(lit.TokenPos.Line),
				})
			}
		}

	case *syntax.BinaryExpr:
		e.walkExpr(ex.X)
		e.walkExpr(ex.Y)

	case *syntax.UnaryExpr:
		e.walkExpr(ex.X)

	case *syntax.CondExpr:
		e.walkExpr(ex.Cond)
		e.walkExpr(ex.True)
		e.walkExpr(ex.False)

	case *syntax.SliceExpr:
		e.walkExpr(ex.X)
		e.walkExpr(ex.Lo)
		e.walkExpr(ex.Hi)
		e.walkExpr(ex.Step)

	case *syntax.ListExpr:
		for _, elem := range ex.List {
			e.walkExpr(elem)
		}

	case *syntax.DictExpr:
		for _, entry := range ex.List {
			if dictEntry, ok := entry.(*syntax.DictEntry); ok {
				e.walkExpr(dictEntry.Key)
				e.walkExpr(dictEntry.Value)
			}
		}

	case *syntax.TupleExpr:
		for _, elem := range ex.List {
			e.walkExpr(elem)
		}

	case *syntax.Comprehension:
		for _, clause := range ex.Clauses {
			if forClause, ok := clause.(*syntax.ForClause); ok {
				e.walkExpr(forClause.X)
			}
			if ifClause, ok := clause.(*syntax.IfClause); ok {
				e.walkExpr(ifClause.Cond)
			}
		}
		e.walkExpr(ex.Body)

	case *syntax.LambdaExpr:
		e.walkExpr(ex.Body)

	case *syntax.DotExpr:
		e.walkExpr(ex.X)

	case *syntax.ParenExpr:
		e.walkExpr(ex.X)
	}
}

// isAttributeAccess checks if an index expression is accessing .attributes[...]
func (e *referenceExtractor) isAttributeAccess(expr *syntax.IndexExpr) bool {
	if dotExpr, ok := expr.X.(*syntax.DotExpr); ok {
		return dotExpr.Name.Name == "attributes"
	}
	return false
}

// extractInstrumentCode tries to extract instrument code from attribute access context.
//
//nolint:gocognit // Nested type assertions for AST pattern matching have inherent complexity
func (e *referenceExtractor) extractInstrumentCode(expr syntax.Expr) string {
	// Look for patterns like:
	// - instrument.attributes["key"] where instrument is a variable
	// - ctx.instrument.attributes["key"]
	// - resolve_instrument("CODE").attributes["key"]

	if dotExpr, ok := expr.(*syntax.DotExpr); ok {
		if dotExpr.Name.Name == "attributes" {
			// Check if the base is a call to resolve_instrument
			if callExpr, ok := dotExpr.X.(*syntax.CallExpr); ok {
				if ident, ok := callExpr.Fn.(*syntax.Ident); ok && ident.Name == "resolve_instrument" {
					if len(callExpr.Args) > 0 {
						if lit, ok := callExpr.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
							return strings.Trim(lit.Raw, `"'`)
						}
					}
				}
			}
		}
	}

	return ""
}

// ListHandlers returns all registered handler names sorted alphabetically.
func (v *ReferenceValidator) ListHandlers() []string {
	handlers := v.handlerRegistry.List()
	sort.Strings(handlers)
	return handlers
}
