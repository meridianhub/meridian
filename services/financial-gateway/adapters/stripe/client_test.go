package stripe

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sony/gobreaker/v2"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// --- test helpers ---

type stubConfigProvider struct {
	configs   map[string]TenantConfig
	callCount int
	err       error
}

func (p *stubConfigProvider) GetTenantConfig(tenantID string) (TenantConfig, error) {
	p.callCount++
	if p.err != nil {
		return TenantConfig{}, p.err
	}
	cfg, ok := p.configs[tenantID]
	if !ok {
		return TenantConfig{}, ErrTenantConfigNotFound
	}
	return cfg, nil
}

func validConfig() Config {
	return Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}
}

func clientTenantCtx(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

// --- NewClientFactory ---

func TestNewClientFactory_Success(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{},
	}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)
	assert.NotNil(t, factory)
}

func TestNewClientFactory_NilProvider(t *testing.T) {
	_, err := NewClientFactory(validConfig(), nil, slog.Default())
	require.ErrorIs(t, err, ErrNilConfigProvider)
}

func TestNewClientFactory_InvalidConfig(t *testing.T) {
	provider := &stubConfigProvider{}
	_, err := NewClientFactory(Config{}, provider, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stripe config")
}

func TestNewClientFactory_NilLogger(t *testing.T) {
	provider := &stubConfigProvider{configs: map[string]TenantConfig{}}
	factory, err := NewClientFactory(validConfig(), provider, nil)
	require.NoError(t, err)
	assert.NotNil(t, factory)
}

// --- NewClient ---

func TestNewClient_Success(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{
			"tenant-a": {
				ConnectedAccountID:    "acct_123",
				WebhookEndpointSecret: "whsec_test",
			},
		},
	}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx := clientTenantCtx("tenant-a")
	client, err := factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, "acct_123", client.AccountID)
	assert.Equal(t, "whsec_test", client.WebhookEndpointSecret)
	assert.NotNil(t, client.Raw)
}

func TestNewClient_MissingTenant(t *testing.T) {
	provider := &stubConfigProvider{configs: map[string]TenantConfig{}}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	_, err = factory.NewClient(context.Background())
	require.ErrorIs(t, err, ErrMissingTenant)
}

func TestNewClient_TenantNotFound(t *testing.T) {
	provider := &stubConfigProvider{configs: map[string]TenantConfig{}}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx := clientTenantCtx("missing-tenant")
	_, err = factory.NewClient(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

// --- Caching ---

func TestNewClient_CacheHit(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{
			"tenant-a": {ConnectedAccountID: "acct_123"},
		},
	}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx := clientTenantCtx("tenant-a")

	// First call populates cache
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, provider.callCount)

	// Second call uses cache
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, provider.callCount, "should use cached config")
}

func TestNewClient_CacheExpiry(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{
			"tenant-a": {ConnectedAccountID: "acct_123"},
		},
	}
	cfg := validConfig()
	cfg.TenantCacheTTL = time.Millisecond // very short TTL
	factory, err := NewClientFactory(cfg, provider, slog.Default())
	require.NoError(t, err)

	ctx := clientTenantCtx("tenant-a")

	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, provider.callCount)

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond)

	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, provider.callCount, "should refetch after cache expiry")
}

// --- InvalidateTenantConfig ---

func TestInvalidateTenantConfig(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{
			"tenant-a": {ConnectedAccountID: "acct_123"},
		},
	}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx := clientTenantCtx("tenant-a")

	// Populate cache
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, provider.callCount)

	// Invalidate
	factory.InvalidateTenantConfig("tenant-a")

	// Next call should refetch
	_, err = factory.NewClient(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, provider.callCount, "should refetch after invalidation")
}

// --- CircuitBreakerState ---

func TestCircuitBreakerState_DefaultClosed(t *testing.T) {
	provider := &stubConfigProvider{configs: map[string]TenantConfig{}}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	state := factory.CircuitBreakerState(tenant.TenantID("new-tenant"))
	assert.Equal(t, gobreaker.StateClosed, state)
}

// --- Client helpers ---

func TestApplyAccount(t *testing.T) {
	c := &Client{AccountID: "acct_test"}

	t.Run("sets stripe account on params", func(t *testing.T) {
		params := &stripego.Params{}
		c.ApplyAccount(params)
		require.NotNil(t, params.StripeAccount)
		assert.Equal(t, "acct_test", *params.StripeAccount)
	})

	t.Run("sets stripe account on list params", func(t *testing.T) {
		params := &stripego.ListParams{}
		c.ApplyAccountList(params)
		require.NotNil(t, params.StripeAccount)
		assert.Equal(t, "acct_test", *params.StripeAccount)
	})
}

// --- WithStripeAccount / AccountFromContext ---

func TestWithStripeAccount_RoundTrip(t *testing.T) {
	ctx := WithStripeAccount(context.Background(), "acct_abc")
	id, ok := AccountFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "acct_abc", id)
}

func TestAccountFromContext_Missing(t *testing.T) {
	_, ok := AccountFromContext(context.Background())
	assert.False(t, ok)
}

func TestAccountFromContext_EmptyString(t *testing.T) {
	ctx := WithStripeAccount(context.Background(), "")
	_, ok := AccountFromContext(ctx)
	assert.False(t, ok, "empty string should return false")
}

// --- Context cancellation during fetch ---

func TestNewClient_ContextCancelled(t *testing.T) {
	provider := &stubConfigProvider{
		configs: map[string]TenantConfig{},
		err:     errors.New("should not be called"),
	}
	factory, err := NewClientFactory(validConfig(), provider, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(clientTenantCtx("tenant-a"))
	cancel() // cancel immediately

	_, err = factory.NewClient(ctx)
	require.Error(t, err)
}
