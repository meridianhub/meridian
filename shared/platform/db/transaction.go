package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// IsolationLevel represents transaction isolation levels supported by CockroachDB and YugabyteDB
type IsolationLevel int

const (
	// ReadCommitted allows reading committed data only (may see phantom reads)
	ReadCommitted IsolationLevel = iota
	// RepeatableRead prevents non-repeatable reads (snapshot isolation)
	RepeatableRead
	// Serializable provides full isolation (default for CockroachDB)
	Serializable
)

// TxOptions configures transaction behavior
type TxOptions struct {
	// Isolation level for the transaction (default: Serializable)
	Isolation IsolationLevel
	// ReadOnly indicates if transaction only performs reads
	ReadOnly bool
}

var (
	// ErrNestedTransaction is returned when attempting to start a transaction within a transaction
	ErrNestedTransaction = errors.New("nested transactions are not supported")
	// ErrTransactionRolledBack is returned when transaction was rolled back due to error
	ErrTransactionRolledBack = errors.New("transaction rolled back")
	// ErrPingNotSupported is returned when Ping is called on a transaction
	ErrPingNotSupported = errors.New("ping not supported on transactions")
)

// DefaultTxOptions returns transaction options optimized for CockroachDB
func DefaultTxOptions() *TxOptions {
	return &TxOptions{
		Isolation: Serializable,
		ReadOnly:  false,
	}
}

// WithTransaction executes a function within a database transaction.
// It automatically:
// - Begins a transaction with specified options
// - Commits on success
// - Rolls back on error or panic
// - Recovers from panics and ensures cleanup
//
// The provided function receives a DB interface that wraps the transaction,
// allowing it to be used anywhere a DB is accepted.
//
// Example:
//
//	err := db.WithTransaction(ctx, pool, func(tx db.DB) error {
//	    return repository.Save(tx, account)
//	})
func WithTransaction(ctx context.Context, db DB, fn func(tx DB) error) error {
	return WithTransactionOptions(ctx, db, DefaultTxOptions(), fn)
}

// WithTransactionOptions executes a function within a transaction with custom options.
// See WithTransaction for detailed behavior.
func WithTransactionOptions(ctx context.Context, db DB, opts *TxOptions, fn func(tx DB) error) (err error) {
	// Begin transaction with options
	sqlOpts := toSQLOptions(opts)
	tx, err := db.BeginTx(ctx, sqlOpts)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Wrap transaction to implement DB interface
	txWrapper := &Tx{tx: tx}

	// Defer rollback - will be no-op if commit succeeds
	defer func() {
		if p := recover(); p != nil {
			// Panic occurred, rollback and re-panic
			_ = txWrapper.Rollback()
			panic(p)
		} else if err != nil {
			// Function returned error, rollback
			if rbErr := txWrapper.Rollback(); rbErr != nil {
				err = fmt.Errorf("%w (rollback failed: %w)", err, rbErr)
			}
		}
	}()

	// Execute function
	err = fn(txWrapper)
	if err != nil {
		return err
	}

	// Commit transaction
	if err = txWrapper.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Tx wraps a sql.Tx to implement the DB interface.
// This allows transactions to be passed to functions expecting DB,
// enabling the same repository code to work with both connections and transactions.
type Tx struct {
	tx *sql.Tx
}

// QueryContext executes a query that returns rows
func (t *Tx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("transaction query failed: %w", err)
	}
	return rows, nil
}

// ExecContext executes a query that doesn't return rows
func (t *Tx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	result, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("transaction exec failed: %w", err)
	}
	return result, nil
}

// QueryRowContext executes a query that returns at most one row
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// BeginTx returns an error as nested transactions are not supported
func (t *Tx) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, ErrNestedTransaction
}

// Ping returns an error as transactions don't support ping
func (t *Tx) Ping(_ context.Context) error {
	return ErrPingNotSupported
}

// Close is a no-op for transactions (use Commit/Rollback instead)
func (t *Tx) Close() error {
	return nil
}

// Commit commits the transaction
func (t *Tx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return fmt.Errorf("transaction commit failed: %w", err)
	}
	return nil
}

// Rollback rolls back the transaction
func (t *Tx) Rollback() error {
	if err := t.tx.Rollback(); err != nil {
		// Ignore "already committed/rolled back" errors
		if errors.Is(err, sql.ErrTxDone) {
			return nil
		}
		return fmt.Errorf("transaction rollback failed: %w", err)
	}
	return nil
}

// toSQLOptions converts TxOptions to sql.TxOptions
func toSQLOptions(opts *TxOptions) *sql.TxOptions {
	if opts == nil {
		opts = DefaultTxOptions()
	}

	sqlOpts := &sql.TxOptions{
		ReadOnly: opts.ReadOnly,
	}

	switch opts.Isolation {
	case ReadCommitted:
		sqlOpts.Isolation = sql.LevelReadCommitted
	case RepeatableRead:
		sqlOpts.Isolation = sql.LevelRepeatableRead
	case Serializable:
		sqlOpts.Isolation = sql.LevelSerializable
	default:
		sqlOpts.Isolation = sql.LevelSerializable
	}

	return sqlOpts
}
