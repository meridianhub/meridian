package e2e

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/services/financial-gateway/client"
	"github.com/meridianhub/meridian/services/financial-gateway/service"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// --- Test infrastructure ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func tenantContext(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

// stubPaymentIntentCreator implements stripe.PaymentIntentCreator for e2e testing.
type stubPaymentIntentCreator struct {
	mu       sync.Mutex
	calls    []*stripego.PaymentIntentCreateParams
	createFn func(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error)
}

func (s *stubPaymentIntentCreator) Create(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
	s.mu.Lock()
	s.calls = append(s.calls, params)
	s.mu.Unlock()
	return s.createFn(ctx, params)
}

func (s *stubPaymentIntentCreator) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubPaymentIntentCreator) lastCall() *stripego.PaymentIntentCreateParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

// stubTenantConfigProvider implements stripe.TenantConfigProvider for e2e testing.
type stubTenantConfigProvider struct {
	configs map[string]stripeadapter.TenantConfig
	err     error
	calls   atomic.Int32
}

func (s *stubTenantConfigProvider) GetTenantConfig(tenantID string) (stripeadapter.TenantConfig, error) {
	s.calls.Add(1)
	if s.err != nil {
		return stripeadapter.TenantConfig{}, s.err
	}
	cfg, ok := s.configs[tenantID]
	if !ok {
		return stripeadapter.TenantConfig{}, stripeadapter.ErrTenantConfigNotFound
	}
	return cfg, nil
}

// e2eHarness wires up a real FinancialGatewayService with mock Stripe components
// behind an in-process gRPC server.
type e2eHarness struct {
	client         *client.Client
	clientCleanup  func()
	stripeCreator  *stubPaymentIntentCreator
	configProvider *stubTenantConfigProvider
	grpcServer     *grpc.Server
}

func setupHarness(t *testing.T, creator *stubPaymentIntentCreator, configProvider *stubTenantConfigProvider) *e2eHarness {
	t.Helper()

	lg := testLogger()

	// Build real adapter with mock creator
	adapter, err := stripeadapter.NewPaymentIntentAdapter(creator, stripeadapter.PaymentIntentAdapterConfig{}, lg)
	require.NoError(t, err)

	// Build real client factory with mock config provider and minimal config
	cfg := stripeadapter.DefaultConfig()
	cfg.APIKey = "sk_test_fake_key_for_e2e"
	cfg.TenantCacheSize = 10
	cfg.TenantCacheTTL = 1 * time.Second
	cfg.CircuitBreakerFailureThreshold = 3
	cfg.CircuitBreakerTimeout = 500 * time.Millisecond
	cfg.MaxRetries = 0

	factory, err := stripeadapter.NewClientFactory(cfg, configProvider, lg)
	require.NoError(t, err)

	// Build the real gRPC service
	svc, err := service.NewFinancialGatewayService(service.Config{
		StripeAdapter: adapter,
		ClientFactory: factory,
		Logger:        lg,
	})
	require.NoError(t, err)

	// Start in-process gRPC server
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.TenantExtractionInterceptor()),
	)
	financialgatewayv1.RegisterFinancialGatewayServiceServer(grpcServer, svc)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.GracefulStop)

	// Create client connected to in-process server
	fgClient, cleanup, err := client.New(client.Config{
		Target: lis.Addr().String(),
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	return &e2eHarness{
		client:         fgClient,
		clientCleanup:  cleanup,
		stripeCreator:  creator,
		configProvider: configProvider,
		grpcServer:     grpcServer,
	}
}

func defaultConfigProvider() *stubTenantConfigProvider {
	return &stubTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"tenant_e2e": {
				ConnectedAccountID:    "acct_e2e_test",
				WebhookEndpointSecret: "whsec_test",
			},
		},
	}
}

func makeDispatchRequest() *financialgatewayv1.DispatchPaymentRequest {
	return &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    uuid.New().String(),
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       10000,
		InstrumentCode:    "GBP",
		DebtorAccountId:   "cus_e2e_test",
		CreditorAccountId: "acct_creditor_e2e",
		Reference:         "pm_e2e_test",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "idem-e2e-" + uuid.New().String()},
	}
}

// --- E2E Tests ---

func TestDispatchPayment_StripeSuccess(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_e2e_success",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())
	ctx := tenantContext("tenant_e2e")

	req := makeDispatchRequest()
	resp, err := h.client.DispatchPayment(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, req.PaymentOrderId, resp.PaymentOrderId)
	assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, resp.Rail)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED, resp.Status)
	assert.Equal(t, "pi_e2e_success", resp.ProviderReference)
	assert.NotEmpty(t, resp.DispatchId)
	assert.NotNil(t, resp.CreatedAt)

	// Verify the Stripe API was called with correct params
	require.Equal(t, 1, creator.callCount())
	call := creator.lastCall()
	require.NotNil(t, call)
	require.NotNil(t, call.Amount)
	require.NotNil(t, call.Currency)
	require.NotNil(t, call.Customer)
	require.NotNil(t, call.PaymentMethod)
	require.NotNil(t, call.Confirm)
	require.NotNil(t, call.OffSession)
	require.NotNil(t, call.StripeAccount)
	assert.Equal(t, int64(10000), *call.Amount)
	assert.Equal(t, "gbp", *call.Currency)
	assert.Equal(t, "cus_e2e_test", *call.Customer)
	assert.Equal(t, "pm_e2e_test", *call.PaymentMethod)
	assert.True(t, *call.Confirm)
	assert.True(t, *call.OffSession)
	assert.Equal(t, "acct_e2e_test", *call.StripeAccount)
}

func TestDispatchPayment_CardDeclined(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type:        stripego.ErrorTypeCard,
				Msg:         "Your card was declined.",
				Code:        "card_declined",
				DeclineCode: "insufficient_funds",
				PaymentIntent: &stripego.PaymentIntent{
					ID:     "pi_card_declined",
					Status: stripego.PaymentIntentStatusRequiresPaymentMethod,
				},
			}
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())
	ctx := tenantContext("tenant_e2e")

	resp, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
	require.NoError(t, err, "card decline is a business result, not a gRPC error")

	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, resp.Status)
	assert.Equal(t, "pi_card_declined", resp.ProviderReference)
}

func TestDispatchPayment_StripeAPIError(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeAPI,
				Msg:  "internal server error",
			}
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())
	ctx := tenantContext("tenant_e2e")

	_, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestDispatchPayment_InvalidRequest(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeInvalidRequest,
				Msg:  "Invalid currency: xyz",
			}
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())
	ctx := tenantContext("tenant_e2e")

	_, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDispatchPayment_MissingTenantContext(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			t.Error("stripe should not be called without tenant context")
			return nil, errors.New("unexpected stripe call")
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	// No tenant in context
	_, err := h.client.DispatchPayment(context.Background(), makeDispatchRequest())
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDispatchPayment_TenantNotConfigured(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			t.Error("stripe should not be called for unconfigured tenant")
			return nil, errors.New("unexpected stripe call")
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	// Use a tenant ID that has no Stripe config
	ctx := tenantContext("unknown_tenant")
	_, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDispatchPayment_UnsupportedRail(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			t.Error("stripe should not be called for non-Stripe rail")
			return nil, errors.New("unexpected stripe call")
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())
	ctx := tenantContext("tenant_e2e")

	req := makeDispatchRequest()
	req.Rail = financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED

	_, err := h.client.DispatchPayment(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestCircuitBreakerTrips(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			t.Error("stripe should not be called when circuit breaker is open")
			return nil, errors.New("unexpected stripe call")
		},
	}

	// Config provider returns infrastructure errors to trip the circuit breaker.
	// The CB wraps configProvider.GetTenantConfig(), not the Stripe API.
	failingProvider := &stubTenantConfigProvider{
		err: errors.New("config service unavailable"),
	}

	h := setupHarness(t, creator, failingProvider)
	ctx := tenantContext("tenant_e2e")

	// Make 3 calls that fail (threshold is 3) to trip the breaker
	for i := 0; i < 3; i++ {
		_, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
		require.Error(t, err, "call %d should fail", i)
	}

	configCallsBefore := failingProvider.calls.Load()

	// 4th call should fail fast without calling config provider (circuit open)
	_, err := h.client.DispatchPayment(ctx, makeDispatchRequest())
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code(), "circuit breaker open should return Unavailable")

	// Config provider should NOT have been called again
	assert.Equal(t, configCallsBefore, failingProvider.calls.Load(),
		"no additional config provider calls after circuit opens")
}

func TestGetProviderHealth_Healthy(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{ID: "pi_health", Status: stripego.PaymentIntentStatusSucceeded}, nil
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	resp, err := h.client.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)

	assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, resp.Rail)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY, resp.Health)
	assert.NotNil(t, resp.LastCheckedAt)
}

func TestGetProviderHealth_UnsupportedRail(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	_, err := h.client.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED,
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestDispatchRefund_Unimplemented(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	_, err := h.client.DispatchRefund(context.Background(), &financialgatewayv1.DispatchRefundRequest{})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestCancelPayment_Unimplemented(t *testing.T) {
	creator := &stubPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}

	h := setupHarness(t, creator, defaultConfigProvider())

	_, err := h.client.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}
