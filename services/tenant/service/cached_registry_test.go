package service

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

func setupCachedRegistry(t *testing.T) (*CachedRegistry, func()) {
	t.Helper()

	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	testdb.CreateAuditTables(t, db)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	config := CachedRegistryConfig{
		RefreshInterval: 50 * time.Millisecond,
		RefreshTimeout:  5 * time.Second,
		Logger:          logger,
	}

	registry := NewCachedRegistry(repo, config)

	return registry, dbCleanup
}

func TestCachedRegistry_Start_IsIdempotent(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	// Call Start() 3 times
	registry.Start(ctx)
	registry.Start(ctx)
	registry.Start(ctx)

	// Wait a bit for any goroutines to start
	time.Sleep(20 * time.Millisecond)

	// Check goroutine count - should only have 1 additional goroutine
	currentGoroutines := runtime.NumGoroutine()
	additionalGoroutines := currentGoroutines - baselineGoroutines

	// Allow for 1-2 additional goroutines (the refresh loop plus potential runtime overhead)
	if additionalGoroutines > 2 {
		t.Errorf("Expected at most 2 additional goroutines, got %d (baseline: %d, current: %d)",
			additionalGoroutines, baselineGoroutines, currentGoroutines)
	}
}

func TestCachedRegistry_Start_ConcurrentCalls(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	// Spawn 10 goroutines all calling Start() concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			registry.Start(ctx)
		}()
	}
	wg.Wait()

	// Wait a bit for any goroutines to settle
	time.Sleep(20 * time.Millisecond)

	// Check goroutine count - should only have 1 additional goroutine from the refresh loop
	currentGoroutines := runtime.NumGoroutine()
	additionalGoroutines := currentGoroutines - baselineGoroutines

	// Allow for 1-2 additional goroutines (the refresh loop plus potential runtime overhead)
	if additionalGoroutines > 2 {
		t.Errorf("Expected at most 2 additional goroutines after concurrent Start calls, got %d (baseline: %d, current: %d)",
			additionalGoroutines, baselineGoroutines, currentGoroutines)
	}
}

func TestCachedRegistry_Started_ReturnsTrue(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry.Start(ctx)

	if !registry.Started() {
		t.Error("Expected Started() to return true after Start() is called")
	}
}

func TestCachedRegistry_Started_ReturnsFalseBeforeStart(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	if registry.Started() {
		t.Error("Expected Started() to return false before Start() is called")
	}
}

func TestCachedRegistry_RefreshLoopRuns(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry.Start(ctx)

	// Get initial refresh time
	initialRefresh := registry.LastRefresh()
	if initialRefresh.IsZero() {
		t.Fatal("Expected LastRefresh to be set after Start()")
	}

	// Wait for at least one refresh cycle (refresh interval is 50ms)
	time.Sleep(100 * time.Millisecond)

	// Check that refresh has run at least once more
	latestRefresh := registry.LastRefresh()
	if !latestRefresh.After(initialRefresh) {
		t.Errorf("Expected LastRefresh to update after waiting; initial: %v, latest: %v",
			initialRefresh, latestRefresh)
	}
}

func TestCachedRegistry_Started_ReturnsFalseAfterContextCancelled(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	registry.Start(ctx)

	if !registry.Started() {
		t.Error("Expected Started() to return true after Start() is called")
	}

	// Cancel the context
	cancel()

	// Wait for the goroutine to exit
	time.Sleep(100 * time.Millisecond)

	if registry.Started() {
		t.Error("Expected Started() to return false after context is cancelled")
	}
}
