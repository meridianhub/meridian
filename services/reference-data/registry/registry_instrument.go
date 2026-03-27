package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
// Idempotent: if the instrument (code, version) already exists, this is a no-op.
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
			)
			ON CONFLICT (code, version) DO NOTHING`

		// Generate ID if not set
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}

		now := time.Now()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		// Default empty attribute_schema to valid JSON - column is jsonb
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

		// Idempotent: already active is a no-op.
		if Status(currentStatus) == StatusActive {
			return nil
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
