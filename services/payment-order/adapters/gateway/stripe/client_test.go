package stripe

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockConfigProvider is a test TenantConfigProvider.
type mockConfigProvider struct {
	mu        sync.Mutex
	configs   map[string]TenantConfig
	err       error
	callCount atomic.Int64
}

func newMockProvider(configs map[string]TenantConfig) *mockConfigProvider {
	return &mockConfigProvider{configs: configs}
}

func (m *mockConfigProvider) GetTenantConfig(tenantID string) (TenantConfig, error) {
	m.callCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return TenantConfig{}, m.err
	}
	cfg, ok := m.configs[tenantID]
	if !ok {
		return TenantConfig{}, ErrTenantConfigNotFound
	}
	return cfg, nil
}

func (m *mockConfigProvider) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.APIKey = "sk_test_123"
	// Speed up tests
	cfg.TenantCacheTTL = 100 * time.Millisecond
	cfg.RetryInitialInterval = 10 * time.Millisecond
	cfg.RetryMaxInterval = 50 * time.Millisecond
	cfg.CircuitBreakerTimeout = 100 * time.Millisecond
	cfg.CircuitBreakerInterval = 100 * time.Millisecond
	return cfg
}

func tenantCtx(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

func TestNewClientFactory_ValidConfig(t *testing.T) {
	provider := newMockProvider(nil)
	factory, err := NewClientFactory(testConfig(), provider, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, factory)
}

func TestNewClientFactory_InvalidConfig(t *testing.T) {
	cfg := DefaultConfig()
	// No API key
	_, err := NewClientFactory(cfg, newMockProvider(nil), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyAPIKey)
}

func TestNewClientFactory_NilProvider(t *testing.T) {
	_, err := NewClientFactory(testConfig(), nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilConfigProvider)
}

func TestClientFactory_NewClient_Success(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{
		"tenant_a": {
			ConnectedAccountID:    "acct_tenant_a",
			WebhookEndpointSecret: "whsec_tenant_a",
		},
	})

	factory, err := NewClientFactory(testConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx := tenantCtx("tenant_a")
	client, err := factory.NewClient(ctx)
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.Equal(t, "acct_tenant_a", client.AccountID)
	assert.Equal(t, "whsec_tenant_a", client.WebhookEndpointSecret)
	assert.NotNil(t, client.Raw)
}

func TestClientFactory_NewClient_MissingTenantContext(t *testing.T) {
	factory, err := NewClientFactory(testConfig(), newMockProvider(nil), nil)
	require.NoError(t, err)

	_, err = factory.NewClient(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingTenant)
}

func TestClientFactory_NewClient_TenantNotFound(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{})
	factory, err := NewClientFactory(testConfig(), provider, nil)
	require.NoError(t, err)

	ctx := tenantCtx("nonexistent")
	_, err = factory.NewClient(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

func TestClientFactory_NewClient_CachesConfig(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{
		"tenant_a": {ConnectedAccountID: "acct_tenant_a"},
	})

	factory, err := NewClientFactory(testConfig(), provider, nil)
	require.NoError(t, err)

	ctx := tenantCtx("tenant_a")

	// First call fetches from provider
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), provider.callCount.Load())

	// Second call should use cache
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), provider.callCount.Load(), "should use cached config")
}

func TestClientFactory_NewClient_CacheExpires(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{
		"tenant_a": {ConnectedAccountID: "acct_tenant_a"},
	})

	cfg := testConfig()
	cfg.TenantCacheTTL = 50 * time.Millisecond

	factory, err := NewClientFactory(cfg, provider, nil)
	require.NoError(t, err)

	ctx := tenantCtx("tenant_a")

	// First call
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), provider.callCount.Load())

	// Wait for cache to expire
	time.Sleep(60 * time.Millisecond) //nolint:forbidigo // triggers cache TTL expiry

	// Second call should fetch again
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), provider.callCount.Load(), "should re-fetch after cache expiry")
}

func TestClientFactory_InvalidateTenantConfig(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{
		"tenant_a": {ConnectedAccountID: "acct_tenant_a"},
	})

	factory, err := NewClientFactory(testConfig(), provider, nil)
	require.NoError(t, err)

	ctx := tenantCtx("tenant_a")

	// Populate cache
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), provider.callCount.Load())

	// Invalidate
	factory.InvalidateTenantConfig("tenant_a")

	// Next call should re-fetch
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), provider.callCount.Load())
}

func TestClientFactory_CircuitBreaker_TripsOnRepeatedFailures(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{})
	provider.setError(errors.New("provider unavailable"))

	cfg := testConfig()
	cfg.MaxRetries = 0
	cfg.CircuitBreakerFailureThreshold = 3

	factory, err := NewClientFactory(cfg, provider, nil)
	require.NoError(t, err)

	ctx := tenantCtx("tenant_a")

	// Fail enough times to trip the breaker
	for i := 0; i < 3; i++ {
		_, _ = factory.NewClient(ctx)
	}

	// Circuit should now be open
	assert.Equal(t, gobreaker.StateOpen, factory.CircuitBreakerState())

	// Next call should fail fast with circuit open error
	_, err = factory.NewClient(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitOpen)
}

func TestClientFactory_CircuitBreaker_TenantNotFoundDoesNotTrip(t *testing.T) {
	// ErrTenantConfigNotFound is a business error, not infrastructure failure.
	// It should NOT trip the circuit breaker.
	provider := newMockProvider(map[string]TenantConfig{})
	// Provider returns ErrTenantConfigNotFound for unknown tenants (default behavior)

	cfg := testConfig()
	cfg.MaxRetries = 0
	cfg.CircuitBreakerFailureThreshold = 3

	factory, err := NewClientFactory(cfg, provider, nil)
	require.NoError(t, err)

	// Request config for many nonexistent tenants
	for i := 0; i < 10; i++ {
		ctx := tenantCtx("nonexistent")
		_, err := factory.NewClient(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTenantConfigNotFound)
	}

	// Circuit should still be closed
	assert.Equal(t, gobreaker.StateClosed, factory.CircuitBreakerState(),
		"tenant-not-found errors should not trip the circuit breaker")
}

func TestClientFactory_ContextCancellation(t *testing.T) {
	provider := newMockProvider(map[string]TenantConfig{})
	provider.setError(errors.New("slow provider"))

	cfg := testConfig()
	cfg.MaxRetries = 5 // Many retries, but context will cancel first

	factory, err := NewClientFactory(cfg, provider, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(tenantCtx("tenant_a"))
	cancel() // Cancel immediately

	_, err = factory.NewClient(ctx)
	require.Error(t, err)
}

func TestClient_ApplyAccount(t *testing.T) {
	client := &Client{AccountID: "acct_test_123"}

	params := &stripego.Params{}
	client.ApplyAccount(params)

	require.NotNil(t, params.StripeAccount)
	assert.Equal(t, "acct_test_123", *params.StripeAccount)
}

func TestClient_ApplyAccountList(t *testing.T) {
	client := &Client{AccountID: "acct_test_456"}

	params := &stripego.ListParams{}
	client.ApplyAccountList(params)

	require.NotNil(t, params.StripeAccount)
	assert.Equal(t, "acct_test_456", *params.StripeAccount)
}
