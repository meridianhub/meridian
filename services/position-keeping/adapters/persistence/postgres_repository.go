// Package persistence provides PostgreSQL persistence implementation for Position Keeping domain.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
)

var (
	// ErrNilLog is returned when a nil log is passed to a repository method
	ErrNilLog = errors.New("log cannot be nil")
	// ErrInvalidLimit is returned when limit is not greater than 0
	ErrInvalidLimit = errors.New("limit must be greater than 0")
	// ErrBulkInsertMismatch is returned when bulk insert count doesn't match expected
	ErrBulkInsertMismatch = errors.New("bulk insert count mismatch")
	// ErrDatabaseIDNotFound is returned when database ID mapping fails
	ErrDatabaseIDNotFound = errors.New("database ID not found for log_id")
)

// PostgresRepository implements domain.FinancialPositionLogRepository using PostgreSQL.
// It provides full CRUD operations with connection pooling, bulk operations, and transaction support.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgreSQL repository with the given connection pool.
// The pool should be pre-configured with appropriate connection limits and timeouts.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
// In single-tenant mode (no tenant context), it does nothing.
//
// This must be called immediately after tx.Begin() to ensure all queries
// in the transaction are scoped to the correct tenant schema.
func (r *PostgresRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// Single-tenant mode: no scoping needed
		return nil
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
// In multi-tenant mode, it wraps the function in a transaction with search_path set.
// In single-tenant mode, it still uses a transaction for consistency but without search_path.
// This is necessary because PostgreSQL's SET LOCAL requires a transaction context.
func (r *PostgresRepository) withReadTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Set tenant scope if in multi-tenant mode
	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	// Commit the read-only transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit read transaction: %w", err)
	}

	return nil
}

// nullString converts a pointer to TransactionStatus to sql.NullString for PreviousStatus
func nullString(status *domain.TransactionStatus) sql.NullString {
	if status == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: status.String(), Valid: true}
}

// nullStringValue converts a string to sql.NullString, treating empty strings as NULL
func nullStringValue(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullTime converts a time.Time to sql.NullTime, treating zero time as NULL
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// openingBalanceCurrencyCode returns the currency code for an opening balance,
// defaulting to GBP if the Money value is zero-valued (has no instrument code).
// This ensures we always have a valid currency code in the database.
func openingBalanceCurrencyCode(m domain.Money) string {
	if m.Instrument.Code == "" {
		return string(domain.CurrencyGBP) // Default to GBP for unset opening balance
	}
	return m.Instrument.Code
}

// decimalToCents converts a decimal amount to cents (int64) for database storage.
// This function assumes 2 decimal places which is appropriate for most currencies
// (GBP, USD, EUR, etc.). Note that some currencies have different decimal place
// requirements (e.g., JPY has 0, some cryptocurrencies have more). The domain
// layer currently supports currencies with 2 decimal places as defined in
// domain.Currency constants.
// Example: 123.45 -> 12345 cents
func decimalToCents(d decimal.Decimal) int64 {
	cents := d.Mul(decimal.NewFromInt(100)).RoundBank(0)
	return cents.IntPart()
}

// centsToDecimal converts cents (int64) from database storage to a decimal amount.
// This function assumes 2 decimal places which is appropriate for most currencies
// (GBP, USD, EUR, etc.). See decimalToCents for currency decimal place notes.
// Example: 12345 cents -> 123.45
func centsToDecimal(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}
