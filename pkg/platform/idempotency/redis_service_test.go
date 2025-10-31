package idempotency

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func setupRedisService(t *testing.T) (*RedisService, *miniredis.Miniredis, func()) {
	t.Helper()

	// Start miniredis server
	mr := miniredis.RunT(t)

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	service := NewRedisService(client)

	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}

	return service, mr, cleanup
}

func testKey() Key {
	return Key{
		Namespace: "current-account",
		Operation: "deposit",
		EntityID:  "ACC-12345",
		RequestID: uuid.NewString(),
	}
}

// Test Checker interface implementation

func TestRedisService_Check_NotFound(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	result, err := service.Check(ctx, key)

	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
	if !errors.Is(err, ErrResultNotFound) {
		t.Errorf("Expected ErrResultNotFound, got %v", err)
	}
}

func TestRedisService_Check_CompletedOperation(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Store a completed result
	completedResult := Result{
		Key:         key,
		Status:      StatusCompleted,
		Data:        []byte(`{"amount": 100}`),
		CompletedAt: time.Now(),
		TTL:         time.Hour,
	}
	if err := service.StoreResult(ctx, completedResult); err != nil {
		t.Fatalf("Failed to store result: %v", err)
	}

	// Check should return the result and ErrOperationAlreadyProcessed
	result, err := service.Check(ctx, key)

	if !errors.Is(err, ErrOperationAlreadyProcessed) {
		t.Errorf("Expected ErrOperationAlreadyProcessed, got %v", err)
	}
	if result == nil {
		t.Fatal("Expected result to be non-nil")
	}
	if result.Status != StatusCompleted {
		t.Errorf("Expected status %v, got %v", StatusCompleted, result.Status)
	}
}

func TestRedisService_Check_PendingOperation(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Mark operation as pending
	if err := service.MarkPending(ctx, key, time.Hour); err != nil {
		t.Fatalf("Failed to mark pending: %v", err)
	}

	// Check should return pending result without error
	result, err := service.Check(ctx, key)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("Expected result to be non-nil")
	}
	if result.Status != StatusPending {
		t.Errorf("Expected status %v, got %v", StatusPending, result.Status)
	}
}

func TestRedisService_MarkPending(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	err := service.MarkPending(ctx, key, 30*time.Second)
	if err != nil {
		t.Errorf("MarkPending failed: %v", err)
	}

	// Verify key exists in Redis
	redisKey := "idempotency:result:" + key.String()
	if !mr.Exists(redisKey) {
		t.Error("Expected key to exist in Redis")
	}

	// Verify TTL is set
	ttl := mr.TTL(redisKey)
	if ttl <= 0 || ttl > 30*time.Second {
		t.Errorf("Expected TTL around 30s, got %v", ttl)
	}
}

func TestRedisService_StoreResult(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	result := Result{
		Key:         key,
		Status:      StatusCompleted,
		Data:        []byte(`{"result": "success"}`),
		Error:       "",
		CompletedAt: time.Now(),
		TTL:         24 * time.Hour,
	}

	err := service.StoreResult(ctx, result)
	if err != nil {
		t.Errorf("StoreResult failed: %v", err)
	}

	// Verify key exists
	redisKey := "idempotency:result:" + key.String()
	if !mr.Exists(redisKey) {
		t.Error("Expected key to exist in Redis")
	}
}

func TestRedisService_Delete(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Store a result first
	if err := service.MarkPending(ctx, key, time.Hour); err != nil {
		t.Fatalf("Failed to mark pending: %v", err)
	}

	// Verify it exists
	redisKey := "idempotency:result:" + key.String()
	if !mr.Exists(redisKey) {
		t.Fatal("Expected key to exist before deletion")
	}

	// Delete
	if err := service.Delete(ctx, key); err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	// Verify it's gone
	if mr.Exists(redisKey) {
		t.Error("Expected key to be deleted from Redis")
	}
}

// Test Locker interface implementation

func TestRedisService_Acquire_Success(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      token,
	}

	err := service.Acquire(ctx, key, opts)
	if err != nil {
		t.Errorf("Acquire failed: %v", err)
	}

	// Verify lock exists
	lockKey := "idempotency:lock:" + key.String()
	if !mr.Exists(lockKey) {
		t.Error("Expected lock to exist in Redis")
	}

	// Verify token is stored
	storedToken, _ := mr.Get(lockKey)
	if storedToken != token {
		t.Errorf("Expected token %s, got %s", token, storedToken)
	}
}

func TestRedisService_Acquire_AlreadyHeld(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Acquire lock with first token
	opts1 := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      uuid.NewString(),
	}
	if err := service.Acquire(ctx, key, opts1); err != nil {
		t.Fatalf("First acquire failed: %v", err)
	}

	// Try to acquire with second token (should fail)
	opts2 := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      uuid.NewString(),
	}
	err := service.Acquire(ctx, key, opts2)

	if !errors.Is(err, ErrLockAcquisitionFailed) {
		t.Errorf("Expected ErrLockAcquisitionFailed, got %v", err)
	}
}

func TestRedisService_Acquire_WithRetries(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()

	// Acquire lock with first token
	token1 := uuid.NewString()
	opts1 := LockOptions{
		TTL:        100 * time.Millisecond, // Short TTL
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      token1,
	}
	if err := service.Acquire(ctx, key, opts1); err != nil {
		t.Fatalf("First acquire failed: %v", err)
	}

	// Fast-forward time to expire the lock
	mr.FastForward(200 * time.Millisecond)

	// Try to acquire with second token after expiration
	token2 := uuid.NewString()
	opts2 := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 3,
		Token:      token2,
	}
	err := service.Acquire(ctx, key, opts2)
	if err != nil {
		t.Errorf("Second acquire should succeed after expiration, got %v", err)
	}
}

func TestRedisService_Release_Success(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Acquire lock
	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := service.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Release lock
	err := service.Release(ctx, key, token)
	if err != nil {
		t.Errorf("Release failed: %v", err)
	}

	// Verify lock is gone
	lockKey := "idempotency:lock:" + key.String()
	if mr.Exists(lockKey) {
		t.Error("Expected lock to be released")
	}
}

func TestRedisService_Release_WrongToken(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Acquire lock
	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := service.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Try to release with wrong token
	wrongToken := uuid.NewString()
	err := service.Release(ctx, key, wrongToken)

	if !errors.Is(err, ErrLockNotHeld) {
		t.Errorf("Expected ErrLockNotHeld, got %v", err)
	}
}

func TestRedisService_Refresh_Success(t *testing.T) {
	service, mr, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Acquire lock with short TTL
	opts := LockOptions{
		TTL:   5 * time.Second,
		Token: token,
	}
	if err := service.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Refresh with longer TTL
	newTTL := 60 * time.Second
	err := service.Refresh(ctx, key, token, newTTL)
	if err != nil {
		t.Errorf("Refresh failed: %v", err)
	}

	// Verify new TTL
	lockKey := "idempotency:lock:" + key.String()
	ttl := mr.TTL(lockKey)
	if ttl < 55*time.Second || ttl > 60*time.Second {
		t.Errorf("Expected TTL around 60s, got %v", ttl)
	}
}

func TestRedisService_Refresh_WrongToken(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Acquire lock
	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := service.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Try to refresh with wrong token
	wrongToken := uuid.NewString()
	err := service.Refresh(ctx, key, wrongToken, time.Minute)

	if !errors.Is(err, ErrLockNotHeld) {
		t.Errorf("Expected ErrLockNotHeld, got %v", err)
	}
}

func TestRedisService_IsHeld(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	key := testKey()
	token := uuid.NewString()

	// Check before acquiring
	held, err := service.IsHeld(ctx, key)
	if err != nil {
		t.Errorf("IsHeld failed: %v", err)
	}
	if held {
		t.Error("Expected lock to not be held initially")
	}

	// Acquire lock
	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := service.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Check after acquiring
	held, err = service.IsHeld(ctx, key)
	if err != nil {
		t.Errorf("IsHeld failed: %v", err)
	}
	if !held {
		t.Error("Expected lock to be held after acquisition")
	}

	// Release lock
	if err := service.Release(ctx, key, token); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Check after releasing
	held, err = service.IsHeld(ctx, key)
	if err != nil {
		t.Errorf("IsHeld failed: %v", err)
	}
	if held {
		t.Error("Expected lock to not be held after release")
	}
}

// Test validation

func TestRedisService_InvalidKey(t *testing.T) {
	service, _, cleanup := setupRedisService(t)
	defer cleanup()

	ctx := context.Background()
	invalidKey := Key{} // Missing required fields

	// All methods should return ErrInvalidKey
	_, err := service.Check(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Check: expected ErrInvalidKey, got %v", err)
	}

	err = service.MarkPending(ctx, invalidKey, time.Hour)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("MarkPending: expected ErrInvalidKey, got %v", err)
	}

	err = service.Delete(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Delete: expected ErrInvalidKey, got %v", err)
	}

	opts := DefaultLockOptions()
	opts.Token = uuid.NewString()
	err = service.Acquire(ctx, invalidKey, opts)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Acquire: expected ErrInvalidKey, got %v", err)
	}

	err = service.Release(ctx, invalidKey, "token")
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Release: expected ErrInvalidKey, got %v", err)
	}

	_, err = service.IsHeld(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("IsHeld: expected ErrInvalidKey, got %v", err)
	}
}
