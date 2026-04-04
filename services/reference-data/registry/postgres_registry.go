// Package registry provides the InstrumentRegistry implementation backed by PostgreSQL.
package registry

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
