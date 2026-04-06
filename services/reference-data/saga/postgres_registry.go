// Package saga provides the SagaRegistry implementation backed by PostgreSQL.
package saga

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostgresRegistry implements SagaRegistry using PostgreSQL.
type PostgresRegistry struct {
	pool      *pgxpool.Pool
	validator Validator
	logger    *slog.Logger
}

// NewPostgresRegistry creates a new PostgreSQL-backed saga registry.
// The validator is optional - if nil, activation will succeed without validation.
func NewPostgresRegistry(pool *pgxpool.Pool, validator Validator) *PostgresRegistry {
	return &PostgresRegistry{
		pool:      pool,
		validator: validator,
		logger:    slog.Default().With("component", "saga_registry"),
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
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
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

// scanDefinitionWithFallback scans a single row into a Definition.
func (r *PostgresRegistry) scanDefinitionWithFallback(row pgx.Row) (*Definition, error) {
	var def Definition
	var status string
	var script sql.NullString
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID
	var validationStatus sql.NullString
	var complexityScore, handlerCallCount sql.NullInt64

	err := row.Scan(
		&def.ID, &def.Name, &def.Version,
		&script,
		&status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
		&validationStatus, &complexityScore, &handlerCallCount, &def.ValidatedAt,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID

	if script.Valid {
		def.Script = script.String
	}
	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}
	if validationStatus.Valid {
		def.ValidationStatus = validationStatus.String
	}
	if complexityScore.Valid {
		v := int(complexityScore.Int64)
		def.ComplexityScore = &v
	}
	if handlerCallCount.Valid {
		v := int(handlerCallCount.Int64)
		def.HandlerCallCount = &v
	}

	return &def, nil
}

// scanDefinitionWithFallbackFromRows scans from pgx.Rows into a Definition.
func (r *PostgresRegistry) scanDefinitionWithFallbackFromRows(rows pgx.Rows) (*Definition, error) {
	var def Definition
	var status string
	var script sql.NullString
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID
	var validationStatus sql.NullString
	var complexityScore, handlerCallCount sql.NullInt64

	err := rows.Scan(
		&def.ID, &def.Name, &def.Version,
		&script,
		&status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
		&validationStatus, &complexityScore, &handlerCallCount, &def.ValidatedAt,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID

	if script.Valid {
		def.Script = script.String
	}
	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}
	if validationStatus.Valid {
		def.ValidationStatus = validationStatus.String
	}
	if complexityScore.Valid {
		v := int(complexityScore.Int64)
		def.ComplexityScore = &v
	}
	if handlerCallCount.Valid {
		v := int(handlerCallCount.Int64)
		def.HandlerCallCount = &v
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
