//meridian:large-file — known oversized file; split tracked in backlog
// Package registry provides the InstrumentRegistry implementation backed by PostgreSQL.
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostgresRegistry implements InstrumentRegistry using PostgreSQL.
type PostgresRegistry struct {
	pool     *pgxpool.Pool
	compiler *refcel.Compiler

	// programCache stores compiled CEL programs keyed by "code:version:expression_type".
	// This avoids recompiling CEL expressions on every ValidateAttributes call.
	programCache   map[string]cel.Program
	programCacheMu sync.RWMutex
}

// NewPostgresRegistry creates a new PostgreSQL-backed instrument registry.
func NewPostgresRegistry(pool *pgxpool.Pool) (*PostgresRegistry, error) {
	compiler, err := refcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}

	return &PostgresRegistry{
		pool:         pool,
		compiler:     compiler,
		programCache: make(map[string]cel.Program),
	}, nil
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
func (r *PostgresRegistry) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrMissingTenantContext
	}

	// Quote the schema name to prevent SQL injection
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// withReadTransaction executes a read-only function within a transaction with tenant scoping.
func (r *PostgresRegistry) withReadTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit read transaction: %w", err)
	}

	return nil
}

// withWriteTransaction executes a write function within a transaction with tenant scoping.
func (r *PostgresRegistry) withWriteTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetDefinition retrieves a specific instrument by code and version.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, code string, version int) (*InstrumentDefinition, error) {
	var result *InstrumentDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, dimension, precision, status, is_system,
				validation_expression, fungibility_key_expression, error_message_expression,
				attribute_schema, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM instrument_definition
			WHERE code = $1 AND version = $2`

		row := tx.QueryRow(ctx, query, code, version)
		def, err := r.scanInstrumentDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query instrument definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetActiveDefinition retrieves the latest ACTIVE version of an instrument.
func (r *PostgresRegistry) GetActiveDefinition(ctx context.Context, code string) (*InstrumentDefinition, error) {
	var result *InstrumentDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, dimension, precision, status, is_system,
				validation_expression, fungibility_key_expression, error_message_expression,
				attribute_schema, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM instrument_definition
			WHERE code = $1 AND status = 'ACTIVE'
			ORDER BY version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, query, code)
		def, err := r.scanInstrumentDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query active instrument definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ListActive retrieves all instruments with ACTIVE status.
func (r *PostgresRegistry) ListActive(ctx context.Context) ([]*InstrumentDefinition, error) {
	var result []*InstrumentDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, dimension, precision, status, is_system,
				validation_expression, fungibility_key_expression, error_message_expression,
				attribute_schema, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM instrument_definition
			WHERE status = 'ACTIVE'
			ORDER BY code, version DESC`

		rows, err := tx.Query(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to query active instruments: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanInstrumentDefinitionFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, def)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ListByStatus retrieves all instruments with the specified status.
// If status is empty, returns all instruments regardless of status.
func (r *PostgresRegistry) ListByStatus(ctx context.Context, status Status) ([]*InstrumentDefinition, error) {
	var result []*InstrumentDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		var query string
		var args []any

		if status == "" {
			query = `
				SELECT id, code, version, dimension, precision, status, is_system,
					validation_expression, fungibility_key_expression, error_message_expression,
					attribute_schema, display_name, description,
					created_at, updated_at, activated_at, deprecated_at, successor_id
				FROM instrument_definition
				ORDER BY code, version DESC`
		} else {
			query = `
				SELECT id, code, version, dimension, precision, status, is_system,
					validation_expression, fungibility_key_expression, error_message_expression,
					attribute_schema, display_name, description,
					created_at, updated_at, activated_at, deprecated_at, successor_id
				FROM instrument_definition
				WHERE status = $1
				ORDER BY code, version DESC`
			args = append(args, string(status))
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to query instruments by status: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanInstrumentDefinitionFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, def)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// CreateDraft creates a new instrument definition in DRAFT status.
func (r *PostgresRegistry) CreateDraft(ctx context.Context, def *InstrumentDefinition) error {
	// Reject system instrument creation
	if def.IsSystem {
		return ErrSystemInstrumentReadOnly
	}

	// Compile CEL expressions at creation time (fail-fast)
	if err := r.compileCELExpressions(def); err != nil {
		return err
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			INSERT INTO instrument_definition (
				id, code, version, dimension, precision, status, is_system,
				validation_expression, fungibility_key_expression, error_message_expression,
				attribute_schema, display_name, description,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7,
				$8, $9, $10,
				$11, $12, $13,
				$14, $15
			)`

		// Generate ID if not set
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}

		now := time.Now()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		// Default empty attribute_schema to valid JSON — column is jsonb
		attrSchema := def.AttributeSchema
		if len(attrSchema) == 0 {
			attrSchema = []byte("{}")
		}

		_, err := tx.Exec(ctx, query,
			def.ID, def.Code, def.Version, string(def.Dimension), def.Precision, string(def.Status), def.IsSystem,
			nullString(def.ValidationExpression), def.FungibilityKeyExpression, nullString(def.ErrorMessageExpression),
			attrSchema, nullString(def.DisplayName), nullString(def.Description),
			def.CreatedAt, def.UpdatedAt,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert instrument definition: %w", err)
		}

		return nil
	})
}

// UpdateDefinition updates a DRAFT instrument definition.
func (r *PostgresRegistry) UpdateDefinition(ctx context.Context, code string, version int, updates *InstrumentDefinition) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// First, check if the instrument exists and is not a system instrument
		var isSystem bool
		var currentStatus string
		var currentUpdatedAt time.Time

		checkQuery := `SELECT is_system, status, updated_at FROM instrument_definition WHERE code = $1 AND version = $2`
		err := tx.QueryRow(ctx, checkQuery, code, version).Scan(&isSystem, &currentStatus, &currentUpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check instrument: %w", err)
		}

		if isSystem {
			return ErrSystemInstrumentReadOnly
		}

		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		// Compile CEL expressions if provided
		if err := r.compileCELExpressions(updates); err != nil {
			return err
		}

		// Update the instrument
		updateQuery := `
			UPDATE instrument_definition SET
				dimension = COALESCE(NULLIF($1, ''), dimension),
				precision = CASE WHEN $2 >= 0 THEN $2 ELSE precision END,
				validation_expression = $3,
				fungibility_key_expression = $4,
				error_message_expression = $5,
				attribute_schema = COALESCE($6, attribute_schema),
				display_name = $7,
				description = $8,
				updated_at = $9
			WHERE code = $10 AND version = $11 AND updated_at = $12`

		updateAttrSchema := updates.AttributeSchema
		if updateAttrSchema != nil && len(updateAttrSchema) == 0 {
			updateAttrSchema = []byte("{}")
		}

		now := time.Now()
		result, err := tx.Exec(ctx, updateQuery,
			string(updates.Dimension), updates.Precision,
			nullString(updates.ValidationExpression),
			updates.FungibilityKeyExpression,
			nullString(updates.ErrorMessageExpression),
			updateAttrSchema,
			nullString(updates.DisplayName),
			nullString(updates.Description),
			now,
			code, version, currentUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to update instrument definition: %w", err)
		}

		if result.RowsAffected() == 0 {
			return ErrOptimisticLock
		}

		// Invalidate cache for this instrument
		r.invalidateCache(code, version)

		return nil
	})
}

// ActivateInstrument transitions an instrument from DRAFT to ACTIVE.
func (r *PostgresRegistry) ActivateInstrument(ctx context.Context, code string, version int) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		var isSystem bool
		var currentStatus string

		checkQuery := `SELECT is_system, status FROM instrument_definition WHERE code = $1 AND version = $2`
		err := tx.QueryRow(ctx, checkQuery, code, version).Scan(&isSystem, &currentStatus)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check instrument: %w", err)
		}

		if isSystem {
			return ErrSystemInstrumentReadOnly
		}

		if err := ValidateStatusTransition(Status(currentStatus), StatusActive); err != nil {
			return ErrNotDraft
		}

		now := time.Now()
		updateQuery := `
			UPDATE instrument_definition SET
				status = 'ACTIVE',
				activated_at = $1,
				updated_at = $1
			WHERE code = $2 AND version = $3`

		_, err = tx.Exec(ctx, updateQuery, now, code, version)
		if err != nil {
			return fmt.Errorf("failed to activate instrument: %w", err)
		}

		return nil
	})
}

// DeprecateInstrument transitions an instrument from ACTIVE to DEPRECATED.
// Successor validation is enforced at the Go application layer (CockroachDB does not support PL/pgSQL triggers):
// the successor must exist, be ACTIVE, have the same dimension, and not be self-referential.
// Successor ID is write-once: once set, it cannot be changed.
func (r *PostgresRegistry) DeprecateInstrument(ctx context.Context, code string, version int, successorID *uuid.UUID) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		var instrumentID uuid.UUID
		var isSystem bool
		var currentStatus string
		var currentDimension string
		var existingSuccessorID *uuid.UUID

		checkQuery := `SELECT id, is_system, status, dimension, successor_id
			FROM instrument_definition WHERE code = $1 AND version = $2`
		err := tx.QueryRow(ctx, checkQuery, code, version).Scan(
			&instrumentID, &isSystem, &currentStatus, &currentDimension, &existingSuccessorID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check instrument: %w", err)
		}

		if isSystem {
			return ErrSystemInstrumentReadOnly
		}

		if err := ValidateStatusTransition(Status(currentStatus), StatusDeprecated); err != nil {
			return ErrNotActive
		}

		// Enforce write-once semantics for successor_id
		if existingSuccessorID != nil && successorID != nil && *existingSuccessorID != *successorID {
			return ErrSuccessorWriteOnce
		}

		// Validate successor if provided
		if successorID != nil {
			if err := r.validateSuccessor(ctx, tx, instrumentID, *successorID, currentDimension); err != nil {
				return err
			}
		}

		now := time.Now()
		updateQuery := `
			UPDATE instrument_definition SET
				status = 'DEPRECATED',
				deprecated_at = $1,
				updated_at = $1,
				successor_id = $4
			WHERE code = $2 AND version = $3`

		_, err = tx.Exec(ctx, updateQuery, now, code, version, successorID)
		if err != nil {
			return fmt.Errorf("failed to deprecate instrument: %w", err)
		}

		return nil
	})
}

// validateSuccessor checks that a proposed successor instrument is valid.
// It must exist, be ACTIVE, have the same dimension, and not be the instrument itself.
func (r *PostgresRegistry) validateSuccessor(ctx context.Context, tx pgx.Tx, instrumentID, successorID uuid.UUID, requiredDimension string) error {
	// Cannot designate self as successor
	if successorID == instrumentID {
		return ErrSuccessorInvalid
	}

	var successorStatus string
	var successorDimension string
	query := `SELECT status, dimension FROM instrument_definition WHERE id = $1`
	err := tx.QueryRow(ctx, query, successorID).Scan(&successorStatus, &successorDimension)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSuccessorInvalid
		}
		return fmt.Errorf("failed to query successor instrument: %w", err)
	}

	if successorStatus != string(StatusActive) {
		return ErrSuccessorInvalid
	}

	if successorDimension != requiredDimension {
		return ErrSuccessorInvalid
	}

	return nil
}

// ValidateAttributes executes the CEL validation expression against the provided attributes.
func (r *PostgresRegistry) ValidateAttributes(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error) {
	def, err := r.GetDefinition(ctx, code, version)
	if err != nil {
		return ValidationResult{}, err
	}

	if def.ValidationExpression == "" {
		return ValidationResult{Valid: true}, nil
	}

	prg, err := r.getOrCompileValidation(code, version, def.ValidationExpression)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to get validation program: %w", err)
	}

	input := r.buildCELInput(attrs)
	return r.executeValidation(prg, input, def, code, version)
}

// buildCELInput constructs the input map for CEL evaluation.
func (r *PostgresRegistry) buildCELInput(attrs AttributeBag) map[string]any {
	// Ensure attributes is never nil to prevent CEL evaluation issues
	attributes := attrs.Attributes
	if attributes == nil {
		attributes = make(map[string]string)
	}

	input := map[string]any{
		"attributes": attributes,
		"amount":     attrs.Amount,
		"source":     attrs.Source,
		"valid_from": time.Time{},
		"valid_to":   time.Time{},
	}

	if attrs.ValidFrom != nil {
		input["valid_from"] = *attrs.ValidFrom
	}
	if attrs.ValidTo != nil {
		input["valid_to"] = *attrs.ValidTo
	}

	return input
}

// executeValidation runs the CEL program and handles the result.
func (r *PostgresRegistry) executeValidation(prg cel.Program, input map[string]any, def *InstrumentDefinition, code string, version int) (ValidationResult, error) {
	result, _, err := prg.Eval(input)
	if err != nil {
		return ValidationResult{
			Valid:        false,
			ErrorMessage: fmt.Sprintf("validation expression error: %v", err),
		}, nil
	}

	valid, ok := result.Value().(bool)
	if !ok {
		return ValidationResult{
			Valid:        false,
			ErrorMessage: "validation expression did not return boolean",
		}, nil
	}

	if valid {
		return ValidationResult{Valid: true}, nil
	}

	errorMsg := r.getCustomErrorMessage(def, code, version, input)
	return ValidationResult{Valid: false, ErrorMessage: errorMsg}, nil
}

const defaultValidationErrorMsg = "validation failed"

// getCustomErrorMessage evaluates the error message expression if defined.
func (r *PostgresRegistry) getCustomErrorMessage(def *InstrumentDefinition, code string, version int, input map[string]any) string {
	if def.ErrorMessageExpression == "" {
		return defaultValidationErrorMsg
	}

	errorPrg, err := r.getOrCompileErrorMessage(code, version, def.ErrorMessageExpression)
	if err != nil {
		return defaultValidationErrorMsg
	}

	errorResult, _, evalErr := errorPrg.Eval(input)
	if evalErr != nil {
		return defaultValidationErrorMsg
	}

	if msg, ok := errorResult.Value().(string); ok {
		return msg
	}

	return defaultValidationErrorMsg
}

// compileCELExpressions validates and compiles all CEL expressions in a definition.
func (r *PostgresRegistry) compileCELExpressions(def *InstrumentDefinition) error {
	if def.ValidationExpression != "" {
		_, err := r.compiler.CompileValidation(def.ValidationExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("validation expression: %w", err))
		}
	}

	if def.FungibilityKeyExpression != "" {
		_, err := r.compiler.CompileBucketKey(def.FungibilityKeyExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("fungibility key expression: %w", err))
		}
	}

	// ErrorMessageExpression uses the same environment as validation but returns a string.
	if def.ErrorMessageExpression != "" {
		_, err := r.compiler.CompileValueExpression(def.ErrorMessageExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("error message expression: %w", err))
		}
	}

	return nil
}

// getOrCompileValidation gets a cached validation program or compiles one.
func (r *PostgresRegistry) getOrCompileValidation(code string, version int, expr string) (cel.Program, error) {
	cacheKey := fmt.Sprintf("%s:%d:validation", code, version)

	r.programCacheMu.RLock()
	prg, ok := r.programCache[cacheKey]
	r.programCacheMu.RUnlock()

	if ok {
		return prg, nil
	}

	// Compile and cache
	prg, err := r.compiler.CompileValidation(expr)
	if err != nil {
		return nil, err
	}

	r.programCacheMu.Lock()
	r.programCache[cacheKey] = prg
	r.programCacheMu.Unlock()

	return prg, nil
}

// getOrCompileErrorMessage gets a cached error message program or compiles one.
func (r *PostgresRegistry) getOrCompileErrorMessage(code string, version int, expr string) (cel.Program, error) {
	cacheKey := fmt.Sprintf("%s:%d:error", code, version)

	r.programCacheMu.RLock()
	prg, ok := r.programCache[cacheKey]
	r.programCacheMu.RUnlock()

	if ok {
		return prg, nil
	}

	// Compile and cache; error message expressions return strings, not booleans.
	prg, err := r.compiler.CompileValueExpression(expr)
	if err != nil {
		return nil, err
	}

	r.programCacheMu.Lock()
	r.programCache[cacheKey] = prg
	r.programCacheMu.Unlock()

	return prg, nil
}

// invalidateCache removes cached programs for an instrument.
func (r *PostgresRegistry) invalidateCache(code string, version int) {
	r.programCacheMu.Lock()
	defer r.programCacheMu.Unlock()

	delete(r.programCache, fmt.Sprintf("%s:%d:validation", code, version))
	delete(r.programCache, fmt.Sprintf("%s:%d:error", code, version))
	delete(r.programCache, fmt.Sprintf("%s:%d:bucket", code, version))
}

// scanInstrumentDefinition scans a single row into an InstrumentDefinition.
func (r *PostgresRegistry) scanInstrumentDefinition(row pgx.Row) (*InstrumentDefinition, error) {
	var def InstrumentDefinition
	var dimension, status string
	var validationExpr, errorMsgExpr, displayName, description sql.NullString
	var successorID *uuid.UUID

	err := row.Scan(
		&def.ID, &def.Code, &def.Version, &dimension, &def.Precision, &status, &def.IsSystem,
		&validationExpr, &def.FungibilityKeyExpression, &errorMsgExpr,
		&def.AttributeSchema, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
	)
	if err != nil {
		return nil, err
	}

	def.Dimension = Dimension(dimension)
	def.Status = Status(status)
	def.SuccessorID = successorID

	if validationExpr.Valid {
		def.ValidationExpression = validationExpr.String
	}
	if errorMsgExpr.Valid {
		def.ErrorMessageExpression = errorMsgExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}

	return &def, nil
}

// scanInstrumentDefinitionFromRows scans from pgx.Rows.
func (r *PostgresRegistry) scanInstrumentDefinitionFromRows(rows pgx.Rows) (*InstrumentDefinition, error) {
	var def InstrumentDefinition
	var dimension, status string
	var validationExpr, errorMsgExpr, displayName, description sql.NullString
	var successorID *uuid.UUID

	err := rows.Scan(
		&def.ID, &def.Code, &def.Version, &dimension, &def.Precision, &status, &def.IsSystem,
		&validationExpr, &def.FungibilityKeyExpression, &errorMsgExpr,
		&def.AttributeSchema, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
	)
	if err != nil {
		return nil, err
	}

	def.Dimension = Dimension(dimension)
	def.Status = Status(status)
	def.SuccessorID = successorID

	if validationExpr.Valid {
		def.ValidationExpression = validationExpr.String
	}
	if errorMsgExpr.Valid {
		def.ErrorMessageExpression = errorMsgExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}

	return &def, nil
}

// nullString converts a string to sql.NullString, treating empty strings as NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
