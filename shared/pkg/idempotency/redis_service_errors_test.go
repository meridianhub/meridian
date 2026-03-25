package idempotency

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupClosedRedis returns a RedisService pointing at a closed miniredis instance.
func setupClosedRedis(t *testing.T) *RedisService {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewRedisService(client)
	t.Cleanup(func() { _ = client.Close() })
	mr.Close()
	return svc
}

// ---- Redis-down error paths ----

func TestRedisService_Check_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	_, err := svc.Check(context.Background(), key)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrResultNotFound, "should be a Redis error, not ErrResultNotFound")
}

func TestRedisService_StoreResult_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	result := Result{
		Key:    key,
		Status: StatusCompleted,
		TTL:    time.Hour,
	}
	err := svc.StoreResult(context.Background(), result)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to store result")
}

func TestRedisService_Delete_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	err := svc.Delete(context.Background(), key)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete key")
}

func TestRedisService_Acquire_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	opts := LockOptions{
		TTL:        30 * time.Second,
		Token:      uuid.NewString(),
		MaxRetries: 0,
	}
	err := svc.Acquire(context.Background(), key, opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to acquire lock")
}

func TestRedisService_Release_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	err := svc.Release(context.Background(), key, uuid.NewString())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to release lock")
}

func TestRedisService_Refresh_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	err := svc.Refresh(context.Background(), key, uuid.NewString(), time.Minute)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh lock")
}

func TestRedisService_IsHeld_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)
	key := testKey()

	_, err := svc.IsHeld(context.Background(), key)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check lock")
}

// ---- Lock refresh with expired lock ----

func TestRedisService_Refresh_ExpiredLock(t *testing.T) {
	svc, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Acquire with a short TTL
	opts := LockOptions{
		TTL:   50 * time.Millisecond,
		Token: token,
	}
	require.NoError(t, svc.Acquire(ctx, key, opts))

	// Expire the lock
	mr.FastForward(100 * time.Millisecond)

	// Refresh should fail because the lock expired
	err := svc.Refresh(ctx, key, token, time.Minute)
	assert.ErrorIs(t, err, ErrLockNotHeld)
}

// ---- Acquire: invalid TTL / empty token ----

func TestRedisService_Acquire_InvalidTTL(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	key := testKey()
	opts := LockOptions{
		TTL:   0,
		Token: uuid.NewString(),
	}

	err := svc.Acquire(context.Background(), key, opts)
	assert.ErrorIs(t, err, ErrInvalidTTL)
}

func TestRedisService_Acquire_EmptyToken(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	key := testKey()
	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: "",
	}

	err := svc.Acquire(context.Background(), key, opts)
	assert.ErrorIs(t, err, ErrEmptyToken)
}

func TestRedisService_Release_EmptyToken(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	key := testKey()
	err := svc.Release(context.Background(), key, "")
	assert.ErrorIs(t, err, ErrEmptyToken)
}

func TestRedisService_Refresh_EmptyToken(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	key := testKey()
	err := svc.Refresh(context.Background(), key, "", time.Minute)
	assert.ErrorIs(t, err, ErrEmptyToken)
}

// ---- ScanStalePendingKeys ----

func TestRedisService_ScanStalePendingKeys_Empty(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	stale, err := svc.ScanStalePendingKeys(context.Background(), "idempotency:result:*", time.Hour, 10)
	require.NoError(t, err)
	assert.Empty(t, stale)
}

func TestRedisService_ScanStalePendingKeys_FindsStaleKey(t *testing.T) {
	svc, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	threshold := 5 * time.Minute

	// Create a PENDING result with a created time in the past
	staleTime := time.Now().Add(-10 * time.Minute)
	result := Result{
		Key:       key,
		Status:    StatusPending,
		CreatedAt: staleTime,
		TTL:       time.Hour,
	}
	require.NoError(t, svc.StoreResult(ctx, result))

	// Fast-forward miniredis clock so TTL doesn't expire
	mr.FastForward(0)

	stale, err := svc.ScanStalePendingKeys(ctx, "idempotency:result:*", threshold, 10)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, key.Namespace, stale[0].Result.Key.Namespace)
	assert.Equal(t, StatusPending, stale[0].Result.Status)
}

func TestRedisService_ScanStalePendingKeys_SkipsCompletedKeys(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Store a COMPLETED result (should not be returned as stale)
	result := Result{
		Key:         key,
		Status:      StatusCompleted,
		CreatedAt:   time.Now().Add(-10 * time.Minute),
		CompletedAt: time.Now(),
		TTL:         time.Hour,
	}
	require.NoError(t, svc.StoreResult(ctx, result))

	stale, err := svc.ScanStalePendingKeys(ctx, "idempotency:result:*", time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, stale)
}

func TestRedisService_ScanStalePendingKeys_RespectsLimit(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	threshold := time.Minute

	// Create 5 stale pending keys
	for i := 0; i < 5; i++ {
		k := Key{
			Namespace: "test",
			Operation: "op",
			EntityID:  uuid.NewString(),
		}
		result := Result{
			Key:       k,
			Status:    StatusPending,
			CreatedAt: time.Now().Add(-2 * time.Minute),
			TTL:       time.Hour,
		}
		require.NoError(t, svc.StoreResult(ctx, result))
	}

	// Request only 3
	stale, err := svc.ScanStalePendingKeys(ctx, "idempotency:result:*", threshold, 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(stale), 3)
}

func TestRedisService_ScanStalePendingKeys_DefaultLimit(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	// Passing limit=0 should use default (100) without panicking
	stale, err := svc.ScanStalePendingKeys(context.Background(), "idempotency:result:*", time.Hour, 0)
	require.NoError(t, err)
	assert.Empty(t, stale)
}

func TestRedisService_ScanStalePendingKeys_ContextCancelled(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.ScanStalePendingKeys(ctx, "idempotency:result:*", time.Hour, 10)
	assert.Error(t, err)
}

// ---- MarkStaleAsFailed ----

func TestRedisService_MarkStaleAsFailed_Success(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	result := Result{
		Key:       key,
		Status:    StatusPending,
		CreatedAt: time.Now().Add(-10 * time.Minute),
		TTL:       time.Hour,
	}
	require.NoError(t, svc.StoreResult(ctx, result))

	staleKey := StalePendingKey{
		RedisKey: "idempotency:result:" + key.String(),
		Result:   &result,
		Age:      10 * time.Minute,
	}

	err := svc.MarkStaleAsFailed(ctx, staleKey, "timeout")
	require.NoError(t, err)

	// Verify it's now FAILED
	retrieved, err := svc.Check(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, retrieved.Status)
}

func TestRedisService_MarkStaleAsFailed_NilResult(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	staleKey := StalePendingKey{
		RedisKey: "idempotency:result:some-key",
		Result:   nil,
	}

	err := svc.MarkStaleAsFailed(context.Background(), staleKey, "timeout")
	assert.ErrorIs(t, err, ErrNilResult)
}

func TestRedisService_MarkStaleAsFailed_KeyAlreadyDeleted(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	result := Result{
		Key:    key,
		Status: StatusPending,
		TTL:    time.Hour,
	}

	// Don't store the key - simulate already deleted
	staleKey := StalePendingKey{
		RedisKey: "idempotency:result:" + key.String(),
		Result:   &result,
		Age:      10 * time.Minute,
	}

	// Should not error - key is already gone (desired state)
	err := svc.MarkStaleAsFailed(ctx, staleKey, "timeout")
	require.NoError(t, err)
}

func TestRedisService_MarkStaleAsFailed_AlreadyCompleted(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Store as COMPLETED
	result := Result{
		Key:         key,
		Status:      StatusCompleted,
		CreatedAt:   time.Now(),
		CompletedAt: time.Now(),
		TTL:         time.Hour,
	}
	require.NoError(t, svc.StoreResult(ctx, result))

	// Try to mark as failed with a stale pending ref
	pendingResult := Result{
		Key:    key,
		Status: StatusPending,
	}
	staleKey := StalePendingKey{
		RedisKey: "idempotency:result:" + key.String(),
		Result:   &pendingResult,
	}

	// Should not error - operation is no longer pending, skip it
	err := svc.MarkStaleAsFailed(ctx, staleKey, "timeout")
	require.NoError(t, err)

	// Verify original status preserved
	retrieved, err := svc.Check(ctx, key)
	require.ErrorIs(t, err, ErrOperationAlreadyProcessed)
	assert.Equal(t, StatusCompleted, retrieved.Status)
}

func TestRedisService_MarkStaleAsFailed_RedisDown(t *testing.T) {
	svc := setupClosedRedis(t)

	result := Result{
		Key:    testKey(),
		Status: StatusPending,
	}
	staleKey := StalePendingKey{
		RedisKey: "idempotency:result:test",
		Result:   &result,
	}

	err := svc.MarkStaleAsFailed(context.Background(), staleKey, "timeout")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read key for update")
}

// ---- Proto conversion: StatusFailed round-trip ----

func TestRedisService_Check_FailedStatus(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	result := Result{
		Key:         key,
		Status:      StatusFailed,
		Error:       "something went wrong",
		CreatedAt:   time.Now(),
		CompletedAt: time.Now(),
		TTL:         time.Hour,
	}
	require.NoError(t, svc.StoreResult(ctx, result))

	retrieved, err := svc.Check(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, StatusFailed, retrieved.Status)
	assert.Equal(t, "something went wrong", retrieved.Error)
}

// ---- Acquire: context cancelled during retry delay ----

// TestRedisService_Acquire_ContextCancelledDuringRetry verifies that a pre-cancelled
// context is respected inside the retry-delay select: the lock is already held by
// another token, the first attempt returns redis.Nil, and ctx.Done() fires immediately
// instead of waiting for RetryDelay.
func TestRedisService_Acquire_ContextCancelledDuringRetry(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	key := testKey()

	// Hold the lock so the second acquire can't succeed on the first attempt.
	opts1 := LockOptions{
		TTL:   30 * time.Second,
		Token: uuid.NewString(),
	}
	require.NoError(t, svc.Acquire(context.Background(), key, opts1))

	// Pre-cancel the context so the retry-delay select fires ctx.Done() immediately.
	ctxCancelled, cancel := context.WithCancel(context.Background())
	cancel()

	opts2 := LockOptions{
		TTL:        30 * time.Second,
		Token:      uuid.NewString(),
		MaxRetries: 5,
		RetryDelay: time.Hour, // long delay so ctx.Done() wins the select
	}
	err := svc.Acquire(ctxCancelled, key, opts2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancel")
}

// ---- Key with TenantID ----

func TestRedisService_KeyWithTenantID(t *testing.T) {
	svc, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := Key{
		TenantID:  "tenant-abc",
		Namespace: "billing",
		Operation: "charge",
		EntityID:  "cust-001",
		RequestID: uuid.NewString(),
	}

	require.NoError(t, svc.MarkPending(ctx, key, time.Hour))

	result, err := svc.Check(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
}
