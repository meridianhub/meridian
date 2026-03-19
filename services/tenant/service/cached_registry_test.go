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
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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

func TestDefaultCachedRegistryConfig(t *testing.T) {
	config := DefaultCachedRegistryConfig()

	if config.RefreshInterval <= 0 {
		t.Error("Expected positive RefreshInterval")
	}
	if config.RefreshTimeout <= 0 {
		t.Error("Expected positive RefreshTimeout")
	}
	if config.Logger == nil {
		t.Error("Expected non-nil Logger")
	}
}

func TestCachedRegistry_IsActive(t *testing.T) {
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer dbCleanup()
	testdb.CreateAuditTables(t, db)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := CachedRegistryConfig{
		RefreshInterval: 50 * time.Millisecond,
		RefreshTimeout:  5 * time.Second,
		Logger:          logger,
	}
	registry := NewCachedRegistry(repo, config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a tenant
	tid, _ := tenant.NewTenantID("active_test")
	tenantObj := &domain.Tenant{
		ID:              tid,
		DisplayName:     "Active Test",
		SettlementAsset: "GBP",
		Status:          domain.StatusActive,
		CreatedAt:       time.Now(),
		Version:         1,
	}
	err := repo.Create(ctx, tenantObj)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Start the registry to load cache
	registry.Start(ctx)

	// Wait for cache to load
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return registry.Count() > 0
	})
	if err != nil {
		t.Fatal("Cache never loaded")
	}

	// Check IsActive for cached tenant
	active, err := registry.IsActive(ctx, tid)
	if err != nil {
		t.Fatalf("IsActive failed: %v", err)
	}
	if !active {
		t.Error("Expected active tenant to return true")
	}

	// Check IsActive for non-existent tenant (cache miss, falls through to DB)
	unknownID, _ := tenant.NewTenantID("unknown_tenant")
	active, err = registry.IsActive(ctx, unknownID)
	// DB returns error for non-existent tenant - that's expected
	if err == nil {
		if active {
			t.Error("Expected unknown tenant to not be active")
		}
	}
	// Whether error or false, the tenant should not be considered active
}

func TestCachedRegistry_GetTenant(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Before start, cache is empty
	tid := tenant.MustNewTenantID("nonexistent")
	result := registry.GetTenant(tid)
	if result != nil {
		t.Error("Expected nil for empty cache")
	}

	registry.Start(ctx)

	// Still nil for non-existent tenant
	result = registry.GetTenant(tid)
	if result != nil {
		t.Error("Expected nil for non-existent tenant")
	}
}

func TestCachedRegistry_LastRefreshError(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	// Before start, no error
	err := registry.LastRefreshError()
	if err != nil {
		t.Errorf("Expected nil error before start, got %v", err)
	}
}

func TestCachedRegistry_Count(t *testing.T) {
	registry, cleanup := setupCachedRegistry(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Before start, count is 0
	if registry.Count() != 0 {
		t.Errorf("Expected count 0 before start, got %d", registry.Count())
	}

	registry.Start(ctx)

	// Count should be 0 (empty DB)
	err := await.New().AtMost(1 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return registry.LastRefresh().After(time.Time{})
	})
	if err != nil {
		t.Fatal("Registry never refreshed")
	}

	if registry.Count() != 0 {
		t.Errorf("Expected count 0 with empty DB, got %d", registry.Count())
	}
}

func TestNewCachedRegistry_Defaults(t *testing.T) {
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer dbCleanup()
	testdb.CreateAuditTables(t, db)
	repo := persistence.NewRepository(db)

	// Pass zero-value config - should use defaults
	registry := NewCachedRegistry(repo, CachedRegistryConfig{})

	if registry.refreshInterval <= 0 {
		t.Error("Expected positive default refreshInterval")
	}
	if registry.refreshTimeout <= 0 {
		t.Error("Expected positive default refreshTimeout")
	}
	if registry.logger == nil {
		t.Error("Expected non-nil default logger")
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
