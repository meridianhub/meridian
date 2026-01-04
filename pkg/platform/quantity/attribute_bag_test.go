package quantity

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestSliceAttributeBag_BasicOperations tests Get, Set, Len, Keys operations.
func TestSliceAttributeBag_BasicOperations(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	// Test empty bag
	if bag.Len() != 0 {
		t.Errorf("expected empty bag, got len=%d", bag.Len())
	}

	// Test Set and Get
	bag.Set("key1", "value1")
	bag.Set("key2", "value2")

	if val, ok := bag.Get("key1"); !ok || val != "value1" {
		t.Errorf("Get(key1) = %q, %v; want value1, true", val, ok)
	}
	if val, ok := bag.Get("key2"); !ok || val != "value2" {
		t.Errorf("Get(key2) = %q, %v; want value2, true", val, ok)
	}
	if _, ok := bag.Get("nonexistent"); ok {
		t.Error("Get(nonexistent) should return false")
	}

	// Test Len
	if bag.Len() != 2 {
		t.Errorf("Len() = %d; want 2", bag.Len())
	}

	// Test Keys
	keys := bag.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() returned %d keys; want 2", len(keys))
	}
}

// TestSliceAttributeBag_UpdateExisting tests that Set updates existing keys.
func TestSliceAttributeBag_UpdateExisting(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	bag.Set("key", "original")
	bag.Set("key", "updated")

	if val, ok := bag.Get("key"); !ok || val != "updated" {
		t.Errorf("Get(key) = %q, %v; want updated, true", val, ok)
	}
	if bag.Len() != 1 {
		t.Errorf("Len() = %d; want 1 (should not duplicate)", bag.Len())
	}
}

// TestSliceAttributeBag_Reset tests that Reset clears entries but preserves capacity.
func TestSliceAttributeBag_Reset(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	// Add entries
	for i := 0; i < 10; i++ {
		bag.Set(string(rune('a'+i)), "value")
	}

	if bag.Len() != 10 {
		t.Errorf("Len() = %d; want 10", bag.Len())
	}

	// Reset
	bag.Reset()

	if bag.Len() != 0 {
		t.Errorf("after Reset(), Len() = %d; want 0", bag.Len())
	}

	// Verify we can still add after reset
	bag.Set("new", "entry")
	if val, ok := bag.Get("new"); !ok || val != "entry" {
		t.Error("should be able to Set after Reset")
	}
}

// TestSliceAttributeBag_ToMap tests map conversion for CEL boundary.
func TestSliceAttributeBag_ToMap(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	bag.Set("currency", "USD")
	bag.Set("country", "US")

	m := bag.ToMap()

	if len(m) != 2 {
		t.Errorf("ToMap() returned map with %d entries; want 2", len(m))
	}
	if m["currency"] != "USD" {
		t.Errorf("m[currency] = %q; want USD", m["currency"])
	}
	if m["country"] != "US" {
		t.Errorf("m[country] = %q; want US", m["country"])
	}

	// Verify map is independent (modification doesn't affect bag)
	m["currency"] = "EUR"
	if val, _ := bag.Get("currency"); val != "USD" {
		t.Error("modifying map should not affect bag")
	}
}

// TestSliceAttributeBag_ToAttributes tests conversion to Attribute slice.
func TestSliceAttributeBag_ToAttributes(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	bag.Set("type", "spot")
	bag.Set("region", "EU")

	attrs := bag.ToAttributes()

	if len(attrs) != 2 {
		t.Errorf("ToAttributes() returned %d; want 2", len(attrs))
	}

	// Verify content (order preserved)
	found := make(map[string]string)
	for _, attr := range attrs {
		found[attr.Key] = attr.Value
	}
	if found["type"] != "spot" || found["region"] != "EU" {
		t.Error("ToAttributes() content mismatch")
	}
}

// TestSliceAttributeBag_FromAttributes tests populating from Attribute slice.
func TestSliceAttributeBag_FromAttributes(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	attrs := []Attribute{
		{Key: "grid", Value: "ERCOT"},
		{Key: "node", Value: "HB_HOUSTON"},
	}

	bag.FromAttributes(attrs)

	if bag.Len() != 2 {
		t.Errorf("Len() = %d; want 2", bag.Len())
	}
	if val, ok := bag.Get("grid"); !ok || val != "ERCOT" {
		t.Errorf("Get(grid) = %q, %v; want ERCOT, true", val, ok)
	}
}

// TestSliceAttributeBag_InitialCapacity verifies bag starts with capacity 16.
func TestSliceAttributeBag_InitialCapacity(t *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	// Check capacity is at least 16 (initial allocation)
	if cap(bag.entries) < 16 {
		t.Errorf("initial capacity = %d; want >= 16", cap(bag.entries))
	}
}

// TestReleaseAttributeBag_NilSafe tests that releasing nil doesn't panic.
func TestReleaseAttributeBag_NilSafe(_ *testing.T) {
	// Should not panic
	ReleaseAttributeBag(nil)
}

// TestAttributeBag_ConcurrentAccess tests concurrent Get/Set operations.
func TestAttributeBag_ConcurrentAccess(_ *testing.T) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	// Pre-populate
	bag.Set("shared", "initial")

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Note: AttributeBag is NOT thread-safe by design.
	// Each goroutine should acquire its own bag from the pool.
	// This test verifies the pool itself handles concurrent Acquire/Release.

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				localBag := AcquireAttributeBag()
				localBag.Set("key", "value")
				_, _ = localBag.Get("key")
				ReleaseAttributeBag(localBag)
			}
		}()
	}

	wg.Wait()
}

// BenchmarkPooledAllocation benchmarks acquiring from pool vs direct allocation.
func BenchmarkPooledAllocation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bag := AcquireAttributeBag()
		bag.Set("key", "value")
		ReleaseAttributeBag(bag)
	}
}

// BenchmarkUnpooledAllocation benchmarks direct allocation without pooling.
func BenchmarkUnpooledAllocation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bag := &AttributeBag{
			entries: make([]kv, 0, 16),
		}
		bag.Set("key", "value")
		// No release - simulates unpooled usage
		_ = bag
	}
}

// BenchmarkPoolReuseRate measures pool hit rate under load.
func BenchmarkPoolReuseRate(b *testing.B) {
	// Track pool misses (new allocations)
	var misses atomic.Int64
	originalPool := attributeBagPool

	// Replace pool with instrumented version
	attributeBagPool = sync.Pool{
		New: func() any {
			misses.Add(1)
			return &AttributeBag{
				entries: make([]kv, 0, 16),
			}
		},
	}
	defer func() { attributeBagPool = originalPool }()

	// Warm up the pool
	warmupBags := make([]*AttributeBag, 100)
	for i := range warmupBags {
		warmupBags[i] = AcquireAttributeBag()
	}
	for _, bag := range warmupBags {
		ReleaseAttributeBag(bag)
	}
	misses.Store(0) // Reset after warmup

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		bag := AcquireAttributeBag()
		bag.Set("key", "value")
		ReleaseAttributeBag(bag)
	}

	b.StopTimer()

	// Calculate reuse rate
	totalOps := int64(b.N)
	poolMisses := misses.Load()
	reuseRate := float64(totalOps-poolMisses) / float64(totalOps) * 100

	b.ReportMetric(reuseRate, "reuse%")
	if reuseRate < 95 && b.N >= 1000 {
		b.Logf("Warning: pool reuse rate %.2f%% is below 95%% target", reuseRate)
	}
}

// BenchmarkConcurrentAccess benchmarks concurrent pool usage.
func BenchmarkConcurrentAccess(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bag := AcquireAttributeBag()
			bag.Set("key", "value")
			bag.Set("key2", "value2")
			_, _ = bag.Get("key")
			ReleaseAttributeBag(bag)
		}
	})
}

// BenchmarkGCPressure simulates high TPS and measures GC impact.
func BenchmarkGCPressure(b *testing.B) {
	b.ReportAllocs()

	// Measure GC stats before
	var statsBefore, statsAfter runtime.MemStats
	runtime.GC() // Clean slate
	runtime.ReadMemStats(&statsBefore)

	b.ResetTimer()

	// Run at high concurrency
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bag := AcquireAttributeBag()
			// Simulate typical workload
			bag.Set("currency", "USD")
			bag.Set("country", "US")
			bag.Set("type", "spot")
			_ = bag.ToMap() // CEL boundary crossing
			ReleaseAttributeBag(bag)
		}
	})

	b.StopTimer()

	runtime.GC()
	runtime.ReadMemStats(&statsAfter)

	// Report GC metrics
	gcPauses := statsAfter.NumGC - statsBefore.NumGC
	pauseTotalNs := statsAfter.PauseTotalNs - statsBefore.PauseTotalNs

	if gcPauses > 0 {
		avgPauseMs := float64(pauseTotalNs) / float64(gcPauses) / 1e6
		b.ReportMetric(avgPauseMs, "avgPause-ms")
	}
}

// BenchmarkToMap measures the cost of map conversion.
func BenchmarkToMap(b *testing.B) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	// Pre-populate with typical attributes
	bag.Set("currency", "USD")
	bag.Set("country", "US")
	bag.Set("type", "spot")
	bag.Set("region", "ERCOT")
	bag.Set("node", "HB_HOUSTON")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m := bag.ToMap()
		_ = m["currency"]
	}
}

// BenchmarkLinearSearch benchmarks Get performance at various sizes.
func BenchmarkLinearSearch_5Keys(b *testing.B) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	for i := 0; i < 5; i++ {
		bag.Set(string(rune('a'+i)), "value")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bag.Get("c") // Middle key
	}
}

// BenchmarkLinearSearch_16Keys benchmarks Get at full initial capacity.
func BenchmarkLinearSearch_16Keys(b *testing.B) {
	bag := AcquireAttributeBag()
	defer ReleaseAttributeBag(bag)

	for i := 0; i < 16; i++ {
		bag.Set(string(rune('a'+i)), "value")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bag.Get("h") // Middle key
	}
}
