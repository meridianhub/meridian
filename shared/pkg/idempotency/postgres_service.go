package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresService implements Service using PostgreSQL/CockroachDB for distributed
// idempotency and locking. It uses a single _idempotency_keys table for both
// idempotency checking (Checker) and distributed locking (Locker).
type PostgresService struct {
	pool            *pgxpool.Pool
	cleanupInterval time.Duration
}

// PostgresOption configures a PostgresService.
type PostgresOption func(*PostgresService)

// WithCleanupInterval sets the interval for the TTL cleanup goroutine.
func WithCleanupInterval(d time.Duration) PostgresOption {
	return func(s *PostgresService) {
		s.cleanupInterval = d
	}
}

// NewPostgresService creates a new PostgreSQL/CockroachDB-based idempotency service.
func NewPostgresService(pool *pgxpool.Pool, opts ...PostgresOption) *PostgresService {
	s := &PostgresService{
		pool:            pool,
		cleanupInterval: 60 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnsureTable creates the _idempotency_keys table and index if they do not exist.
// Safe to call multiple times (idempotent).
func (s *PostgresService) EnsureTable(ctx context.Context) error {
	createTable := `
		CREATE TABLE IF NOT EXISTS _idempotency_keys (
			key            TEXT PRIMARY KEY,
			status         TEXT NOT NULL,
			result         JSONB,
			token          TEXT,
			expires_at     TIMESTAMPTZ,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`

	if _, err := s.pool.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("failed to create _idempotency_keys table: %w", err)
	}

	createIndex := `
		CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at
		ON _idempotency_keys (expires_at) WHERE expires_at IS NOT NULL`

	if _, err := s.pool.Exec(ctx, createIndex); err != nil {
		return fmt.Errorf("failed to create expires_at index: %w", err)
	}

	return nil
}

// Check verifies if an operation has already been processed.
// Returns ErrOperationAlreadyProcessed if the operation was already completed.
// Returns ErrResultNotFound if no record exists for the key.
func (s *PostgresService) Check(ctx context.Context, key Key) (*Result, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}

	dbKey := key.String()

	var status string
	var resultJSON []byte
	var expiresAt *time.Time
	var createdAt time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT status, result, expires_at, created_at FROM _idempotency_keys WHERE key = $1`,
		dbKey,
	).Scan(&status, &resultJSON, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrResultNotFound
		}
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Check TTL expiry
	if expiresAt != nil && time.Now().After(*expiresAt) {
		// Expired: clean up and report not found.
		// Guard the DELETE with expires_at < NOW() to avoid removing a record
		// that was refreshed by another process between our SELECT and DELETE.
		_, _ = s.pool.Exec(ctx,
			`DELETE FROM _idempotency_keys WHERE key = $1 AND expires_at IS NOT NULL AND expires_at < NOW()`,
			dbKey,
		)
		return nil, ErrResultNotFound
	}

	result := &Result{
		Key:       key,
		Status:    OperationStatus(status),
		CreatedAt: createdAt,
	}

	if resultJSON != nil {
		result.Data = resultJSON
	}

	if result.Status == StatusCompleted {
		return result, ErrOperationAlreadyProcessed
	}

	return result, nil
}

// MarkPending marks an operation as in-progress.
// Uses INSERT ON CONFLICT DO NOTHING so concurrent calls don't error.
func (s *PostgresService) MarkPending(ctx context.Context, key Key, ttl time.Duration) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if ttl <= 0 {
		return ErrInvalidTTL
	}

	dbKey := key.String()
	expiresAt := time.Now().Add(ttl)

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO _idempotency_keys (key, status, expires_at) VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO NOTHING`,
		dbKey, string(StatusPending), expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to mark pending: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Key already existed - atomically replace it only if it's expired.
		// The WHERE clause guards against race conditions where another writer
		// refreshes the row between our INSERT and this UPDATE.
		_, err = s.pool.Exec(ctx,
			`UPDATE _idempotency_keys SET status = $1, expires_at = $2, result = NULL, token = NULL, created_at = CURRENT_TIMESTAMP
			 WHERE key = $3 AND expires_at IS NOT NULL AND expires_at < NOW()`,
			string(StatusPending), expiresAt, dbKey,
		)
		if err != nil {
			return fmt.Errorf("failed to replace expired key: %w", err)
		}
	}

	return nil
}

// StoreResult saves the operation result for future idempotency checks.
func (s *PostgresService) StoreResult(ctx context.Context, result Result) error {
	if err := result.Key.Validate(); err != nil {
		return err
	}
	if result.TTL <= 0 {
		return ErrInvalidTTL
	}

	dbKey := result.Key.String()
	expiresAt := time.Now().Add(result.TTL)

	var resultJSON []byte
	if result.Data != nil {
		// Validate that Data is valid JSON for the JSONB column
		if !json.Valid(result.Data) {
			// Wrap the raw bytes as a JSON string
			var err error
			resultJSON, err = json.Marshal(string(result.Data))
			if err != nil {
				return fmt.Errorf("failed to marshal result data: %w", err)
			}
		} else {
			resultJSON = result.Data
		}
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO _idempotency_keys (key, status, result, expires_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET status = $2, result = $3, expires_at = $4`,
		dbKey, string(result.Status), resultJSON, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to store result: %w", err)
	}

	return nil
}

// Delete removes an idempotency record.
func (s *PostgresService) Delete(ctx context.Context, key Key) error {
	if err := key.Validate(); err != nil {
		return err
	}

	dbKey := key.String()
	_, err := s.pool.Exec(ctx, `DELETE FROM _idempotency_keys WHERE key = $1`, dbKey)
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}

	return nil
}

// lockKey returns the lock row key for a given idempotency key.
func lockKey(key Key) string {
	return key.String() + "__lock"
}

// Acquire attempts to acquire a distributed lock using row-level locking.
// It inserts a lock row and uses SELECT FOR UPDATE SKIP LOCKED to ensure mutual exclusion.
func (s *PostgresService) Acquire(ctx context.Context, key Key, opts LockOptions) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if opts.TTL <= 0 {
		return ErrInvalidTTL
	}
	if opts.Token == "" {
		return ErrEmptyToken
	}

	lk := lockKey(key)
	expiresAt := time.Now().Add(opts.TTL)

	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		acquired, err := s.tryAcquire(ctx, lk, opts.Token, expiresAt)
		if err != nil {
			return fmt.Errorf("failed to acquire lock: %w", err)
		}
		if acquired {
			return nil
		}

		if attempt < opts.MaxRetries {
			select {
			case <-ctx.Done():
				return fmt.Errorf("lock acquisition cancelled: %w", ctx.Err())
			case <-time.After(opts.RetryDelay):
				continue
			}
		}
	}

	return ErrLockAcquisitionFailed
}

// tryAcquire attempts a single lock acquisition within a transaction.
func (s *PostgresService) tryAcquire(ctx context.Context, lk, token string, expiresAt time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit returns ErrTxClosed, safe to ignore

	// First, clean up any expired lock
	_, err = tx.Exec(ctx,
		`DELETE FROM _idempotency_keys WHERE key = $1 AND expires_at IS NOT NULL AND expires_at < NOW()`,
		lk,
	)
	if err != nil {
		return false, fmt.Errorf("failed to clean expired lock: %w", err)
	}

	// Try to insert a new lock row. If it already exists (and isn't expired, since
	// we just cleaned expired ones), the ON CONFLICT DO NOTHING means 0 rows affected.
	tag, err := tx.Exec(ctx,
		`INSERT INTO _idempotency_keys (key, status, token, expires_at)
		 VALUES ($1, 'lock', $2, $3)
		 ON CONFLICT (key) DO NOTHING`,
		lk, token, expiresAt,
	)
	if err != nil {
		return false, fmt.Errorf("failed to insert lock row: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Lock row exists and is not expired - someone else holds it.
		// Try SELECT FOR UPDATE SKIP LOCKED to see if we can grab it.
		var existingToken string
		err = tx.QueryRow(ctx,
			`SELECT token FROM _idempotency_keys WHERE key = $1 FOR UPDATE SKIP LOCKED`,
			lk,
		).Scan(&existingToken)
		if err != nil {
			// ErrNoRows means the row is locked by another transaction
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, fmt.Errorf("failed to check lock: %w", err)
		}
		// We got the row - someone else's lock. We can't take it.
		return false, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit lock: %w", err)
	}

	return true, nil
}

// Release releases a previously acquired lock.
// Returns ErrLockNotHeld if the lock is not held by this token.
func (s *PostgresService) Release(ctx context.Context, key Key, token string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if token == "" {
		return ErrEmptyToken
	}

	lk := lockKey(key)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM _idempotency_keys WHERE key = $1 AND token = $2`,
		lk, token,
	)
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// Refresh extends the TTL of a held lock.
// Returns ErrLockNotHeld if the lock is not held by this token.
func (s *PostgresService) Refresh(ctx context.Context, key Key, token string, ttl time.Duration) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if ttl <= 0 {
		return ErrInvalidTTL
	}
	if token == "" {
		return ErrEmptyToken
	}

	lk := lockKey(key)
	newExpiresAt := time.Now().Add(ttl)

	tag, err := s.pool.Exec(ctx,
		`UPDATE _idempotency_keys SET expires_at = $1 WHERE key = $2 AND token = $3`,
		newExpiresAt, lk, token,
	)
	if err != nil {
		return fmt.Errorf("failed to refresh lock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// IsHeld checks if a lock is currently held (by any token).
func (s *PostgresService) IsHeld(ctx context.Context, key Key) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}

	lk := lockKey(key)
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM _idempotency_keys WHERE key = $1 AND (expires_at IS NULL OR expires_at > NOW()))`,
		lk,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check lock: %w", err)
	}

	return exists, nil
}

// StartCleanup runs a background goroutine that periodically deletes expired rows.
// It stops when the context is cancelled. If cleanupInterval is zero or negative,
// this is a no-op.
func (s *PostgresService) StartCleanup(ctx context.Context) {
	if s.cleanupInterval <= 0 {
		return
	}
	ticker := time.NewTicker(s.cleanupInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.pool.Exec(ctx,
					`DELETE FROM _idempotency_keys WHERE expires_at IS NOT NULL AND expires_at < NOW()`,
				)
			}
		}
	}()
}

// Verify interface compliance at compile time.
var _ Service = (*PostgresService)(nil)
