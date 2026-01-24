// Package saga provides the SagaRegistry implementation backed by PostgreSQL.
package saga

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostgresRegistry implements SagaRegistry using PostgreSQL.
type PostgresRegistry struct {
	pool      *pgxpool.Pool
	validator Validator
}

// NewPostgresRegistry creates a new PostgreSQL-backed saga registry.
// The validator is optional - if nil, activation will succeed without validation.
func NewPostgresRegistry(pool *pgxpool.Pool, validator Validator) *PostgresRegistry {
	return &PostgresRegistry{
		pool:      pool,
		validator: validator,
	}
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

// GetByID retrieves a specific saga by its UUID.
func (r *PostgresRegistry) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM saga_definition
			WHERE id = $1`

		row := tx.QueryRow(ctx, query, id)
		def, err := r.scanDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetDefinition retrieves a specific saga by name and version.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, name string, version int) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM saga_definition
			WHERE name = $1 AND version = $2`

		row := tx.QueryRow(ctx, query, name, version)
		def, err := r.scanDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetActive retrieves the active saga for a name using tenant resolution.
// Resolution order:
//  1. Tenant override (is_system=FALSE, status=ACTIVE, highest version)
//  2. Platform default (is_system=TRUE, status=ACTIVE, highest version)
func (r *PostgresRegistry) GetActive(ctx context.Context, name string) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Step 1: Try tenant override (is_system=FALSE)
		tenantQuery := `
			SELECT id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM saga_definition
			WHERE name = $1 AND status = 'ACTIVE' AND is_system = FALSE
			ORDER BY version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, tenantQuery, name)
		def, err := r.scanDefinition(row)
		if err == nil {
			result = def
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to query tenant saga: %w", err)
		}

		// Step 2: Fall back to platform default (is_system=TRUE)
		platformQuery := `
			SELECT id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM saga_definition
			WHERE name = $1 AND status = 'ACTIVE' AND is_system = TRUE
			ORDER BY version DESC
			LIMIT 1`

		row = tx.QueryRow(ctx, platformQuery, name)
		def, err = r.scanDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query platform saga: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ListByStatus retrieves all sagas with the specified status.
func (r *PostgresRegistry) ListByStatus(ctx context.Context, status Status) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at, activated_at, deprecated_at, successor_id
			FROM saga_definition
			WHERE status = $1
			ORDER BY name, version DESC`

		rows, err := tx.Query(ctx, query, string(status))
		if err != nil {
			return fmt.Errorf("failed to query sagas by status: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanDefinitionFromRows(rows)
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

// CreateDraft creates a new saga definition in DRAFT status.
func (r *PostgresRegistry) CreateDraft(ctx context.Context, def *Definition) error {
	// Reject system saga creation
	if def.IsSystem {
		return ErrSystemSagaReadOnly
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9,
				$10, $11
			)`

		// Generate ID if not set
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}

		now := time.Now()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		_, err := tx.Exec(ctx, query,
			def.ID, def.Name, def.Version, def.Script, string(def.Status), def.IsSystem,
			nullString(def.PreconditionsExpression), nullString(def.DisplayName), nullString(def.Description),
			def.CreatedAt, def.UpdatedAt,
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
		updateQuery := `
			UPDATE saga_definition SET
				script = COALESCE(NULLIF($1, ''), script),
				preconditions_expression = $2,
				display_name = $3,
				description = $4,
				updated_at = $5
			WHERE id = $6 AND updated_at = $7`

		now := time.Now()
		result, err := tx.Exec(ctx, updateQuery,
			updates.Script,
			nullString(updates.PreconditionsExpression),
			nullString(updates.DisplayName),
			nullString(updates.Description),
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

	if saga.Status != StatusDraft {
		return ErrNotDraft
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

		// Transition to ACTIVE (trigger will set activated_at)
		updateQuery := `
			UPDATE saga_definition SET
				status = 'ACTIVE'
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id)
		if err != nil {
			return fmt.Errorf("failed to activate saga: %w", err)
		}

		return nil
	})
}

// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
func (r *PostgresRegistry) DeprecateSaga(ctx context.Context, id uuid.UUID, successorID *uuid.UUID) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// Check current state
		var isSystem bool
		var currentStatus string

		checkQuery := `SELECT is_system, status FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&isSystem, &currentStatus)
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

		// Transition to DEPRECATED with optional successor
		// The database trigger will validate the successor and set deprecated_at
		updateQuery := `
			UPDATE saga_definition SET
				status = 'DEPRECATED',
				successor_id = $2
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id, successorID)
		if err != nil {
			// Check if the error is from the successor validation trigger
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "P0001" { // raise_exception
				msg := pgErr.Message
				msgLower := strings.ToLower(msg)
				switch {
				case strings.Contains(msgLower, "successor"):
					return ErrSuccessorInvalid
				case strings.Contains(msgLower, "cannot transition"):
					return ErrInvalidStateTransition
				case strings.Contains(msgLower, "cannot modify"):
					return ErrInvalidStatus
				default:
					return fmt.Errorf("%w: %s", ErrInvalidStatus, msg)
				}
			}
			return fmt.Errorf("failed to deprecate saga: %w", err)
		}

		return nil
	})
}

// scanDefinition scans a single row into a Definition.
func (r *PostgresRegistry) scanDefinition(row pgx.Row) (*Definition, error) {
	var def Definition
	var status string
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID

	err := row.Scan(
		&def.ID, &def.Name, &def.Version, &def.Script, &status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID

	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}

	return &def, nil
}

// scanDefinitionFromRows scans from pgx.Rows.
func (r *PostgresRegistry) scanDefinitionFromRows(rows pgx.Rows) (*Definition, error) {
	var def Definition
	var status string
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID

	err := rows.Scan(
		&def.ID, &def.Name, &def.Version, &def.Script, &status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID

	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
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
