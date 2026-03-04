package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/metadata"
)

var (
	// ErrNilManifestClient is returned when the ManifestHistoryService client is nil.
	ErrNilManifestClient = errors.New("manifest history service client is required")
	// ErrNilLogger is returned when the logger is nil.
	ErrNilLogger = errors.New("logger is required")
)

// ManifestTenantConfigProvider retrieves Stripe Connect configuration for a tenant
// by querying the control-plane ManifestHistoryService gRPC endpoint.
// Results are cached with a configurable TTL to avoid hitting the control-plane on every request.
type ManifestTenantConfigProvider struct {
	client controlplanev1.ManifestHistoryServiceClient
	logger *slog.Logger
	ttl    time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// cacheEntry stores a cached TenantConfig with an expiration time.
type cacheEntry struct {
	config    TenantConfig
	expiresAt time.Time
}

// ManifestTenantConfigProviderConfig holds configuration for ManifestTenantConfigProvider.
type ManifestTenantConfigProviderConfig struct {
	// Client is the gRPC client for ManifestHistoryService.
	Client controlplanev1.ManifestHistoryServiceClient
	// Logger is the structured logger.
	Logger *slog.Logger
	// CacheTTL is the duration to cache tenant configs. Defaults to 5 minutes.
	CacheTTL time.Duration
}

const defaultCacheTTL = 5 * time.Minute

// stripeConnectProvider is the payment rails provider name for Stripe Connect.
const stripeConnectProvider = "stripe_connect"

// NewManifestTenantConfigProvider creates a new ManifestTenantConfigProvider.
func NewManifestTenantConfigProvider(cfg ManifestTenantConfigProviderConfig) (*ManifestTenantConfigProvider, error) {
	if cfg.Client == nil {
		return nil, ErrNilManifestClient
	}
	if cfg.Logger == nil {
		return nil, ErrNilLogger
	}
	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = defaultCacheTTL
	}
	return &ManifestTenantConfigProvider{
		client: cfg.Client,
		logger: cfg.Logger,
		ttl:    ttl,
		cache:  make(map[string]cacheEntry),
	}, nil
}

// GetTenantConfig returns the Stripe Connect config for the given tenant.
// Returns ErrTenantConfigNotFound if the tenant has no payment_rails with stripe_connect provider.
func (p *ManifestTenantConfigProvider) GetTenantConfig(tenantID string) (TenantConfig, error) {
	// Check cache first
	p.mu.RLock()
	entry, ok := p.cache[tenantID]
	p.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.config, nil
	}

	// Cache miss or expired - fetch from control-plane
	cfg, err := p.fetchFromManifest(tenantID)
	if err != nil {
		return TenantConfig{}, err
	}

	// Store in cache
	p.mu.Lock()
	p.cache[tenantID] = cacheEntry{
		config:    cfg,
		expiresAt: time.Now().Add(p.ttl),
	}
	p.mu.Unlock()

	return cfg, nil
}

// fetchFromManifest calls GetCurrentManifest on the control-plane with the tenant's context
// and extracts the Stripe Connect configuration from the payment_rails section.
func (p *ManifestTenantConfigProvider) fetchFromManifest(tenantID string) (TenantConfig, error) {
	// Set tenant ID in outgoing gRPC metadata
	ctx := metadata.AppendToOutgoingContext(
		context.Background(),
		tenant.TenantIDKey, tenantID,
	)

	resp, err := p.client.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
	if err != nil {
		return TenantConfig{}, fmt.Errorf("failed to get manifest for tenant %s: %w", tenantID, err)
	}

	manifest := resp.GetVersion().GetManifest()
	if manifest == nil {
		return TenantConfig{}, ErrTenantConfigNotFound
	}

	// Find stripe_connect payment rail
	for _, rail := range manifest.GetPaymentRails() {
		if rail.GetProvider() != stripeConnectProvider {
			continue
		}

		cfg := TenantConfig{
			ConnectedAccountID:    rail.GetAccountId(),
			WebhookEndpointSecret: rail.GetWebhookEndpointSecret(),
		}

		if err := cfg.Validate(); err != nil {
			p.logger.Warn("invalid stripe config in manifest",
				"tenant_id", tenantID,
				"error", err)
			return TenantConfig{}, fmt.Errorf("invalid stripe config for tenant %s: %w", tenantID, err)
		}

		p.logger.Debug("loaded stripe config from manifest",
			"tenant_id", tenantID,
			"connected_account_id", cfg.ConnectedAccountID)

		return cfg, nil
	}

	return TenantConfig{}, ErrTenantConfigNotFound
}
