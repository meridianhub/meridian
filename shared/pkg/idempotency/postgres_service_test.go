package idempotency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

// Shared container and pool for all Postgres service tests.
// Lazily initialized on first use so redis-only test runs don't start CockroachDB.
var (
	pgOnce        sync.Once
	pgPool        *pgxpool.Pool
	pgContainer   testcontainers.Container
	pgInitErr     error
	pgInitCleanup func()
)

func getSharedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		container, err := cockroachdb.Run(ctx,
			"cockroachdb/cockroach:v24.3.0",
			cockroachdb.WithDatabase("test_db"),
			cockroachdb.WithUser("root"),
			cockroachdb.WithInsecure(),
		)
		if err != nil {
			pgInitErr = err
			return
		}
		pgContainer = container

		connConfig, err := container.ConnectionConfig(ctx)
		if err != nil {
			pgInitErr = err
			return
		}

		pool, err := pgxpool.New(ctx, connConfig.ConnString())
		if err != nil {
			pgInitErr = err
			return
		}
		pgPool = pool

		svc := NewPostgresService(pool)
		if err := svc.EnsureTable(ctx); err != nil {
			pgInitErr = err
			pool.Close()
			return
		}

		pgInitCleanup = func() {
			pool.Close()
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_ = container.Terminate(cleanupCtx)
		}
	})

	if pgInitErr != nil {
		t.Fatalf("Failed to initialize shared CockroachDB: %v", pgInitErr)
	}

	// Register cleanup only once via t.Cleanup won't work well with shared state.
	// The container lives for the entire test run and is cleaned up at process exit.
	// For long-lived test processes, the Go runtime will clean up.

	return pgPool
}

// freshService returns a service backed by the shared pool after truncating the table.
func freshService(t *testing.T) *PostgresService {
	t.Helper()
	pool := getSharedPool(t)
	_, err := pool.Exec(context.Background(), `DELETE FROM _idempotency_keys`)
	if err != nil {
		t.Fatalf("Failed to truncate _idempotency_keys: %v", err)
	}
	return NewPostgresService(pool)
}

func pgTestKey() Key {
	return Key{
		Namespace: "current-account",
		Operation: "deposit",
		EntityID:  "ACC-12345",
		RequestID: uuid.NewString(),
	}
}

// Interface compliance
func TestPostgresService_InterfaceCompliance(_ *testing.T) {
	var _ Service = (*PostgresService)(nil)
}

// -- Checker tests --

func TestPostgresService_Check_NotFound(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()

	result, err := svc.Check(ctx, pgTestKey())
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
	if !errors.Is(err, ErrResultNotFound) {
		t.Errorf("Expected ErrResultNotFound, got %v", err)
	}
}

func TestPostgresService_MarkPending_ThenCheck(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()

	if err := svc.MarkPending(ctx, key, time.Hour); err != nil {
		t.Fatalf("MarkPending failed: %v", err)
	}

	result, err := svc.Check(ctx, key)
	if err != nil {
		t.Errorf("Expected no error for pending, got %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.Status != StatusPending {
		t.Errorf("Expected status %v, got %v", StatusPending, result.Status)
	}
}

func TestPostgresService_StoreResult_ThenCheck(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()

	completedResult := Result{
		Key:         key,
		Status:      StatusCompleted,
		Data:        []byte(`{"amount": 100}`),
		CompletedAt: time.Now(),
		TTL:         time.Hour,
	}
	if err := svc.StoreResult(ctx, completedResult); err != nil {
		t.Fatalf("StoreResult failed: %v", err)
	}

	result, err := svc.Check(ctx, key)
	if !errors.Is(err, ErrOperationAlreadyProcessed) {
		t.Errorf("Expected ErrOperationAlreadyProcessed, got %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.Status != StatusCompleted {
		t.Errorf("Expected status %v, got %v", StatusCompleted, result.Status)
	}
	if string(result.Data) != `{"amount": 100}` {
		t.Errorf("Expected data %q, got %q", `{"amount": 100}`, string(result.Data))
	}
}

func TestPostgresService_Delete_ThenCheck(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()

	if err := svc.MarkPending(ctx, key, time.Hour); err != nil {
		t.Fatalf("MarkPending failed: %v", err)
	}

	if err := svc.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	result, err := svc.Check(ctx, key)
	if result != nil {
		t.Errorf("Expected nil result after delete, got %v", result)
	}
	if !errors.Is(err, ErrResultNotFound) {
		t.Errorf("Expected ErrResultNotFound after delete, got %v", err)
	}
}

func TestPostgresService_TTLExpiry(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()

	// Store with a long TTL first
	result := Result{
		Key:    key,
		Status: StatusCompleted,
		Data:   []byte(`{"test": true}`),
		TTL:    time.Hour,
	}
	if err := svc.StoreResult(ctx, result); err != nil {
		t.Fatalf("StoreResult failed: %v", err)
	}

	// Force expires_at into the past using CockroachDB's own clock to avoid host/container clock skew
	_, err := svc.pool.Exec(ctx,
		`UPDATE _idempotency_keys SET expires_at = NOW() - INTERVAL '1 minute' WHERE key = $1`,
		key.String(),
	)
	if err != nil {
		t.Fatalf("Failed to backdate expires_at: %v", err)
	}

	// Check should return not found (expired)
	checked, err := svc.Check(ctx, key)
	if checked != nil {
		t.Errorf("Expected nil result after TTL expiry, got %v", checked)
	}
	if !errors.Is(err, ErrResultNotFound) {
		t.Errorf("Expected ErrResultNotFound after TTL expiry, got %v", err)
	}
}

// -- Locker tests --

func TestPostgresService_Acquire_Success(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      token,
	}

	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	held, err := svc.IsHeld(ctx, key)
	if err != nil {
		t.Fatalf("IsHeld failed: %v", err)
	}
	if !held {
		t.Error("Expected lock to be held after acquisition")
	}
}

func TestPostgresService_Acquire_AlreadyHeld(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()

	opts1 := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      uuid.NewString(),
	}
	if err := svc.Acquire(ctx, key, opts1); err != nil {
		t.Fatalf("First acquire failed: %v", err)
	}

	opts2 := LockOptions{
		TTL:        30 * time.Second,
		RetryDelay: 10 * time.Millisecond,
		MaxRetries: 0,
		Token:      uuid.NewString(),
	}
	err := svc.Acquire(ctx, key, opts2)
	if !errors.Is(err, ErrLockAcquisitionFailed) {
		t.Errorf("Expected ErrLockAcquisitionFailed, got %v", err)
	}
}

func TestPostgresService_Release_WrongToken(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	err := svc.Release(ctx, key, uuid.NewString())
	if !errors.Is(err, ErrLockNotHeld) {
		t.Errorf("Expected ErrLockNotHeld, got %v", err)
	}
}

func TestPostgresService_Release_CorrectToken_ThenReAcquire(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if err := svc.Release(ctx, key, token); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Re-acquire with a new token
	newToken := uuid.NewString()
	opts2 := LockOptions{
		TTL:   30 * time.Second,
		Token: newToken,
	}
	if err := svc.Acquire(ctx, key, opts2); err != nil {
		t.Errorf("Re-acquire after release failed: %v", err)
	}
}

func TestPostgresService_Refresh(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:   5 * time.Second,
		Token: token,
	}
	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Refresh with longer TTL
	if err := svc.Refresh(ctx, key, token, 60*time.Second); err != nil {
		t.Errorf("Refresh failed: %v", err)
	}

	// Verify lock is still held
	held, err := svc.IsHeld(ctx, key)
	if err != nil {
		t.Fatalf("IsHeld failed: %v", err)
	}
	if !held {
		t.Error("Expected lock to still be held after refresh")
	}
}

func TestPostgresService_Refresh_WrongToken(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	opts := LockOptions{
		TTL:   30 * time.Second,
		Token: token,
	}
	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	err := svc.Refresh(ctx, key, uuid.NewString(), time.Minute)
	if !errors.Is(err, ErrLockNotHeld) {
		t.Errorf("Expected ErrLockNotHeld, got %v", err)
	}
}

func TestPostgresService_IsHeld(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	token := uuid.NewString()

	// Not held initially
	held, err := svc.IsHeld(ctx, key)
	if err != nil {
		t.Fatalf("IsHeld failed: %v", err)
	}
	if held {
		t.Error("Expected lock to not be held initially")
	}

	// Acquire
	opts := LockOptions{TTL: 30 * time.Second, Token: token}
	if err := svc.Acquire(ctx, key, opts); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Held after acquire
	held, err = svc.IsHeld(ctx, key)
	if err != nil {
		t.Fatalf("IsHeld failed: %v", err)
	}
	if !held {
		t.Error("Expected lock to be held after acquisition")
	}

	// Release
	if err := svc.Release(ctx, key, token); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Not held after release
	held, err = svc.IsHeld(ctx, key)
	if err != nil {
		t.Fatalf("IsHeld failed: %v", err)
	}
	if held {
		t.Error("Expected lock to not be held after release")
	}
}

// -- TTL Cleanup test --

func TestPostgresService_StartCleanup(t *testing.T) {
	svc := freshService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	key := pgTestKey()

	// Store a result first (with a long TTL so it inserts successfully)
	result := Result{
		Key:    key,
		Status: StatusPending,
		Data:   []byte(`{}`),
		TTL:    time.Hour,
	}
	if err := svc.StoreResult(ctx, result); err != nil {
		t.Fatalf("StoreResult failed: %v", err)
	}

	// Force expires_at into the past using CockroachDB's own clock to avoid host/container clock skew
	_, err := svc.pool.Exec(ctx,
		`UPDATE _idempotency_keys SET expires_at = NOW() - INTERVAL '1 minute' WHERE key = $1`,
		key.String(),
	)
	if err != nil {
		t.Fatalf("Failed to backdate expires_at: %v", err)
	}

	// Create a new service instance with short cleanup interval for this test
	pool := getSharedPool(t)
	cleanupSvc := NewPostgresService(pool, WithCleanupInterval(100*time.Millisecond))
	cleanupSvc.StartCleanup(ctx)

	// Wait for cleanup to run
	awaitCleanupErr := await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			var count int
			if err := svc.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM _idempotency_keys WHERE key = $1`, key.String(),
			).Scan(&count); err != nil {
				return false
			}
			return count == 0
		})
	if awaitCleanupErr != nil {
		t.Errorf("Expected 0 rows after cleanup, but row was not cleaned up within timeout")
	}
}

// -- Concurrency test --

func TestPostgresService_Acquire_Concurrent(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	key := pgTestKey()
	var successCount atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := LockOptions{
				TTL:        30 * time.Second,
				RetryDelay: 10 * time.Millisecond,
				MaxRetries: 0,
				Token:      uuid.NewString(),
			}
			if err := svc.Acquire(ctx, key, opts); err == nil {
				successCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Errorf("Expected exactly 1 successful acquisition, got %d", successCount.Load())
	}
}

// -- EnsureTable idempotency test --

func TestPostgresService_EnsureTable_Idempotent(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()

	// Call EnsureTable again (already called during init)
	if err := svc.EnsureTable(ctx); err != nil {
		t.Fatalf("Second EnsureTable call failed: %v", err)
	}

	// Third time for good measure
	if err := svc.EnsureTable(ctx); err != nil {
		t.Fatalf("Third EnsureTable call failed: %v", err)
	}
}

// -- Validation tests --

func TestPostgresService_InvalidKey(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()
	invalidKey := Key{} // Missing required fields

	_, err := svc.Check(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Check: expected ErrInvalidKey, got %v", err)
	}

	err = svc.MarkPending(ctx, invalidKey, time.Hour)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("MarkPending: expected ErrInvalidKey, got %v", err)
	}

	err = svc.Delete(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Delete: expected ErrInvalidKey, got %v", err)
	}

	opts := DefaultLockOptions()
	opts.Token = uuid.NewString()
	err = svc.Acquire(ctx, invalidKey, opts)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Acquire: expected ErrInvalidKey, got %v", err)
	}

	err = svc.Release(ctx, invalidKey, "token")
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Release: expected ErrInvalidKey, got %v", err)
	}

	_, err = svc.IsHeld(ctx, invalidKey)
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("IsHeld: expected ErrInvalidKey, got %v", err)
	}
}

func TestPostgresService_StoreResult_InvalidTTL(t *testing.T) {
	svc := freshService(t)

	key := Key{
		Namespace: "test",
		Operation: "test-op",
		EntityID:  "test-123",
	}

	tests := []struct {
		name string
		ttl  time.Duration
	}{
		{"zero TTL", 0},
		{"negative TTL", -1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Result{
				Key:    key,
				Status: StatusCompleted,
				TTL:    tt.ttl,
			}
			err := svc.StoreResult(context.Background(), result)
			if !errors.Is(err, ErrInvalidTTL) {
				t.Errorf("StoreResult() with %s: got error %v, want %v", tt.name, err, ErrInvalidTTL)
			}
		})
	}
}

func TestPostgresService_MarkPending_InvalidTTL(t *testing.T) {
	svc := freshService(t)

	key := Key{
		Namespace: "test",
		Operation: "test-op",
		EntityID:  "test-123",
	}

	err := svc.MarkPending(context.Background(), key, 0)
	if !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("Expected ErrInvalidTTL, got %v", err)
	}

	err = svc.MarkPending(context.Background(), key, -1*time.Second)
	if !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("Expected ErrInvalidTTL, got %v", err)
	}
}

func TestPostgresService_Refresh_InvalidTTL(t *testing.T) {
	svc := freshService(t)

	key := Key{
		Namespace: "test",
		Operation: "test-lock",
		EntityID:  "test-123",
	}
	token := uuid.NewString()

	opts := LockOptions{
		TTL:        1 * time.Minute,
		Token:      token,
		MaxRetries: 0,
	}
	if err := svc.Acquire(context.Background(), key, opts); err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}

	tests := []struct {
		name string
		ttl  time.Duration
	}{
		{"zero TTL", 0},
		{"negative TTL", -1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Refresh(context.Background(), key, token, tt.ttl)
			if !errors.Is(err, ErrInvalidTTL) {
				t.Errorf("Refresh() with %s: got error %v, want %v", tt.name, err, ErrInvalidTTL)
			}
		})
	}
}

// -- Tenant isolation test --

func TestPostgresService_TenantIsolation(t *testing.T) {
	svc := freshService(t)
	ctx := context.Background()

	key1 := Key{
		TenantID:  "tenant-a",
		Namespace: "ns",
		Operation: "op",
		EntityID:  "entity-1",
	}
	key2 := Key{
		TenantID:  "tenant-b",
		Namespace: "ns",
		Operation: "op",
		EntityID:  "entity-1",
	}

	// Store for tenant-a
	if err := svc.MarkPending(ctx, key1, time.Hour); err != nil {
		t.Fatalf("MarkPending tenant-a failed: %v", err)
	}

	// Check tenant-b should not find it
	result, err := svc.Check(ctx, key2)
	if result != nil {
		t.Errorf("Expected nil result for tenant-b, got %v", result)
	}
	if !errors.Is(err, ErrResultNotFound) {
		t.Errorf("Expected ErrResultNotFound for tenant-b, got %v", err)
	}

	// Check tenant-a should find it
	result, err = svc.Check(ctx, key1)
	if err != nil {
		t.Errorf("Expected no error for tenant-a, got %v", err)
	}
	if result == nil {
		t.Fatal("Expected result for tenant-a, got nil")
	}
}
