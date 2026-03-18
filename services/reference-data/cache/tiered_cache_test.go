package cache

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockSource is a test implementation of the Source interface.
type mockSource struct {
	definitions map[string]*registry.InstrumentDefinition // keyed by "tenantID:code:version"
	loadCount   int32
	loadDelay   time.Duration
	loadErr     error
}

func newMockSource() *mockSource {
	return &mockSource{
		definitions: make(map[string]*registry.InstrumentDefinition),
	}
}

func (m *mockSource) GetDefinition(ctx context.Context, code string, version int) (*registry.InstrumentDefinition, error) {
	atomic.AddInt32(&m.loadCount, 1)

	if m.loadDelay > 0 {
		time.Sleep(m.loadDelay) //nolint:forbidigo // simulates source latency in mock
	}

	if m.loadErr != nil {
		return nil, m.loadErr
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	key := sourceKey(string(tenantID), code, version)
	def, ok := m.definitions[key]
	if !ok {
		return nil, registry.ErrNotFound
	}

	return def, nil
}

func (m *mockSource) addDefinition(tenantID string, def *registry.InstrumentDefinition) {
	key := sourceKey(tenantID, def.Code, def.Version)
	m.definitions[key] = def
}

func sourceKey(tenantID, code string, version int) string {
	return tenantID + ":" + code + ":" + strconv.Itoa(version)
}

// mockCELCompiler is a test implementation of CELCompiler.
type mockCELCompiler struct {
	compileValidationFn func(expression string) (cel.Program, error)
	compileBucketKeyFn  func(expression string) (cel.Program, error)
	compileCount        int32
}

func newMockCELCompiler() *mockCELCompiler {
	return &mockCELCompiler{
		compileValidationFn: func(_ string) (cel.Program, error) {
			return nil, nil // Return nil program, which is valid
		},
		compileBucketKeyFn: func(_ string) (cel.Program, error) {
			return nil, nil
		},
	}
}

func (m *mockCELCompiler) CompileValidation(expression string) (cel.Program, error) {
	atomic.AddInt32(&m.compileCount, 1)
	return m.compileValidationFn(expression)
}

func (m *mockCELCompiler) CompileBucketKey(expression string) (cel.Program, error) {
	atomic.AddInt32(&m.compileCount, 1)
	return m.compileBucketKeyFn(expression)
}

// mockL2Cache tracks calls for testing.
type mockL2Cache struct {
	NoOpL2Cache
	getCount  int32
	putCount  int32
	getCalled map[string]bool
	putCalled map[string]bool
	data      map[string]*registry.InstrumentDefinition
	mu        sync.Mutex
}

func newMockL2Cache() *mockL2Cache {
	return &mockL2Cache{
		getCalled: make(map[string]bool),
		putCalled: make(map[string]bool),
		data:      make(map[string]*registry.InstrumentDefinition),
	}
}

func (m *mockL2Cache) Get(ctx context.Context, code string, version int) *registry.InstrumentDefinition {
	atomic.AddInt32(&m.getCount, 1)

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := sourceKey(string(tenantID), code, version)
	m.getCalled[key] = true
	return m.data[key]
}

func (m *mockL2Cache) Put(ctx context.Context, code string, version int, def *registry.InstrumentDefinition) {
	atomic.AddInt32(&m.putCount, 1)

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := sourceKey(string(tenantID), code, version)
	m.putCalled[key] = true
	if def != nil {
		m.data[key] = def
	}
}

func (m *mockL2Cache) addDefinition(tenantID string, def *registry.InstrumentDefinition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := sourceKey(tenantID, def.Code, def.Version)
	m.data[key] = def
}

// Test helpers

func newTieredTestDefinition(code string, version int) *registry.InstrumentDefinition {
	return &registry.InstrumentDefinition{
		ID:        uuid.New(),
		Code:      code,
		Version:   version,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
		Status:    registry.StatusActive,
		CreatedAt: time.Now(),
	}
}

func newTieredTestDefinitionWithCEL(code string, version int) *registry.InstrumentDefinition {
	def := newTieredTestDefinition(code, version)
	def.ValidationExpression = "amount > 0"
	def.FungibilityKeyExpression = "instrument_code"
	return def
}

// Tests for L1 hit path

func TestTieredInstrumentCache_L1Hit(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate L1 cache
	def := newTieredTestDefinition("USD", 1)
	cached := &CachedInstrument{Definition: def}
	l1.Put(ctx, "USD", 1, cached)

	// Get should hit L1 without checking L2 or source
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// Verify L2 and source were not called
	assert.Equal(t, int32(0), atomic.LoadInt32(&l2.getCount), "L2 should not be called on L1 hit")
	assert.Equal(t, int32(0), atomic.LoadInt32(&source.loadCount), "source should not be called on L1 hit")

	// Verify stats
	stats := tiered.Stats(ctx)
	assert.Equal(t, int64(1), stats.L1Hits)
	assert.Equal(t, int64(0), stats.L1Misses)
	assert.Equal(t, int64(0), stats.L2Hits)
}

// Tests for L2 hit path

func TestTieredInstrumentCache_L1Miss_L2Hit(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate L2 cache (not L1)
	def := newTieredTestDefinitionWithCEL("USD", 1)
	l2.addDefinition("tenant1", def)

	// Get should miss L1, hit L2, compile CEL, and populate L1
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// Verify source was not called
	assert.Equal(t, int32(0), atomic.LoadInt32(&source.loadCount), "source should not be called on L2 hit")

	// Verify L1 was populated
	l1Cached := l1.Get(ctx, "USD", 1)
	require.NotNil(t, l1Cached, "L1 should be populated after L2 hit")
	assert.Equal(t, "USD", l1Cached.Definition.Code)

	// Verify stats
	stats := tiered.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(1), stats.L2Hits)
	assert.Equal(t, int64(0), stats.L2Misses)
	assert.Equal(t, int64(0), stats.SourceLoads)
}

func TestTieredInstrumentCache_L2Hit_CompilesCEL(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate L2 with definition that has CEL expressions
	def := newTieredTestDefinitionWithCEL("USD", 1)
	l2.addDefinition("tenant1", def)

	// Get should trigger CEL compilation
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)

	// CEL compiler should have been called twice (validation + bucket key)
	assert.Equal(t, int32(2), atomic.LoadInt32(&compiler.compileCount),
		"compiler should be called for both validation and bucket key expressions")
}

// Tests for source fetch path

func TestTieredInstrumentCache_L1Miss_L2Miss_SourceFetch(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source (not L1 or L2)
	def := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def)

	// Get should miss L1, miss L2, fetch from source, and populate both
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// Verify source was called
	assert.Equal(t, int32(1), atomic.LoadInt32(&source.loadCount), "source should be called once")

	// Verify L2 was populated
	assert.True(t, l2.putCalled[sourceKey("tenant1", "USD", 1)], "L2 should be populated")

	// Verify L1 was populated
	l1Cached := l1.Get(ctx, "USD", 1)
	require.NotNil(t, l1Cached, "L1 should be populated after source fetch")

	// Verify stats
	stats := tiered.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(0), stats.L2Hits)
	assert.Equal(t, int64(1), stats.L2Misses)
	assert.Equal(t, int64(1), stats.SourceLoads)
}

// Tests for cold start resilience

func TestTieredInstrumentCache_ColdStartResilience_SourceUnavailable_L2Hit(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	// Simulate source unavailable
	source.loadErr = errors.New("source unavailable") //nolint:err113 // test error

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate L2 (simulating warm L2 from before pod restart)
	def := newTieredTestDefinition("USD", 1)
	l2.addDefinition("tenant1", def)

	// Get should miss L1, hit L2, and return successfully despite source being unavailable
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err, "should succeed from L2 even when source is unavailable")
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// Source should not have been called (L2 hit)
	assert.Equal(t, int32(0), atomic.LoadInt32(&source.loadCount))

	// L1 should now be populated
	l1Cached := l1.Get(ctx, "USD", 1)
	require.NotNil(t, l1Cached, "L1 should be populated from L2")
}

func TestTieredInstrumentCache_SourceUnavailable_L2Miss_ReturnsError(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	// Simulate source unavailable
	sourceErr := errors.New("source unavailable") //nolint:err113 // test error
	source.loadErr = sourceErr

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// No data in L1 or L2

	// Get should fail when both L1 and L2 miss and source is unavailable
	result, err := tiered.Get(ctx, "USD", 1)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, sourceErr)
}

// Tests for singleflight deduplication

func TestTieredInstrumentCache_SingleflightDeduplication(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	// Add delay to source to increase overlap window
	source.loadDelay = 50 * time.Millisecond

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	def := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def)

	const concurrency = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	results := make([]*CachedInstrument, concurrency)
	errs := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-startCh // Wait for start signal
			results[idx], errs[idx] = tiered.Get(ctx, "USD", 1)
		}(i)
	}

	// Release all goroutines simultaneously
	close(startCh)
	wg.Wait()

	// All requests should succeed
	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i], "request %d failed", i)
		require.NotNil(t, results[i], "request %d returned nil", i)
		assert.Equal(t, "USD", results[i].Definition.Code)
	}

	// Singleflight should have deduplicated to exactly 1 source call
	assert.Equal(t, int32(1), atomic.LoadInt32(&source.loadCount),
		"singleflight should deduplicate %d concurrent requests to 1 source call", concurrency)
}

func TestTieredInstrumentCache_SingleflightTenantIsolation(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	source.loadDelay = 20 * time.Millisecond

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Pre-populate source for both tenants
	def1 := newTieredTestDefinition("USD", 1)
	def2 := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def1)
	source.addDefinition("tenant2", def2)

	var wg sync.WaitGroup
	wg.Add(2)

	// Both tenants request the same code+version concurrently
	go func() {
		defer wg.Done()
		_, _ = tiered.Get(ctx1, "USD", 1)
	}()

	go func() {
		defer wg.Done()
		_, _ = tiered.Get(ctx2, "USD", 1)
	}()

	wg.Wait()

	// Each tenant should have its own source call (singleflight key includes tenant)
	assert.Equal(t, int32(2), atomic.LoadInt32(&source.loadCount),
		"each tenant should have separate singleflight groups")
}

// Tests for tenant isolation

func TestTieredInstrumentCache_TenantIsolation(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Pre-populate source with different data for each tenant
	def1 := newTieredTestDefinition("USD", 1)
	def1.DisplayName = "Tenant1 USD"
	source.addDefinition("tenant1", def1)

	def2 := newTieredTestDefinition("USD", 1)
	def2.DisplayName = "Tenant2 USD"
	source.addDefinition("tenant2", def2)

	// Get for tenant1
	result1, err := tiered.Get(ctx1, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, "Tenant1 USD", result1.Definition.DisplayName)

	// Get for tenant2
	result2, err := tiered.Get(ctx2, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, "Tenant2 USD", result2.Definition.DisplayName)

	// Verify tenant1 still gets their data (cached in L1 now)
	result1Again, err := tiered.Get(ctx1, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "Tenant1 USD", result1Again.Definition.DisplayName)
}

// Tests for missing tenant context

func TestTieredInstrumentCache_MissingTenantContext(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := context.Background() // No tenant

	result, err := tiered.Get(ctx, "USD", 1)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrTenantContextRequired)
}

// Tests for invalidation

func TestTieredInstrumentCache_Invalidate(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()
	_ = mr

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	def := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def)

	// Get to populate caches
	_, err = tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)

	// Verify L1 has the entry
	require.NotNil(t, l1.Get(ctx, "USD", 1))
	// Verify L2 has the entry
	require.NotNil(t, l2.Get(ctx, "USD", 1))

	// Invalidate
	tiered.Invalidate(ctx, "USD", 1)

	// Both caches should be empty
	assert.Nil(t, l1.Get(ctx, "USD", 1), "L1 should be invalidated")
	assert.Nil(t, l2.Get(ctx, "USD", 1), "L2 should be invalidated")
}

func TestTieredInstrumentCache_InvalidateCode(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()
	_ = mr

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source with multiple versions
	for i := 1; i <= 3; i++ {
		def := newTieredTestDefinition("USD", i)
		source.addDefinition("tenant1", def)
	}
	// Add different code
	eurDef := newTieredTestDefinition("EUR", 1)
	source.addDefinition("tenant1", eurDef)

	// Get to populate caches
	for i := 1; i <= 3; i++ {
		_, err := tiered.Get(ctx, "USD", i)
		require.NoError(t, err)
	}
	_, err = tiered.Get(ctx, "EUR", 1)
	require.NoError(t, err)

	// InvalidateCode for USD
	tiered.InvalidateCode(ctx, "USD")

	// All USD versions should be invalidated from L1
	for i := 1; i <= 3; i++ {
		assert.Nil(t, l1.Get(ctx, "USD", i), "USD v%d should be invalidated from L1", i)
	}

	// EUR should still be cached in L1
	assert.NotNil(t, l1.Get(ctx, "EUR", 1), "EUR should not be affected in L1")
}

func TestTieredInstrumentCache_InvalidateAll(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()
	_ = mr

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	for _, code := range []string{"USD", "EUR", "GBP"} {
		def := newTieredTestDefinition(code, 1)
		source.addDefinition("tenant1", def)
	}

	// Get to populate caches
	for _, code := range []string{"USD", "EUR", "GBP"} {
		_, err := tiered.Get(ctx, code, 1)
		require.NoError(t, err)
	}

	// InvalidateAll
	tiered.InvalidateAll(ctx)

	// All should be invalidated from L1
	for _, code := range []string{"USD", "EUR", "GBP"} {
		assert.Nil(t, l1.Get(ctx, code, 1), "%s should be invalidated from L1", code)
	}
}

func TestTieredInstrumentCache_InvalidateAll_TenantIsolation(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()
	_ = mr

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx1 := newTestContext("tenant1")
	ctx2 := newTestContext("tenant2")

	// Pre-populate source for both tenants
	source.addDefinition("tenant1", newTieredTestDefinition("USD", 1))
	source.addDefinition("tenant2", newTieredTestDefinition("EUR", 1))

	// Get to populate caches
	_, err = tiered.Get(ctx1, "USD", 1)
	require.NoError(t, err)
	_, err = tiered.Get(ctx2, "EUR", 1)
	require.NoError(t, err)

	// InvalidateAll for tenant1 only
	tiered.InvalidateAll(ctx1)

	// Tenant1's cache should be empty
	assert.Nil(t, l1.Get(ctx1, "USD", 1), "tenant1's USD should be invalidated")

	// Tenant2's cache should be unaffected
	assert.NotNil(t, l1.Get(ctx2, "EUR", 1), "tenant2's EUR should not be affected")
}

// Tests for nil L2 cache

func TestTieredInstrumentCache_NilL2UsesNoOp(t *testing.T) {
	l1 := NewInstrumentCache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	// Create with nil L2
	tiered := NewTieredInstrumentCache(l1, nil, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	def := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def)

	// Should work without L2
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// Second get should hit L1
	result2, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	assert.NotNil(t, result2)

	// Only one source load
	assert.Equal(t, int32(1), atomic.LoadInt32(&source.loadCount))
}

// Tests for nil compiler

func TestTieredInstrumentCache_NilCompilerSkipsCEL(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()

	// Create with nil compiler
	tiered := NewTieredInstrumentCache(l1, l2, source, nil)
	ctx := newTestContext("tenant1")

	// Pre-populate source with CEL expressions
	def := newTieredTestDefinitionWithCEL("USD", 1)
	source.addDefinition("tenant1", def)

	// Should work without compiler
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "USD", result.Definition.Code)

	// CEL programs should be nil
	assert.Nil(t, result.ValidationProgram)
	assert.Nil(t, result.BucketKeyProgram)
}

// Tests for CEL compilation errors

func TestTieredInstrumentCache_CELCompilationError(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()

	// Compiler that returns error
	compiler := newMockCELCompiler()
	compileErr := errors.New("CEL compilation failed") //nolint:err113 // test error
	compiler.compileValidationFn = func(_ string) (cel.Program, error) {
		return nil, compileErr
	}

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source with CEL expressions
	def := newTieredTestDefinitionWithCEL("USD", 1)
	source.addDefinition("tenant1", def)

	// Get should fail with CEL compilation error
	result, err := tiered.Get(ctx, "USD", 1)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "compile validation expression")
}

// Tests for Stats

func TestTieredInstrumentCache_Stats(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	def := newTieredTestDefinition("USD", 1)
	source.addDefinition("tenant1", def)

	// Initial stats
	stats := tiered.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(0), stats.L1Misses)
	assert.Equal(t, int64(0), stats.L2Hits)
	assert.Equal(t, int64(0), stats.L2Misses)
	assert.Equal(t, int64(0), stats.SourceLoads)

	// First get: L1 miss, L2 miss, source load
	_, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)

	stats = tiered.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(0), stats.L2Hits)
	assert.Equal(t, int64(1), stats.L2Misses)
	assert.Equal(t, int64(1), stats.SourceLoads)

	// Second get: L1 hit
	_, err = tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)

	stats = tiered.Stats(ctx)
	assert.Equal(t, int64(1), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(0), stats.L2Hits)
	assert.Equal(t, int64(1), stats.L2Misses)
	assert.Equal(t, int64(1), stats.SourceLoads)

	// L1 size and capacity
	assert.Equal(t, 1, stats.L1Size)
	assert.Equal(t, DefaultCacheSize, stats.L1Capacity)
}

func TestTieredInstrumentCache_Stats_L2Hit(t *testing.T) {
	l1 := NewInstrumentCache()
	l2 := newMockL2Cache()
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate L2 only
	def := newTieredTestDefinition("USD", 1)
	l2.addDefinition("tenant1", def)

	// Get: L1 miss, L2 hit
	_, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)

	stats := tiered.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(1), stats.L2Hits)
	assert.Equal(t, int64(0), stats.L2Misses)
	assert.Equal(t, int64(0), stats.SourceLoads)
}

// Integration test with real Redis

func TestTieredInstrumentCache_Integration_WithRedis(t *testing.T) {
	mr, client := setupMiniRedis(t)
	defer client.Close()
	_ = mr

	l1 := NewInstrumentCache()
	l2, err := NewRedisL2Cache(client)
	require.NoError(t, err)
	source := newMockSource()
	compiler := newMockCELCompiler()

	tiered := NewTieredInstrumentCache(l1, l2, source, compiler)
	ctx := newTestContext("tenant1")

	// Pre-populate source
	def := newTieredTestDefinition("USD", 1)
	def.DisplayName = "US Dollar"
	source.addDefinition("tenant1", def)

	// First get: populates L1 and L2
	result, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "US Dollar", result.Definition.DisplayName)

	// Verify L2 has the entry
	l2Entry := l2.Get(ctx, "USD", 1)
	require.NotNil(t, l2Entry)
	assert.Equal(t, "US Dollar", l2Entry.DisplayName)

	// Clear L1 to simulate pod restart
	l1.InvalidateAll(ctx)

	// Second get should hit L2 (not source)
	result2, err := tiered.Get(ctx, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "US Dollar", result2.Definition.DisplayName)

	// Source should only have been called once
	assert.Equal(t, int32(1), atomic.LoadInt32(&source.loadCount))
}

// Benchmark tests

func BenchmarkTieredInstrumentCache_L1Hit(b *testing.B) {
	l1 := NewInstrumentCache()
	l2 := &NoOpL2Cache{}
	source := newMockSource()

	tiered := NewTieredInstrumentCache(l1, l2, source, nil)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Pre-populate L1
	def := newTieredTestDefinition("USD", 1)
	l1.Put(ctx, "USD", 1, &CachedInstrument{Definition: def})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tiered.Get(ctx, "USD", 1)
	}
}

func BenchmarkTieredInstrumentCache_L2Hit(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	l1 := NewInstrumentCache()
	l2, _ := NewRedisL2Cache(client)
	source := newMockSource()

	tiered := NewTieredInstrumentCache(l1, l2, source, nil)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Pre-populate L2 only
	def := newTieredTestDefinition("USD", 1)
	l2.Put(ctx, "USD", 1, def)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Clear L1 for each iteration to measure L2 hit path
		l1.InvalidateAll(ctx)
		_, _ = tiered.Get(ctx, "USD", 1)
	}
}

func BenchmarkTieredInstrumentCache_Parallel(b *testing.B) {
	l1 := NewInstrumentCache()
	l2 := &NoOpL2Cache{}
	source := newMockSource()

	tiered := NewTieredInstrumentCache(l1, l2, source, nil)
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Pre-populate L1 with multiple entries
	for i := 0; i < 100; i++ {
		def := newTieredTestDefinition("INS", i)
		l1.Put(ctx, "INS", i, &CachedInstrument{Definition: def})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = tiered.Get(ctx, "INS", i%100)
			i++
		}
	})
}
