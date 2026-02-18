package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLazyService_BeforeResolution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a LazyClient that never resolves (always fails)
	lazy := bootstrap.NewLazyClient(ctx, "test-redis",
		func(_ context.Context) (*redis.Client, func(), error) {
			return nil, nil, context.DeadlineExceeded
		},
		bootstrap.WithLazyInitialWait(1*time.Hour), // Very long wait so it never resolves
	)

	svc := idempotency.NewLazyService(lazy)

	// Before resolution, service should degrade gracefully
	assert.False(t, svc.IsReady())

	key := idempotency.Key{
		Namespace: "test",
		Operation: "op",
		EntityID:  "123",
	}

	// Check returns ErrResultNotFound (allows request to proceed)
	_, err := svc.Check(context.Background(), key)
	assert.ErrorIs(t, err, idempotency.ErrResultNotFound)

	// All mutating operations are no-op
	assert.NoError(t, svc.MarkPending(context.Background(), key, time.Minute))
	assert.NoError(t, svc.StoreResult(context.Background(), idempotency.Result{}))
	assert.NoError(t, svc.Delete(context.Background(), key))
	assert.NoError(t, svc.Acquire(context.Background(), key, idempotency.LockOptions{}))
	assert.NoError(t, svc.Release(context.Background(), key, "token"))
	assert.NoError(t, svc.Refresh(context.Background(), key, "token", time.Minute))

	held, err := svc.IsHeld(context.Background(), key)
	assert.NoError(t, err)
	assert.False(t, held)
}

func TestLazyService_ImplementsServiceInterface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lazy := bootstrap.NewLazyClient(ctx, "test-redis",
		func(_ context.Context) (*redis.Client, func(), error) {
			return nil, nil, context.DeadlineExceeded
		},
		bootstrap.WithLazyInitialWait(1*time.Hour),
	)

	svc := idempotency.NewLazyService(lazy)

	// Verify it satisfies the Service interface
	var _ idempotency.Service = svc
	require.NotNil(t, svc)
}
