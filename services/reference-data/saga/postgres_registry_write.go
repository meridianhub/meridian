package saga

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// resolveVersionForTenantOverride bumps the version if a system saga occupies
// the requested version slot. This allows tenant overrides to coexist with
// system sagas that occupy version 1 for the same name in the tenant schema.
func resolveVersionForTenantOverride(ctx context.Context, tx pgx.Tx, def *Definition) error {
	var systemVersion sql.NullInt64
	err := tx.QueryRow(ctx,
		`SELECT version FROM saga_definition WHERE name = $1 AND version = $2 AND is_system = true`,
		def.Name, def.Version,
	).Scan(&systemVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to check system saga version: %w", err)
	}
	if !systemVersion.Valid {
		return nil
	}

	// System saga occupies this version slot; find next available
	var maxVersion sql.NullInt64
	err = tx.QueryRow(ctx,
		`SELECT MAX(version) FROM saga_definition WHERE name = $1`, def.Name,
	).Scan(&maxVersion)
	if err != nil {
		return fmt.Errorf("failed to check existing versions: %w", err)
	}
	if maxVersion.Valid {
		def.Version = int(maxVersion.Int64) + 1
	}
	return nil
}

// CreateDraft creates a new saga definition in DRAFT status.
func (r *PostgresRegistry) CreateDraft(ctx context.Context, def *Definition) error {
	// Reject system saga creation
	if def.IsSystem {
		return ErrSystemSagaReadOnly
	}

	// Script is required
	if def.Script == "" {
		return ErrScriptRequired
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// Generate ID if not set
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}

		now := time.Now()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		if err := resolveVersionForTenantOverride(ctx, tx, def); err != nil {
			return err
		}

		query := `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at,
				validation_status, complexity_score, handler_call_count, validated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9,
				$10, $11,
				$12, $13, $14, $15
			)`

		// Default validation_status to UNVALIDATED if not set
		validationStatus := def.ValidationStatus
		if validationStatus == "" {
			validationStatus = "UNVALIDATED"
		}

		_, err := tx.Exec(ctx, query,
			def.ID, def.Name, def.Version, def.Script, string(def.Status), def.IsSystem,
			nullString(def.PreconditionsExpression), nullString(def.DisplayName), nullString(def.Description),
			def.CreatedAt, def.UpdatedAt,
			validationStatus, def.ComplexityScore, def.HandlerCallCount, def.ValidatedAt,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert saga definition: %w", err)
		}

		return nil
	})
}

// UpdateDefinition updates a DRAFT saga definition.
func (r *PostgresRegistry) UpdateDefinition(ctx context.Context, id uuid.UUID, updates *Definition) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// First, check if the saga exists and is not a system saga
		var isSystem bool
		var currentStatus string
		var currentUpdatedAt time.Time

		checkQuery := `SELECT is_system, status, updated_at FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&isSystem, &currentStatus, &currentUpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if isSystem {
			return ErrSystemSagaReadOnly
		}

		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		// Update the saga
		// Use COALESCE(NULLIF(...)) pattern to preserve existing values when empty string is passed
		updateQuery := `
			UPDATE saga_definition SET
				script = COALESCE(NULLIF($1, ''), script),
				preconditions_expression = COALESCE(NULLIF($2, ''), preconditions_expression),
				display_name = COALESCE(NULLIF($3, ''), display_name),
				description = COALESCE(NULLIF($4, ''), description),
				updated_at = $5
			WHERE id = $6 AND updated_at = $7`

		now := time.Now()
		result, err := tx.Exec(ctx, updateQuery,
			updates.Script,
			updates.PreconditionsExpression,
			updates.DisplayName,
			updates.Description,
			now,
			id, currentUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to update saga definition: %w", err)
		}

		if result.RowsAffected() == 0 {
			return ErrOptimisticLock
		}

		return nil
	})
}

// ActivateSaga transitions a saga from DRAFT to ACTIVE.
func (r *PostgresRegistry) ActivateSaga(ctx context.Context, id uuid.UUID) error {
	// First get the saga to validate it
	saga, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if saga.IsSystem {
		return ErrSystemSagaReadOnly
	}

	if saga.Status == StatusActive {
		return nil // idempotent
	}
	if saga.Status != StatusDraft && saga.Status != StatusDeprecated {
		return ErrNotDraft
	}

	// Reject activation if no script is set
	if saga.Script == "" {
		return ErrScriptRequired
	}

	// Validate the saga if a validator is configured
	if r.validator != nil {
		if err := r.validator.Validate(ctx, saga); err != nil {
			return errors.Join(ErrValidationFailed, err)
		}
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// Re-check status within transaction
		var currentStatus string
		checkQuery := `SELECT status FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&currentStatus)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		// Transition to ACTIVE and record activation timestamp
		updateQuery := `
			UPDATE saga_definition SET
				status = 'ACTIVE',
				activated_at = now(),
				updated_at = now()
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id)
		if err != nil {
			return fmt.Errorf("failed to activate saga: %w", err)
		}

		return nil
	})
}

// validateSagaSuccessor checks that the successor exists, is ACTIVE,
// has the same name as the source saga, and is not self-referential.
func validateSagaSuccessor(ctx context.Context, tx pgx.Tx, sagaID uuid.UUID, sagaName string, successorID uuid.UUID) error {
	if successorID == sagaID {
		return ErrSuccessorInvalid
	}

	var successorStatus, successorName string
	err := tx.QueryRow(ctx,
		`SELECT status, name FROM saga_definition WHERE id = $1`, successorID,
	).Scan(&successorStatus, &successorName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSuccessorInvalid
		}
		return fmt.Errorf("failed to validate successor: %w", err)
	}

	if successorStatus != string(StatusActive) || successorName != sagaName {
		return ErrSuccessorInvalid
	}
	return nil
}

// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
// If successorID is provided, validates that the successor exists, is ACTIVE,
// has the same name, and is not a self-reference.
func (r *PostgresRegistry) DeprecateSaga(ctx context.Context, id uuid.UUID, successorID *uuid.UUID) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		var isSystem bool
		var currentStatus string
		var sagaName string

		checkQuery := `SELECT is_system, status, name FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&isSystem, &currentStatus, &sagaName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if isSystem {
			return ErrSystemSagaReadOnly
		}
		if currentStatus != string(StatusActive) {
			return ErrNotActive
		}

		if successorID != nil {
			if err := validateSagaSuccessor(ctx, tx, id, sagaName, *successorID); err != nil {
				return err
			}
		}

		updateQuery := `
			UPDATE saga_definition SET
				status = 'DEPRECATED',
				deprecated_at = now(),
				updated_at = now(),
				successor_id = $2
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id, successorID)
		if err != nil {
			return fmt.Errorf("failed to deprecate saga: %w", err)
		}

		return nil
	})
}
