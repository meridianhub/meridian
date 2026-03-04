package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sagaidempotency "github.com/meridianhub/meridian/services/event-router/internal/idempotency"
	sharedidempotency "github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

// Shared CockroachDB container for all tests in this package.
var (
	once       sync.Once
	sharedPool *pgxpool.Pool
	initErr    error
)

func getPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		container, err := cockroachdb.Run(ctx,
			"cockroachdb/cockroach:v24.3.0",
			cockroachdb.WithDatabase("test_db"),
			cockroachdb.WithUser("root"),
			cockroachdb.WithInsecure(),
		)
		if err != nil {
			initErr = err
			return
		}

		connConfig, err := container.ConnectionConfig(ctx)
		if err != nil {
			initErr = err
			return
		}

		pool, err := pgxpool.New(ctx, connConfig.ConnString())
		if err != nil {
			initErr = err
			return
		}
		sharedPool = pool
	})

	if initErr != nil {
		t.Fatalf("failed to initialize CockroachDB: %v", initErr)
	}
	return sharedPool
}

func newStore(t *testing.T) *sagaidempotency.SagaIdempotencyStore {
	t.Helper()
	pool := getPool(t)
	store, err := sagaidempotency.NewSagaIdempotencyStore(context.Background(), pool, nil)
	require.NoError(t, err)
	return store
}

func TestNewSagaIdempotencyStore_NilPool(t *testing.T) {
	_, err := sagaidempotency.NewSagaIdempotencyStore(context.Background(), nil, nil)
	require.ErrorIs(t, err, sagaidempotency.ErrNilPool)
}

func TestNewSagaIdempotencyStore_DefaultConfig(t *testing.T) {
	store := newStore(t)
	assert.NotNil(t, store)
}

func TestExecute_FirstCall_RunsFunction(t *testing.T) {
	store := newStore(t)
	sagaName := "test-saga"
	correlationID := uuid.New().String()

	called := false
	result, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		called = true
		return nil
	})

	require.NoError(t, err)
	assert.True(t, called)
	assert.False(t, result.FromCache)
}

func TestExecute_DuplicateCall_SkipsFunction(t *testing.T) {
	store := newStore(t)
	sagaName := "test-saga-dup"
	correlationID := uuid.New().String()

	// First call — executes
	_, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err)

	// Second call — should be from cache
	var callCount int
	result, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		callCount++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 0, callCount, "fn should not be called on duplicate")
	assert.True(t, result.FromCache, "result should be from cache")
}

func TestExecute_DifferentSagaName_SameCorrelationID_ExecutesBoth(t *testing.T) {
	store := newStore(t)
	correlationID := uuid.New().String()

	var calls []string
	_, err := store.Execute(context.Background(), "saga-a", correlationID, func(_ context.Context) error {
		calls = append(calls, "saga-a")
		return nil
	})
	require.NoError(t, err)

	_, err = store.Execute(context.Background(), "saga-b", correlationID, func(_ context.Context) error {
		calls = append(calls, "saga-b")
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"saga-a", "saga-b"}, calls, "different saga names should be independent")
}

func TestExecute_FunctionError_AllowsRetry(t *testing.T) {
	store := newStore(t)
	sagaName := "saga-retry"
	correlationID := uuid.New().String()

	// First call fails
	dispatchErr := errors.New("dispatch failed")
	_, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		return dispatchErr
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, dispatchErr))

	// Second call should succeed (PENDING state was cleaned up)
	var callCount int32
	result, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount, "fn should be called after failed attempt")
	assert.False(t, result.FromCache)
}

func TestExecute_InProgress_ReturnsErrOperationInProgress(t *testing.T) {
	store := newStore(t)
	sagaName := "saga-in-progress"
	correlationID := uuid.New().String()

	// Block the first execution
	blockCh := make(chan struct{})
	doneCh := make(chan error, 1)

	// pendingCh is closed once the first execution has started (PENDING written to DB).
	pendingCh := make(chan struct{})

	go func() {
		_, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
			close(pendingCh) // signal that PENDING state is now in the DB
			<-blockCh        // hold the PENDING state
			return nil
		})
		doneCh <- err
	}()

	// Wait until the first goroutine has written PENDING to the DB.
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		select {
		case <-pendingCh:
			return true
		default:
			return false
		}
	}), "timed out waiting for PENDING state to be written")

	// Second call should see PENDING and return ErrOperationInProgress
	_, err := store.Execute(context.Background(), sagaName, correlationID, func(_ context.Context) error {
		return nil
	})

	// Unblock the first goroutine
	close(blockCh)
	firstErr := <-doneCh
	require.NoError(t, firstErr)

	// Check the second call got ErrOperationInProgress
	require.Error(t, err)
	assert.True(t, errors.Is(err, sharedidempotency.ErrOperationInProgress),
		"expected ErrOperationInProgress, got: %v", err)
}

func TestExecute_CustomConfig(t *testing.T) {
	pool := getPool(t)
	cfg := &sagaidempotency.Config{
		DefaultTTL: 5 * time.Minute,
	}
	store, err := sagaidempotency.NewSagaIdempotencyStore(context.Background(), pool, cfg)
	require.NoError(t, err)
	assert.NotNil(t, store)

	// Verify it works
	correlationID := uuid.New().String()
	result, err := store.Execute(context.Background(), "custom-saga", correlationID, func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err)
	assert.False(t, result.FromCache)
}
