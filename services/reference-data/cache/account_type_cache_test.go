package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockAccountTypeLoader is a test double for AccountTypeLoader.
type mockAccountTypeLoader struct {
	mu              sync.Mutex
	definitions     map[string]map[string]*accounttype.Definition // tenantID -> code -> definition
	listDefinitions map[string][]*accounttype.Definition          // tenantID -> definitions
	loadCallCount   atomic.Int32
	loadErr         error
	listErr         error
}

func newMockAccountTypeLoader() *mockAccountTypeLoader {
	return &mockAccountTypeLoader{
		definitions:     make(map[string]map[string]*accounttype.Definition),
		listDefinitions: make(map[string][]*accounttype.Definition),
	}
}

func (m *mockAccountTypeLoader) LoadAccountType(ctx context.Context, code string) (*accounttype.Definition, error) {
	m.loadCallCount.Add(1)

	if m.loadErr != nil {
		return nil, m.loadErr
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	defs, ok := m.definitions[string(tenantID)]
	if !ok {
		return nil, accounttype.ErrNotFound
	}
	def, ok := defs[code]
	if !ok {
		return nil, accounttype.ErrNotFound
	}
	return def, nil
}

func (m *mockAccountTypeLoader) ListActiveAccountTypes(ctx context.Context) ([]*accounttype.Definition, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrTenantContextRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.listDefinitions[string(tenantID)], nil
}

func (m *mockAccountTypeLoader) addDefinition(tenantID string, def *accounttype.Definition) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.definitions[tenantID] == nil {
		m.definitions[tenantID] = make(map[string]*accounttype.Definition)
	}
	m.definitions[tenantID][def.Code] = def
	m.listDefinitions[tenantID] = append(m.listDefinitions[tenantID], def)
}

// mockAccountTypeCELCompiler is a test double for AccountTypeCELCompiler.
type mockAccountTypeCELCompiler struct {
	validationErr  error
	bucketingErr   error
	eligibilityErr error
	// Use real CEL programs for non-nil return
	returnPrograms bool
}

func (m *mockAccountTypeCELCompiler) CompileValidation(_ string) (cel.Program, error) {
	if m.validationErr != nil {
		return nil, m.validationErr
	}
	if m.returnPrograms {
		return newStubCELProgram(), nil
	}
	return nil, nil
}

func (m *mockAccountTypeCELCompiler) CompileBucketKey(_ string) (cel.Program, error) {
	if m.bucketingErr != nil {
		return nil, m.bucketingErr
	}
	if m.returnPrograms {
		return newStubCELProgram(), nil
	}
	return nil, nil
}

func (m *mockAccountTypeCELCompiler) CompileEligibility(_ string) (cel.Program, error) {
	if m.eligibilityErr != nil {
		return nil, m.eligibilityErr
	}
	if m.returnPrograms {
		return newStubCELProgram(), nil
	}
	return nil, nil
}

// stubCELProgram is a minimal cel.Program implementation for testing.
type stubCELProgram struct{}

func newStubCELProgram() cel.Program { return &stubCELProgram{} }

func (s *stubCELProgram) Eval(_ any) (ref.Val, *cel.EvalDetails, error) {
	return nil, nil, nil
}

func (s *stubCELProgram) ContextEval(_ context.Context, _ any) (ref.Val, *cel.EvalDetails, error) {
	return nil, nil, nil
}

func newTestAccountTypeContext(tenantID string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.MustNewTenantID(tenantID))
}

func newTestAccountTypeDef(code string, version int, isSystem bool) *accounttype.Definition {
	return &accounttype.Definition{
		ID:              uuid.New(),
		Code:            code,
		Version:         version,
		DisplayName:     code + " Account",
		NormalBalance:   accounttype.NormalBalanceCredit,
		BehaviorClass:   accounttype.BehaviorClassCustomer,
		InstrumentCode:  "GBP",
		ValidationCEL:   "true",
		BucketingCEL:    `"default"`,
		EligibilityCEL:  "true",
		AttributeSchema: json.RawMessage(`{"type":"object"}`),
		Status:          accounttype.StatusActive,
		IsSystem:        isSystem,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func newTestCache(loader AccountTypeLoader, compiler AccountTypeCELCompiler, opts ...AccountTypeCacheOption) *LocalAccountTypeCache {
	return NewLocalAccountTypeCache(loader, compiler, opts...)
}

// --- Tests ---

func TestLocalAccountTypeCache_CacheHitReturnsWithoutCallingLoader(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	// Load once to populate cache
	result, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "CUSTOMER_CURRENT", result.Definition.Code)
	assert.Equal(t, int32(1), loader.loadCallCount.Load())

	// Second call should hit cache, not loader
	result2, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, "CUSTOMER_CURRENT", result2.Definition.Code)
	assert.Equal(t, int32(1), loader.loadCallCount.Load(), "loader should not be called on cache hit")
}

func TestLocalAccountTypeCache_SingleflightDeduplication(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")
	const concurrency = 10

	var wg sync.WaitGroup
	startCh := make(chan struct{})
	results := make([]*CachedAccountType, concurrency)
	errs := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-startCh
			results[idx], errs[idx] = cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
		}(i)
	}

	close(startCh)
	wg.Wait()

	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i], "request %d failed", i)
		require.NotNil(t, results[i], "request %d returned nil", i)
		assert.Equal(t, "CUSTOMER_CURRENT", results[i].Definition.Code)
	}

	// Singleflight should have deduplicated to exactly 1 call
	assert.Equal(t, int32(1), loader.loadCallCount.Load(),
		"singleflight should deduplicate %d concurrent requests to 1 load call", concurrency)
}

func TestLocalAccountTypeCache_TTLExpiry_RegularDefinition(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler,
		WithAccountTypeTTL(50*time.Millisecond, 0), // No jitter for predictable tests
	)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	// Load to populate cache
	result, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should be cached
	cached := cache.Get(ctx, def.Code, def.Version)
	require.NotNil(t, cached)

	// Wait for TTL expiry
	time.Sleep(60 * time.Millisecond) //nolint:forbidigo // triggers TTL expiry to test cache expiration

	// Should be expired
	expired := cache.Get(ctx, def.Code, def.Version)
	assert.Nil(t, expired, "regular entry should be expired and removed")
}

func TestLocalAccountTypeCache_SystemBlueprintGets24HourTTL(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("SYSTEM_NOSTRO", 1, true)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	result, err := cache.GetOrLoad(ctx, tenantID, "SYSTEM_NOSTRO")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check TTL is approximately 24 hours
	ttl := result.ExpiresAt().Sub(result.CachedAt())
	assert.True(t, ttl >= 23*time.Hour && ttl <= 25*time.Hour,
		"system blueprint TTL should be ~24h, got %v", ttl)
}

func TestLocalAccountTypeCache_TenantIsolation(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	defA := newTestAccountTypeDef("ACCOUNT_A", 1, false)
	defB := newTestAccountTypeDef("ACCOUNT_B", 1, false)
	loader.addDefinition("tenantA", defA)
	loader.addDefinition("tenantB", defB)

	ctxA := newTestAccountTypeContext("tenantA")
	ctxB := newTestAccountTypeContext("tenantB")
	tenantA := tenant.MustNewTenantID("tenantA")
	tenantB := tenant.MustNewTenantID("tenantB")

	// Load for each tenant
	resultA, err := cache.GetOrLoad(ctxA, tenantA, "ACCOUNT_A")
	require.NoError(t, err)
	require.NotNil(t, resultA)

	resultB, err := cache.GetOrLoad(ctxB, tenantB, "ACCOUNT_B")
	require.NoError(t, err)
	require.NotNil(t, resultB)

	// Tenant A should not see tenant B's data
	assert.Nil(t, cache.Get(ctxA, "ACCOUNT_B", 1), "tenant A should not see tenant B's cache")

	// Tenant B should not see tenant A's data
	assert.Nil(t, cache.Get(ctxB, "ACCOUNT_A", 1), "tenant B should not see tenant A's cache")
}

func TestLocalAccountTypeCache_PrefetchAccountTypes(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def1 := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	def2 := newTestAccountTypeDef("SYSTEM_NOSTRO", 1, true)
	def3 := newTestAccountTypeDef("CLEARING", 1, false)
	loader.addDefinition("tenant1", def1)
	loader.addDefinition("tenant1", def2)
	loader.addDefinition("tenant1", def3)

	tenantIDs := []tenant.TenantID{tenant.MustNewTenantID("tenant1")}

	err := cache.PrefetchAccountTypes(context.Background(), tenantIDs)
	require.NoError(t, err)

	ctx := newTestAccountTypeContext("tenant1")
	size, _ := cache.Stats(ctx)
	assert.Equal(t, 3, size, "all 3 definitions should be cached")

	// Verify specific entries
	assert.NotNil(t, cache.Get(ctx, "CUSTOMER_CURRENT", 1))
	assert.NotNil(t, cache.Get(ctx, "SYSTEM_NOSTRO", 1))
	assert.NotNil(t, cache.Get(ctx, "CLEARING", 1))
}

func TestLocalAccountTypeCache_PrefetchAccountTypes_CompilesCELPrograms(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	tenantIDs := []tenant.TenantID{tenant.MustNewTenantID("tenant1")}

	err := cache.PrefetchAccountTypes(context.Background(), tenantIDs)
	require.NoError(t, err)

	ctx := newTestAccountTypeContext("tenant1")
	cached := cache.Get(ctx, "CUSTOMER_CURRENT", 1)
	require.NotNil(t, cached)

	// EligibilityProgram should be non-nil after cache population
	assert.NotNil(t, cached.EligibilityProgram, "EligibilityProgram should be compiled and non-nil")
	assert.NotNil(t, cached.ValidationProgram, "ValidationProgram should be compiled and non-nil")
	assert.NotNil(t, cached.BucketingProgram, "BucketingProgram should be compiled and non-nil")
}

func TestLocalAccountTypeCache_ExpiredSystemBlueprintTriggersBackgroundRefresh(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler,
		WithAccountTypeTTL(50*time.Millisecond, 0),
	)

	def := newTestAccountTypeDef("SYSTEM_NOSTRO", 1, true)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")

	// Manually put with short TTL to simulate expiry
	entry, err := cache.compileCachedEntry(def)
	require.NoError(t, err)

	// Force a short expiry for system blueprint
	now := time.Now()
	entry.cachedAt = now
	entry.expiresAt = now.Add(50 * time.Millisecond)
	entry.isSystem = true

	tenantID := tenant.MustNewTenantID("tenant1")
	lruCache := cache.getOrCreateTenantCache(tenantID)
	lruCache.Add(AccountTypeKey{Code: "SYSTEM_NOSTRO", Version: 1}, entry)

	// Wait for expiry
	time.Sleep(60 * time.Millisecond) //nolint:forbidigo // triggers TTL expiry to test stale-while-revalidate behavior

	// Get should return stale entry (not nil) and trigger background refresh
	result := cache.Get(ctx, "SYSTEM_NOSTRO", 1)
	assert.NotNil(t, result, "expired system blueprint should return stale entry, not nil")
	assert.Equal(t, "SYSTEM_NOSTRO", result.Definition.Code)

	// Wait for background refresh to complete
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			refreshed := cache.Get(ctx, "SYSTEM_NOSTRO", 1)
			return refreshed != nil && refreshed.ExpiresAt().After(time.Now())
		})
	require.NoError(t, err, "background refresh should complete with fresh TTL")

	// After refresh, entry should have a fresh TTL
	refreshed := cache.Get(ctx, "SYSTEM_NOSTRO", 1)
	require.NotNil(t, refreshed)
	assert.True(t, refreshed.ExpiresAt().After(time.Now()), "refreshed entry should have future expiry")
}

func TestLocalAccountTypeCache_Invalidate(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	_, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)

	// Verify cached
	assert.NotNil(t, cache.Get(ctx, "CUSTOMER_CURRENT", 1))

	// Invalidate
	cache.Invalidate(ctx, "CUSTOMER_CURRENT", 1)

	// Should be gone
	assert.Nil(t, cache.Get(ctx, "CUSTOMER_CURRENT", 1))
}

func TestLocalAccountTypeCache_InvalidateAll(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def1 := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	def2 := newTestAccountTypeDef("CLEARING", 1, false)
	loader.addDefinition("tenant1", def1)
	loader.addDefinition("tenant1", def2)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	_, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	_, err = cache.GetOrLoad(ctx, tenantID, "CLEARING")
	require.NoError(t, err)

	// Both cached
	size, _ := cache.Stats(ctx)
	assert.Equal(t, 2, size)

	// Invalidate all
	cache.InvalidateAll(ctx)

	// Both gone
	assert.Nil(t, cache.Get(ctx, "CUSTOMER_CURRENT", 1))
	assert.Nil(t, cache.Get(ctx, "CLEARING", 1))
}

func TestLocalAccountTypeCache_LoadError(t *testing.T) {
	loader := newMockAccountTypeLoader()
	loader.loadErr = errors.New("gRPC unavailable")
	compiler := &mockAccountTypeCELCompiler{}
	cache := newTestCache(loader, compiler)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	result, err := cache.GetOrLoad(ctx, tenantID, "NONEXISTENT")
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestLocalAccountTypeCache_PrefetchMultipleTenants(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	defA := newTestAccountTypeDef("ACCOUNT_A", 1, false)
	defB := newTestAccountTypeDef("ACCOUNT_B", 1, true)
	loader.addDefinition("tenantX", defA)
	loader.addDefinition("tenantY", defB)

	tenantIDs := []tenant.TenantID{
		tenant.MustNewTenantID("tenantX"),
		tenant.MustNewTenantID("tenantY"),
	}

	err := cache.PrefetchAccountTypes(context.Background(), tenantIDs)
	require.NoError(t, err)

	ctxX := newTestAccountTypeContext("tenantX")
	ctxY := newTestAccountTypeContext("tenantY")

	assert.NotNil(t, cache.Get(ctxX, "ACCOUNT_A", 1))
	assert.NotNil(t, cache.Get(ctxY, "ACCOUNT_B", 1))

	// Tenant isolation: X should not see Y's data
	assert.Nil(t, cache.Get(ctxX, "ACCOUNT_B", 1))
	assert.Nil(t, cache.Get(ctxY, "ACCOUNT_A", 1))
}

func TestLocalAccountTypeCache_JitteredTTLVariance(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler,
		WithAccountTypeTTL(100*time.Millisecond, 50*time.Millisecond),
	)

	ctx := newTestAccountTypeContext("tenant1")

	var expirations []time.Duration
	for i := 0; i < 20; i++ {
		def := newTestAccountTypeDef("TYPE", i, false)
		entry, err := cache.compileCachedEntry(def)
		require.NoError(t, err)
		cache.Put(ctx, entry)

		cached := cache.Get(ctx, "TYPE", i)
		require.NotNil(t, cached)
		expirations = append(expirations, cached.ExpiresAt().Sub(cached.CachedAt()))
	}

	minExp := expirations[0]
	maxExp := expirations[0]
	for _, exp := range expirations[1:] {
		if exp < minExp {
			minExp = exp
		}
		if exp > maxExp {
			maxExp = exp
		}
	}

	diff := maxExp - minExp
	assert.Greater(t, diff.Milliseconds(), int64(0), "jitter should produce variance in TTL")
}

func TestLocalAccountTypeCache_NilCELCompiler(t *testing.T) {
	loader := newMockAccountTypeLoader()
	cache := newTestCache(loader, nil) // nil compiler

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	result, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "CUSTOMER_CURRENT", result.Definition.Code)

	// Programs should be nil when compiler is nil
	assert.Nil(t, result.ValidationProgram)
	assert.Nil(t, result.BucketingProgram)
	assert.Nil(t, result.EligibilityProgram)
	// Schema should still compile since it doesn't use CEL compiler
	assert.NotNil(t, result.CompiledSchema)
}

func TestLocalAccountTypeCache_CompiledSchemaPopulated(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	def.AttributeSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"tier": {"type": "string", "enum": ["GOLD", "SILVER", "BRONZE"]}
		},
		"required": ["tier"]
	}`)
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	result, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.CompiledSchema, "compiled JSON Schema should be non-nil")
}

func TestLocalAccountTypeCache_NoAttributeSchema(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	def.AttributeSchema = nil // No schema
	loader.addDefinition("tenant1", def)

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	result, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.CompiledSchema, "compiled JSON Schema should be nil when no schema defined")
}

func TestLocalAccountTypeCache_Stats(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler, WithAccountTypeCacheSize(100))

	ctx := newTestAccountTypeContext("tenant1")

	// Initially empty
	size, capacity := cache.Stats(ctx)
	assert.Equal(t, 0, size)
	assert.Equal(t, 0, capacity)

	// Add entry
	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)
	tenantID := tenant.MustNewTenantID("tenant1")
	_, err := cache.GetOrLoad(ctx, tenantID, "CUSTOMER_CURRENT")
	require.NoError(t, err)

	size, capacity = cache.Stats(ctx)
	assert.Equal(t, 1, size)
	assert.Equal(t, 100, capacity)
}

func TestLocalAccountTypeCache_MissingTenantContext(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{}
	cache := newTestCache(loader, compiler)

	ctx := context.Background() // No tenant

	result := cache.Get(ctx, "CUSTOMER_CURRENT", 1)
	assert.Nil(t, result)

	size, capacity := cache.Stats(ctx)
	assert.Equal(t, 0, size)
	assert.Equal(t, 0, capacity)
}

func TestLocalAccountTypeCache_PrefetchContextCancellation(t *testing.T) {
	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	def := newTestAccountTypeDef("CUSTOMER_CURRENT", 1, false)
	loader.addDefinition("tenant1", def)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	tenantIDs := []tenant.TenantID{tenant.MustNewTenantID("tenant1")}
	err := cache.PrefetchAccountTypes(ctx, tenantIDs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefetch cancelled")
}

func TestLocalAccountTypeCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	loader := newMockAccountTypeLoader()
	compiler := &mockAccountTypeCELCompiler{returnPrograms: true}
	cache := newTestCache(loader, compiler)

	for i := 0; i < 10; i++ {
		def := newTestAccountTypeDef("TYPE_"+string(rune('A'+i)), 1, false)
		loader.addDefinition("tenant1", def)
	}

	ctx := newTestAccountTypeContext("tenant1")
	tenantID := tenant.MustNewTenantID("tenant1")

	var wg sync.WaitGroup

	// Concurrent readers and loaders
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			code := "TYPE_" + string(rune('A'+idx%10))
			_, _ = cache.GetOrLoad(ctx, tenantID, code)
			_ = cache.Get(ctx, code, 1)
		}(i)
	}

	// Concurrent invalidators
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			code := "TYPE_" + string(rune('A'+idx%10))
			cache.Invalidate(ctx, code, 1)
		}(i)
	}

	wg.Wait()
	// No panics means test passes
}
