package stripe

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// mockManifestClient implements ManifestHistoryServiceClient for testing.
type mockManifestClient struct {
	controlplanev1.ManifestHistoryServiceClient
	getCurrentManifestFn func(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest, opts ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error)
	callCount            int
}

func (m *mockManifestClient) GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest, opts ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
	m.callCount++
	return m.getCurrentManifestFn(ctx, req, opts...)
}

func manifestWithStripeConnect(accountID, webhookSecret string) *controlplanev1.GetCurrentManifestResponse {
	return &controlplanev1.GetCurrentManifestResponse{
		Version: &controlplanev1.ManifestVersion{
			Manifest: &controlplanev1.Manifest{
				PaymentRails: []*controlplanev1.PaymentRails{
					{
						Provider:              "stripe_connect",
						AccountId:             accountID,
						WebhookEndpointSecret: webhookSecret,
					},
				},
			},
		},
	}
}

func TestNewManifestTenantConfigProvider(t *testing.T) {
	t.Run("nil client", func(t *testing.T) {
		_, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Logger: slog.Default(),
		})
		require.ErrorIs(t, err, ErrNilManifestClient)
	})

	t.Run("nil logger", func(t *testing.T) {
		_, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: &mockManifestClient{},
		})
		require.ErrorIs(t, err, ErrNilLogger)
	})

	t.Run("defaults cache TTL to 5 minutes", func(t *testing.T) {
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: &mockManifestClient{},
			Logger: slog.Default(),
		})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, p.ttl)
	})

	t.Run("custom cache TTL", func(t *testing.T) {
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client:   &mockManifestClient{},
			Logger:   slog.Default(),
			CacheTTL: 30 * time.Second,
		})
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, p.ttl)
	})
}

func TestManifestTenantConfigProvider_GetTenantConfig(t *testing.T) {
	t.Run("happy path — returns stripe config", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return manifestWithStripeConnect("acct_tenant1", "whsec_secret1"), nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		cfg, err := p.GetTenantConfig("tenant-1")
		require.NoError(t, err)
		assert.Equal(t, "acct_tenant1", cfg.ConnectedAccountID)
		assert.Equal(t, "whsec_secret1", cfg.WebhookEndpointSecret)
	})

	t.Run("cache hit — second call uses cache", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return manifestWithStripeConnect("acct_cached", "whsec_cached"), nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client:   mock,
			Logger:   slog.Default(),
			CacheTTL: 1 * time.Hour, // won't expire during test
		})
		require.NoError(t, err)

		cfg1, err := p.GetTenantConfig("tenant-cache")
		require.NoError(t, err)
		assert.Equal(t, "acct_cached", cfg1.ConnectedAccountID)
		assert.Equal(t, 1, mock.callCount)

		cfg2, err := p.GetTenantConfig("tenant-cache")
		require.NoError(t, err)
		assert.Equal(t, "acct_cached", cfg2.ConnectedAccountID)
		assert.Equal(t, 1, mock.callCount, "should not call gRPC again — cache hit")
	})

	t.Run("cache expiry — refetches after TTL", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return manifestWithStripeConnect("acct_fresh", "whsec_fresh"), nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client:   mock,
			Logger:   slog.Default(),
			CacheTTL: 1 * time.Millisecond, // expires immediately
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-expire")
		require.NoError(t, err)
		assert.Equal(t, 1, mock.callCount)

		time.Sleep(5 * time.Millisecond) // let cache expire

		_, err = p.GetTenantConfig("tenant-expire")
		require.NoError(t, err)
		assert.Equal(t, 2, mock.callCount, "should refetch after cache TTL")
	})

	t.Run("nil manifest — returns ErrTenantConfigNotFound", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return &controlplanev1.GetCurrentManifestResponse{
					Version: &controlplanev1.ManifestVersion{
						Manifest: nil,
					},
				}, nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-nil-manifest")
		require.ErrorIs(t, err, ErrTenantConfigNotFound)
	})

	t.Run("no stripe_connect rail — returns ErrTenantConfigNotFound", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return &controlplanev1.GetCurrentManifestResponse{
					Version: &controlplanev1.ManifestVersion{
						Manifest: &controlplanev1.Manifest{
							PaymentRails: []*controlplanev1.PaymentRails{
								{Provider: "manual_bank_transfer", AccountId: "acct_bank"},
							},
						},
					},
				}, nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-no-stripe")
		require.ErrorIs(t, err, ErrTenantConfigNotFound)
	})

	t.Run("empty payment_rails — returns ErrTenantConfigNotFound", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return &controlplanev1.GetCurrentManifestResponse{
					Version: &controlplanev1.ManifestVersion{
						Manifest: &controlplanev1.Manifest{
							PaymentRails: []*controlplanev1.PaymentRails{},
						},
					},
				}, nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-empty-rails")
		require.ErrorIs(t, err, ErrTenantConfigNotFound)
	})

	t.Run("invalid config — empty connected account ID", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return manifestWithStripeConnect("", "whsec_secret"), nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-invalid")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingAccountID)
	})

	t.Run("gRPC error propagates", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return nil, fmt.Errorf("connection refused")
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-grpc-fail")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get manifest")
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("stripe_connect not first rail — still found", func(t *testing.T) {
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				return &controlplanev1.GetCurrentManifestResponse{
					Version: &controlplanev1.ManifestVersion{
						Manifest: &controlplanev1.Manifest{
							PaymentRails: []*controlplanev1.PaymentRails{
								{Provider: "manual_bank_transfer", AccountId: "acct_bank"},
								{Provider: "stripe_connect", AccountId: "acct_stripe", WebhookEndpointSecret: "whsec_stripe"},
							},
						},
					},
				}, nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		cfg, err := p.GetTenantConfig("tenant-multi-rail")
		require.NoError(t, err)
		assert.Equal(t, "acct_stripe", cfg.ConnectedAccountID)
	})

	t.Run("error does not cache — subsequent call retries", func(t *testing.T) {
		callNum := 0
		mock := &mockManifestClient{
			getCurrentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
				callNum++
				if callNum == 1 {
					return nil, fmt.Errorf("transient failure")
				}
				return manifestWithStripeConnect("acct_retry", "whsec_retry"), nil
			},
		}
		p, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
			Client: mock,
			Logger: slog.Default(),
		})
		require.NoError(t, err)

		_, err = p.GetTenantConfig("tenant-retry")
		require.Error(t, err)

		// Second call should retry (error not cached)
		cfg, err := p.GetTenantConfig("tenant-retry")
		require.NoError(t, err)
		assert.Equal(t, "acct_retry", cfg.ConnectedAccountID)
		assert.Equal(t, 2, mock.callCount)
	})
}
