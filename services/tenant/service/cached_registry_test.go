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
	"github.com/meridianhub/meridian/shared/platform/await"
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
	baselineGoroutines := runtime.NumGoroutine()

	// Call Start() 3 times
	registry.Start(ctx)
	registry.Start(ctx)
	registry.Start(ctx)

	// Wait for goroutine count to stabilize
	var currentGoroutines int
	_ = await.New().AtMost(1 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		runtime.GC()
		currentGoroutines = runtime.NumGoroutine()
		// Check it's within expected range (goroutine count stabilized)
		additionalGoroutines := currentGoroutines - baselineGoroutines
		return additionalGoroutines >= 1 && additionalGoroutines <= 2
	})

	// Check goroutine count - should only have 1 additional goroutine
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

	// Wait for goroutine count to stabilize
	var currentGoroutines int
	_ = await.New().AtMost(1 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		runtime.GC()
		currentGoroutines = runtime.NumGoroutine()
		// Check it's within expected range (goroutine count stabilized)
		additionalGoroutines := currentGoroutines - baselineGoroutines
		return additionalGoroutines >= 1 && additionalGoroutines <= 2
	})

	// Check goroutine count - should only have 1 additional goroutine from the refresh loop
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

	// Wait for refresh to run by polling LastRefresh()
	var latestRefresh time.Time
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		latestRefresh = registry.LastRefresh()
		return latestRefresh.After(initialRefresh)
	})
	if err != nil {
		t.Errorf("Expected LastRefresh to update; initial: %v, latest: %v",
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

	// Wait for the goroutine to exit by polling Started()
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return !registry.Started()
	})
	if err != nil {
		t.Error("Expected Started() to return false after context is cancelled")
	}
}

func TestCachedRegistry_CannotRestartAfterCancel(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	// Start with first context
	ctx1, cancel1 := context.WithCancel(context.Background())
	registry.Start(ctx1)

	if !registry.Started() {
		t.Fatal("Expected Started() to return true after Start()")
	}

	// Cancel the first context and wait for goroutine to exit
	cancel1()
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return !registry.Started()
	})
	if err != nil {
		t.Fatal("Expected Started() to return false after context cancelled")
	}

	// Attempt to restart with a new context
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	registry.Start(ctx2)

	// Verify registry cannot restart - sync.Once has already fired
	if registry.Started() {
		t.Error("Expected Started() to remain false after restart attempt; registry should not be restartable")
	}
}
