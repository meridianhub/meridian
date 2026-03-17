package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PgxQuerier is the minimal interface for pgx query operations.
// Both *pgxpool.Pool and pgx.Tx satisfy this interface.
type PgxQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GuardedPgxPool wraps a *pgxpool.Pool and enforces tenant context on all
// query operations. If a query is made without tenant context (and without
// a bypass), the operation is rejected with ErrPgxTenantContextRequired.
//
// For operations that need tenant schema isolation (via SET LOCAL search_path),
// callers should use BeginTenantTx which starts a transaction with the
// tenant's schema already set.
type GuardedPgxPool struct {
	pool *pgxpool.Pool
}

// NewGuardedPgxPool creates a GuardedPgxPool that enforces tenant context
// on all pgx operations.
func NewGuardedPgxPool(pool *pgxpool.Pool) *GuardedPgxPool {
	return &GuardedPgxPool{pool: pool}
}

// Exec executes a query that doesn't return rows.
// Requires tenant context or bypass in ctx.
func (g *GuardedPgxPool) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if _, err := RequirePgxTenantContext(ctx); err != nil {
		return pgconn.CommandTag{}, err
	}
	return g.pool.Exec(ctx, sql, arguments...)
}

// Query executes a query that returns rows.
// Requires tenant context or bypass in ctx.
func (g *GuardedPgxPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if _, err := RequirePgxTenantContext(ctx); err != nil {
		return nil, err
	}
	return g.pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that returns at most one row.
// Requires tenant context or bypass in ctx.
//
// Note: pgx.Row doesn't support returning errors from QueryRow directly,
// so the guard check wraps the result in an errorRow on failure.
func (g *GuardedPgxPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if _, err := RequirePgxTenantContext(ctx); err != nil {
		return &errorRow{err: err}
	}
	return g.pool.QueryRow(ctx, sql, args...)
}

// Begin starts a transaction with tenant context validation.
// Requires tenant context or bypass in ctx.
func (g *GuardedPgxPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if _, err := RequirePgxTenantContext(ctx); err != nil {
		return nil, err
	}
	return g.pool.Begin(ctx)
}

// BeginTenantTx starts a transaction and sets the search_path to the tenant's
// schema. This is the recommended way to execute tenant-scoped pgx queries
// that need schema isolation.
//
// The caller is responsible for committing or rolling back the transaction.
func (g *GuardedPgxPool) BeginTenantTx(ctx context.Context) (pgx.Tx, error) {
	tid, ok := tenant.FromContext(ctx)
	if !ok || tid.IsEmpty() {
		return nil, fmt.Errorf("%w", ErrPgxTenantContextRequired)
	}

	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tenant tx: %w", err)
	}

	schemaName := pq.QuoteIdentifier(tid.SchemaName())
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set tenant schema: %w", err)
	}

	return tx, nil
}

// Pool returns the underlying *pgxpool.Pool for cases where direct access
// is needed (e.g., Close, Stat). The returned pool is NOT guarded.
func (g *GuardedPgxPool) Pool() *pgxpool.Pool {
	return g.pool
}

// Close closes the underlying connection pool.
func (g *GuardedPgxPool) Close() {
	g.pool.Close()
}

// errorRow implements pgx.Row and returns an error on Scan.
// Used when QueryRow fails the tenant guard check.
type errorRow struct {
	err error
}

func (r *errorRow) Scan(_ ...any) error {
	return r.err
}

// Verify GuardedPgxPool implements PgxQuerier at compile time.
var _ PgxQuerier = (*GuardedPgxPool)(nil)
