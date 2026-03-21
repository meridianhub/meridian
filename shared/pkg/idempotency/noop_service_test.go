package idempotency

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopService(t *testing.T) {
	logger := slog.Default()
	svc := NewNoopService(logger)
	require.NotNil(t, svc)

	ctx := context.Background()
	key := Key{
		Namespace: "test",
		Operation: "op",
		EntityID:  "123",
	}

	t.Run("Check returns ErrResultNotFound", func(t *testing.T) {
		result, err := svc.Check(ctx, key)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrResultNotFound)
	})

	t.Run("MarkPending is no-op", func(t *testing.T) {
		err := svc.MarkPending(ctx, key, 30*time.Second)
		assert.NoError(t, err)
	})

	t.Run("StoreResult is no-op", func(t *testing.T) {
		err := svc.StoreResult(ctx, Result{Key: key, Status: StatusCompleted})
		assert.NoError(t, err)
	})

	t.Run("Delete is no-op", func(t *testing.T) {
		err := svc.Delete(ctx, key)
		assert.NoError(t, err)
	})

	t.Run("Acquire always succeeds", func(t *testing.T) {
		err := svc.Acquire(ctx, key, LockOptions{TTL: 10 * time.Second})
		assert.NoError(t, err)
	})

	t.Run("Release is no-op", func(t *testing.T) {
		err := svc.Release(ctx, key, "token")
		assert.NoError(t, err)
	})

	t.Run("Refresh is no-op", func(t *testing.T) {
		err := svc.Refresh(ctx, key, "token", 10*time.Second)
		assert.NoError(t, err)
	})

	t.Run("IsHeld always returns false", func(t *testing.T) {
		held, err := svc.IsHeld(ctx, key)
		assert.NoError(t, err)
		assert.False(t, held)
	})
}
