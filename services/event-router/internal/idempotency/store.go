// Package idempotency provides saga-specific idempotency checking for event-triggered sagas.
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sharedidempotency "github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// ErrNilPool is returned when a nil pool is provided.
var ErrNilPool = errors.New("pool cannot be nil")

const (
	// sagaNamespace is the idempotency namespace for saga dispatch operations.
	sagaNamespace = "saga-dispatch"

	// DefaultTTL is how long idempotency records are retained.
	DefaultTTL = 24 * time.Hour

	// DefaultStaleThreshold is how long a PENDING record can exist before being considered stale.
	DefaultStaleThreshold = 1 * time.Hour
)

// Config holds configuration for the SagaIdempotencyStore.
type Config struct {
	// DefaultTTL is how long idempotency records are retained.
	// Default: 24h
	DefaultTTL time.Duration

	// StaleThreshold is how long a PENDING record can exist before being considered stale.
	// Default: 1h
	StaleThreshold time.Duration
}

// DefaultConfig returns the default store configuration.
func DefaultConfig() Config {
	return Config{
		DefaultTTL:     DefaultTTL,
		StaleThreshold: DefaultStaleThreshold,
	}
}

// DispatchFunc is the saga dispatch function protected by idempotency.
type DispatchFunc func(ctx context.Context) error

// SagaIdempotencyStore prevents duplicate saga executions by tracking processed correlation IDs
// using the shared idempotency infrastructure.
type SagaIdempotencyStore struct {
	executor *sharedidempotency.Executor
	config   Config
}

// NewSagaIdempotencyStore creates a new store backed by a PostgreSQL/CockroachDB connection pool.
// It calls EnsureTable to create the _idempotency_keys table if it does not exist.
func NewSagaIdempotencyStore(ctx context.Context, pool *pgxpool.Pool, cfg *Config) (*SagaIdempotencyStore, error) {
	if pool == nil {
		return nil, ErrNilPool
	}

	if cfg == nil {
		defaultCfg := DefaultConfig()
		cfg = &defaultCfg
	}

	svc := sharedidempotency.NewPostgresService(pool)
	if err := svc.EnsureTable(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure idempotency table: %w", err)
	}

	execCfg := sharedidempotency.ExecutorConfig{
		DefaultTTL:         cfg.DefaultTTL,
		MaxDeadlockRetries: 3,
		DeadlockRetryDelay: 50 * time.Millisecond,
	}
	executor := sharedidempotency.NewExecutor(svc, &execCfg)

	return &SagaIdempotencyStore{
		executor: executor,
		config:   *cfg,
	}, nil
}

// buildKey constructs an idempotency key for a saga dispatch.
// namespace=saga-dispatch, operation=sagaName, entityID=correlationID.
func buildKey(sagaName, correlationID string) sharedidempotency.Key {
	return sharedidempotency.Key{
		Namespace: sagaNamespace,
		Operation: sagaName,
		EntityID:  correlationID,
	}
}

// Execute runs fn with idempotency protection keyed on (sagaName, correlationID).
//
// Behavior:
//   - If already completed: fn is not called, returns (result, nil) with FromCache=true.
//   - If in progress: fn is not called, returns (nil, ErrOperationInProgress).
//   - If new: marks PENDING, calls fn, records COMPLETED on success.
//   - On fn error: cleans up PENDING state and returns the error.
func (s *SagaIdempotencyStore) Execute(
	ctx context.Context,
	sagaName, correlationID string,
	fn DispatchFunc,
) (*sharedidempotency.ExecuteResult, error) {
	key := buildKey(sagaName, correlationID)

	return s.executor.Execute(ctx, key, s.config.DefaultTTL, func(ctx context.Context) ([]byte, error) {
		if err := fn(ctx); err != nil {
			return nil, err
		}
		// Store the saga name as the result data so it can be logged on cache hit.
		return []byte(fmt.Sprintf("%q", sagaName)), nil
	})
}
