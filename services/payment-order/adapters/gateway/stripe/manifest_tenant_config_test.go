package stripe

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockManifestClient implements ManifestHistoryServiceClient for testing.
type mockManifestClient struct {
	controlplanev1.ManifestHistoryServiceClient
	currentManifestFn func(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest, opts ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error)
	callCount         atomic.Int32
}

func (m *mockManifestClient) GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest, opts ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
	m.callCount.Add(1)
	return m.currentManifestFn(ctx, req, opts...)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func manifestWithStripeRails(accountID, webhookSecret, feeValue string) *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Test Tenant",
			Industry: "energy",
		},
		PaymentRails: []*controlplanev1.PaymentRails{
			{
				Provider:              "stripe_connect",
				Mode:                  controlplanev1.ConnectMode_CONNECT_MODE_STANDARD,
				AccountId:             accountID,
				WebhookEndpointSecret: webhookSecret,
				PlatformFee: &controlplanev1.PlatformFee{
					Type:  controlplanev1.PlatformFeeType_PLATFORM_FEE_TYPE_PERCENTAGE,
					Value: feeValue,
				},
				PayoutSchedule:   controlplanev1.PayoutSchedule_PAYOUT_SCHEDULE_DAILY,
				SupportedMethods: []string{"card"},
			},
		},
	}
}

func TestManifestTenantConfigProvider_GetTenantConfig(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(ctx context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			// Verify tenant ID is propagated in metadata
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				return nil, status.Error(codes.InvalidArgument, "missing metadata")
			}
			vals := md.Get(tenant.TenantIDKey)
			if len(vals) == 0 || vals[0] == "" {
				return nil, status.Error(codes.InvalidArgument, "missing tenant ID")
			}

			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						"acct_1234567890abcdef",
						"whsec_test_secret",
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	cfg, err := provider.GetTenantConfig("tenant_abc")
	require.NoError(t, err)
	assert.Equal(t, "acct_1234567890abcdef", cfg.ConnectedAccountID)
	assert.Equal(t, "whsec_test_secret", cfg.WebhookEndpointSecret)
}

func TestManifestTenantConfigProvider_CacheHit(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						"acct_1234567890abcdef",
						"whsec_test_secret",
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	// First call - cache miss
	cfg1, err := provider.GetTenantConfig("tenant_abc")
	require.NoError(t, err)
	assert.Equal(t, "acct_1234567890abcdef", cfg1.ConnectedAccountID)
	assert.Equal(t, int32(1), mock.callCount.Load())

	// Second call - cache hit (should NOT make gRPC call)
	cfg2, err := provider.GetTenantConfig("tenant_abc")
	require.NoError(t, err)
	assert.Equal(t, cfg1, cfg2)
	assert.Equal(t, int32(1), mock.callCount.Load(), "second call should use cache")
}

func TestManifestTenantConfigProvider_CacheExpiry(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						"acct_1234567890abcdef",
						"whsec_test_secret",
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Millisecond, // Very short TTL
	})
	require.NoError(t, err)

	// First call
	_, err = provider.GetTenantConfig("tenant_abc")
	require.NoError(t, err)
	assert.Equal(t, int32(1), mock.callCount.Load())

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // triggers cache TTL expiry

	// Second call - cache expired
	_, err = provider.GetTenantConfig("tenant_abc")
	require.NoError(t, err)
	assert.Equal(t, int32(2), mock.callCount.Load(), "should make new gRPC call after cache expiry")
}

func TestManifestTenantConfigProvider_NoPaymentRails(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: &controlplanev1.Manifest{
						Version: "1.0",
						Metadata: &controlplanev1.ManifestMetadata{
							Name: "No Rails Tenant",
						},
						PaymentRails: nil,
					},
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	_, err = provider.GetTenantConfig("tenant_no_rails")
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

func TestManifestTenantConfigProvider_NonStripeProvider(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: &controlplanev1.Manifest{
						Version: "1.0",
						Metadata: &controlplanev1.ManifestMetadata{
							Name: "Other Provider Tenant",
						},
						PaymentRails: []*controlplanev1.PaymentRails{
							{
								Provider:  "other_provider",
								AccountId: "other_123",
								PlatformFee: &controlplanev1.PlatformFee{
									Type:  controlplanev1.PlatformFeeType_PLATFORM_FEE_TYPE_FLAT,
									Value: "1.00",
								},
								SupportedMethods: []string{"card"},
							},
						},
					},
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	_, err = provider.GetTenantConfig("tenant_other")
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

func TestManifestTenantConfigProvider_GRPCError(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return nil, status.Error(codes.Unavailable, "control-plane unavailable")
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	_, err = provider.GetTenantConfig("tenant_abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get manifest")
}

func TestManifestTenantConfigProvider_NilManifest(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: nil,
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	_, err = provider.GetTenantConfig("tenant_abc")
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

func TestManifestTenantConfigProvider_MissingAccountID(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						"", // missing account ID
						"whsec_test_secret",
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	_, err = provider.GetTenantConfig("tenant_abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stripe config")
}

func TestNewManifestTenantConfigProvider_NilClient(t *testing.T) {
	_, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client: nil,
		Logger: testLogger(),
	})
	assert.ErrorIs(t, err, ErrNilManifestClient)
}

func TestNewManifestTenantConfigProvider_NilLogger(t *testing.T) {
	mock := &mockManifestClient{}
	_, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client: mock,
		Logger: nil,
	})
	assert.ErrorIs(t, err, ErrNilLogger)
}

func TestNewManifestTenantConfigProvider_DefaultTTL(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						"acct_1234567890abcdef",
						"whsec_test_secret",
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client: mock,
		Logger: testLogger(),
		// CacheTTL not set - should use default
	})
	require.NoError(t, err)
	assert.Equal(t, defaultCacheTTL, provider.ttl)
}

func TestManifestTenantConfigProvider_DifferentTenants(t *testing.T) {
	mock := &mockManifestClient{
		currentManifestFn: func(ctx context.Context, _ *controlplanev1.GetCurrentManifestRequest, _ ...grpc.CallOption) (*controlplanev1.GetCurrentManifestResponse, error) {
			md, _ := metadata.FromOutgoingContext(ctx)
			tenantIDVals := md.Get(tenant.TenantIDKey)
			tenantID := tenantIDVals[0]

			// Return different configs per tenant
			accountID := "acct_" + tenantID + "1234567890"
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: manifestWithStripeRails(
						accountID,
						"whsec_"+tenantID,
						"2.5",
					),
				},
			}, nil
		},
	}

	provider, err := NewManifestTenantConfigProvider(ManifestTenantConfigProviderConfig{
		Client:   mock,
		Logger:   testLogger(),
		CacheTTL: 1 * time.Minute,
	})
	require.NoError(t, err)

	cfg1, err := provider.GetTenantConfig("tenant_a")
	require.NoError(t, err)

	cfg2, err := provider.GetTenantConfig("tenant_b")
	require.NoError(t, err)

	assert.NotEqual(t, cfg1.ConnectedAccountID, cfg2.ConnectedAccountID)
	assert.Equal(t, int32(2), mock.callCount.Load(), "should make separate gRPC call per tenant")
}
