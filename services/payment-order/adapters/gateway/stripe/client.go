package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cenkalti/backoff/v4"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/sony/gobreaker/v2"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Client wraps a stripe-go Client and automatically applies the Connected Account
// header for all requests routed through a tenant.
type Client struct {
	// Raw is the underlying stripe-go Client for making API calls.
	// Callers must set params.SetStripeAccount(accountID) on each request;
	// use ApplyAccount as a convenience.
	Raw *stripego.Client

	// AccountID is the Stripe Connected Account ID for this tenant.
	AccountID string

	// WebhookEndpointSecret is the per-tenant webhook signing secret.
	WebhookEndpointSecret string
}

// ApplyAccount sets the Stripe-Account header on the given params.
// Use this before every API call to route the request to the Connected Account.
func (c *Client) ApplyAccount(params *stripego.Params) {
	params.SetStripeAccount(c.AccountID)
}

// ApplyAccountList sets the Stripe-Account header on list params.
func (c *Client) ApplyAccountList(params *stripego.ListParams) {
	params.SetStripeAccount(c.AccountID)
}

// ClientFactory creates tenant-scoped Stripe clients with caching,
// circuit breaker, and retry resilience.
type ClientFactory struct {
	apiKey         string
	configProvider TenantConfigProvider
	rawClient      *stripego.Client
	cache          *lru.Cache[string, *cachedTenantConfig]
	cacheTTL       time.Duration
	cb             *gobreaker.CircuitBreaker[TenantConfig]
	retryConfig    retrySettings
	logger         *slog.Logger
}

// cachedTenantConfig holds a tenant config with expiration tracking.
type cachedTenantConfig struct {
	config    TenantConfig
	expiresAt time.Time
}

// retrySettings holds retry configuration.
type retrySettings struct {
	maxRetries          int
	initialInterval     time.Duration
	maxInterval         time.Duration
	multiplier          float64
	randomizationFactor float64
}

// Factory errors.
var (
	ErrNilConfigProvider = errors.New("tenant config provider must not be nil")
	ErrCircuitOpen       = errors.New("circuit breaker is open")
	ErrMissingTenant     = errors.New("tenant context required for Stripe client")
)

// NewClientFactory creates a new ClientFactory.
func NewClientFactory(cfg Config, provider TenantConfigProvider, logger *slog.Logger) (*ClientFactory, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid stripe config: %w", err)
	}
	if provider == nil {
		return nil, ErrNilConfigProvider
	}
	if logger == nil {
		logger = slog.Default()
	}

	cache, err := lru.New[string, *cachedTenantConfig](cfg.TenantCacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create tenant config cache: %w", err)
	}

	cbSettings := gobreaker.Settings{
		Name:        cfg.CircuitBreakerName,
		MaxRequests: cfg.CircuitBreakerMaxRequests,
		Interval:    cfg.CircuitBreakerInterval,
		Timeout:     cfg.CircuitBreakerTimeout,
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			// Tenant-not-found is a business logic error, not an infrastructure failure.
			// Don't let missing tenant configs trip the breaker for other tenants.
			return errors.Is(err, ErrTenantConfigNotFound)
		},
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.CircuitBreakerFailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("stripe client factory circuit breaker state changed",
				"name", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	}

	return &ClientFactory{
		apiKey:         cfg.APIKey,
		configProvider: provider,
		rawClient:      stripego.NewClient(cfg.APIKey),
		cache:          cache,
		cacheTTL:       cfg.TenantCacheTTL,
		cb:             gobreaker.NewCircuitBreaker[TenantConfig](cbSettings),
		retryConfig: retrySettings{
			maxRetries:          cfg.MaxRetries,
			initialInterval:     cfg.RetryInitialInterval,
			maxInterval:         cfg.RetryMaxInterval,
			multiplier:          cfg.RetryMultiplier,
			randomizationFactor: cfg.RetryRandomizationFactor,
		},
		logger: logger,
	}, nil
}

// NewClient creates a tenant-scoped Stripe Client by retrieving the tenant's
// Connected Account configuration. The tenant ID is extracted from the context.
//
// Tenant configs are cached with TTL and fetched through a circuit breaker
// with exponential backoff retries.
func (f *ClientFactory) NewClient(ctx context.Context) (*Client, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrMissingTenant
	}

	tid := tenantID.String()

	cfg, err := f.getTenantConfig(ctx, tid)
	if err != nil {
		return nil, fmt.Errorf("failed to get stripe config for tenant %s: %w", tid, err)
	}

	f.logger.Debug("created stripe client for tenant",
		"tenant_id", tid,
		"connected_account_id", cfg.ConnectedAccountID,
	)

	return &Client{
		Raw:                   f.rawClient,
		AccountID:             cfg.ConnectedAccountID,
		WebhookEndpointSecret: cfg.WebhookEndpointSecret,
	}, nil
}

// getTenantConfig retrieves the tenant config from cache or provider.
func (f *ClientFactory) getTenantConfig(ctx context.Context, tenantID string) (TenantConfig, error) {
	// Check cache first
	if cached, ok := f.cache.Get(tenantID); ok {
		if time.Now().Before(cached.expiresAt) {
			return cached.config, nil
		}
		// Expired, remove from cache
		f.cache.Remove(tenantID)
	}

	// Fetch with circuit breaker and retry
	cfg, err := f.fetchWithResilience(ctx, tenantID)
	if err != nil {
		return TenantConfig{}, err
	}

	// Cache the result
	f.cache.Add(tenantID, &cachedTenantConfig{
		config:    cfg,
		expiresAt: time.Now().Add(f.cacheTTL),
	})

	return cfg, nil
}

// fetchWithResilience fetches tenant config through circuit breaker with retries.
func (f *ClientFactory) fetchWithResilience(ctx context.Context, tenantID string) (TenantConfig, error) {
	var result TenantConfig

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = f.retryConfig.initialInterval
	b.MaxInterval = f.retryConfig.maxInterval
	b.Multiplier = f.retryConfig.multiplier
	b.RandomizationFactor = f.retryConfig.randomizationFactor
	b.MaxElapsedTime = 0
	b.Reset()

	backoffWithContext := backoff.WithContext(b, ctx)

	attempt := 0
	maxAttempts := f.retryConfig.maxRetries + 1

	operation := func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		attempt++

		cfg, err := f.cb.Execute(func() (TenantConfig, error) {
			return f.configProvider.GetTenantConfig(tenantID)
		})
		if err != nil {
			// Circuit breaker open - don't retry
			if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
				f.logger.Warn("stripe config circuit breaker open",
					"tenant_id", tenantID,
					"attempt", attempt,
				)
				return backoff.Permanent(fmt.Errorf("%w: %v", ErrCircuitOpen, err)) //nolint:errorlint // second error is context-only
			}

			// Business logic errors - don't retry or log as infrastructure failure
			if errors.Is(err, ErrTenantConfigNotFound) {
				return backoff.Permanent(err)
			}

			// Infrastructure failure - check retry budget
			if attempt >= maxAttempts {
				f.logger.Error("stripe config fetch failed after max retries",
					"tenant_id", tenantID,
					"attempts", attempt,
					"error", err,
				)
				return backoff.Permanent(err)
			}

			f.logger.Debug("stripe config fetch failed, retrying",
				"tenant_id", tenantID,
				"attempt", attempt,
				"error", err,
			)
			return err
		}

		result = cfg
		return nil
	}

	if err := backoff.Retry(operation, backoffWithContext); err != nil {
		return TenantConfig{}, fmt.Errorf("stripe config fetch failed: %w", err)
	}

	return result, nil
}

// InvalidateTenantConfig removes a tenant's cached config, forcing a refresh
// on the next NewClient call. Use when a tenant's Stripe config changes.
func (f *ClientFactory) InvalidateTenantConfig(tenantID string) {
	f.cache.Remove(tenantID)
	f.logger.Debug("invalidated stripe config cache", "tenant_id", tenantID)
}

// CircuitBreakerState returns the current state of the circuit breaker.
func (f *ClientFactory) CircuitBreakerState() gobreaker.State {
	return f.cb.State()
}
