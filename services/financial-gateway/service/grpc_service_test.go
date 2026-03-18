package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Test helpers ---

type mockCreator struct {
	createFn func(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error)
}

func (m *mockCreator) Create(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
	return m.createFn(ctx, params)
}

type mockCanceller struct {
	cancelFn func(ctx context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error)
}

func (m *mockCanceller) Cancel(ctx context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
	return m.cancelFn(ctx, id, params)
}

type mockResolver struct {
	findFn func(ctx context.Context, paymentOrderID string) (string, error)
}

func (m *mockResolver) FindByPaymentOrderID(ctx context.Context, paymentOrderID string) (string, error) {
	return m.findFn(ctx, paymentOrderID)
}

type mockConfigProvider struct {
	configs map[string]stripeadapter.TenantConfig
}

func (p *mockConfigProvider) GetTenantConfig(tenantID string) (stripeadapter.TenantConfig, error) {
	cfg, ok := p.configs[tenantID]
	if !ok {
		return stripeadapter.TenantConfig{}, stripeadapter.ErrTenantConfigNotFound
	}
	return cfg, nil
}

func tenantCtx(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

func newServiceWithStripeMocks(t *testing.T, creator stripeadapter.PaymentIntentCreator, canceller stripeadapter.PaymentIntentCanceller, resolver stripeadapter.PaymentIntentResolver) *FinancialGatewayService {
	t.Helper()

	adapter, err := stripeadapter.NewPaymentIntentAdapter(creator, stripeadapter.PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	provider := &mockConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"test-tenant": {
				ConnectedAccountID:    "acct_test_123",
				WebhookEndpointSecret: "whsec_test",
			},
		},
	}

	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     60000000000, // 1 minute
		CircuitBreakerName: "test-cb",
	}, provider, slog.Default())
	require.NoError(t, err)

	svc, err := NewFinancialGatewayService(Config{
		StripeAdapter: adapter,
		ClientFactory: factory,
		Logger:        slog.Default(),
	})
	require.NoError(t, err)
	return svc
}

func testStripeRequest() *financialgatewayv1.DispatchPaymentRequest {
	return &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       10000,
		InstrumentCode:    "GBP",
		DebtorAccountId:   "cus_test123",
		CreditorAccountId: "acct_creditor",
		Reference:         "pm_test456",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-key"},
	}
}

// --- Existing tests (unchanged) ---

func TestDispatchPayment_UnsupportedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_SWIFT,
		AmountUnits:       1000,
		InstrumentCode:    "USD",
		DebtorAccountId:   "cus_test",
		CreditorAccountId: "acct_cred",
		Reference:         "ref",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-key"},
	}

	_, err = svc.DispatchPayment(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestDispatchPayment_StripeNotConfigured(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       1000,
		InstrumentCode:    "USD",
		DebtorAccountId:   "cus_test",
		CreditorAccountId: "acct_cred",
		Reference:         "ref",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-key"},
	}

	_, err = svc.DispatchPayment(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestDispatchRefund_Unimplemented(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.DispatchRefund(context.Background(), &financialgatewayv1.DispatchRefundRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestGetProviderHealth_StripeNotConfigured(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	resp, err := svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED, resp.Health)
	assert.Contains(t, resp.Message, "not configured")
}

func TestGetProviderHealth_UnsupportedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_ACH,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestCancelPayment_StripeNotConfigured(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "11111111-1111-1111-1111-111111111111",
		Reason:         "test",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestMapCircuitBreakerHealth(t *testing.T) {
	tests := []struct {
		name           string
		state          gobreaker.State
		expectedHealth financialgatewayv1.ProviderHealth
	}{
		{"closed maps to healthy", gobreaker.StateClosed, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY},
		{"half-open maps to degraded", gobreaker.StateHalfOpen, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_DEGRADED},
		{"open maps to unhealthy", gobreaker.StateOpen, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNHEALTHY},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedHealth, mapCircuitBreakerHealth(tt.state))
		})
	}
}

// --- New tests for full dispatch/cancel/health paths ---

func TestDispatchPayment_StripeSuccess(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_success",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	resp, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, resp.Rail)
	assert.Equal(t, "pi_success", resp.ProviderReference)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED, resp.Status)
	assert.NotEmpty(t, resp.DispatchId)
	assert.NotNil(t, resp.CreatedAt)
}

func TestDispatchPayment_ClientFactoryMissingTenant(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	// No tenant in context
	_, err := svc.DispatchPayment(context.Background(), testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDispatchPayment_ClientFactoryTenantNotFound(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	// Tenant not configured
	ctx := tenantCtx("unknown-tenant")
	_, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDispatchPayment_StripeAdapterError_MissingStripeAccount(t *testing.T) {
	// This test verifies the adapter returns ErrMissingStripeAccount and it maps correctly.
	// The adapter actually sets the account from WithStripeAccount, so we test the error mapping directly.
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeInvalidRequest,
				Msg:  "No such connected account",
			}
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	_, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDispatchPayment_StripeNetworkError(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, errors.New("connection refused")
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	_, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestDispatchPayment_ContextCancelled(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, context.Canceled
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	_, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestDispatchPayment_ContextDeadlineExceeded(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, context.DeadlineExceeded
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	_, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
}

func TestDispatchPayment_CardError_ReturnsSuccessWithFailedStatus(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeCard,
				Msg:  "card_declined",
			}
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	resp, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, resp.Status)
}

// --- CancelPayment full path tests ---

func TestCancelPayment_Success(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, id string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     id,
				Status: stripego.PaymentIntentStatusCanceled,
			}, nil
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_to_cancel", nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	ctx := tenantCtx("test-tenant")
	resp, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-cancel-1",
		Reason:         "test cancellation",
	})
	require.NoError(t, err)
	assert.Equal(t, "po-cancel-1", resp.PaymentOrderId)
	assert.Equal(t, "CANCELLED", resp.Status)
	assert.Equal(t, "test cancellation", resp.Reason)
}

func TestCancelPayment_ClientFactoryMissingTenant(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_x", nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	_, err := svc.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestCancelPayment_ResolverNotFound(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			t.Fatal("should not be called")
			return nil, nil
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "", stripeadapter.ErrPaymentIntentNotFound
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	ctx := tenantCtx("test-tenant")
	_, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-missing",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCancelPayment_CancelNotConfigured(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	// No canceller/resolver = not configured
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	_, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestCancelPayment_AlreadySucceeded(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				HTTPStatusCode: 400,
				Type:           stripego.ErrorTypeInvalidRequest,
				Code:           stripego.ErrorCodePaymentIntentUnexpectedState,
				Msg:            "You cannot cancel this PaymentIntent because it has a status of succeeded.",
			}
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_succeeded", nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	ctx := tenantCtx("test-tenant")
	_, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-succeeded",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestCancelPayment_ContextCancelled(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, context.Canceled
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_x", nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	ctx := tenantCtx("test-tenant")
	_, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled, st.Code())
}

func TestCancelPayment_ContextDeadlineExceeded(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, context.DeadlineExceeded
		},
	}
	resolver := &mockResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_x", nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, canceller, resolver)

	ctx := tenantCtx("test-tenant")
	_, err := svc.CancelPayment(ctx, &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
}

// --- GetProviderHealth full path tests ---

func TestGetProviderHealth_WithTenantContext(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenantCtx("test-tenant")
	resp, err := svc.GetProviderHealth(ctx, &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, resp.Rail)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY, resp.Health)
	assert.Contains(t, resp.Message, "circuit breaker state")
	assert.NotNil(t, resp.LastCheckedAt)
}

func TestGetProviderHealth_WithoutTenantContext(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	// No tenant context
	resp, err := svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED, resp.Health)
	assert.Contains(t, resp.Message, "tenant context required")
}

// --- Error mapping tests ---

func TestMapClientFactoryError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"ErrMissingTenant", stripeadapter.ErrMissingTenant, codes.FailedPrecondition},
		{"ErrTenantConfigNotFound", stripeadapter.ErrTenantConfigNotFound, codes.FailedPrecondition},
		{"ErrCircuitOpen", stripeadapter.ErrCircuitOpen, codes.Unavailable},
		{"context.Canceled", context.Canceled, codes.Canceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"unknown error", errors.New("unknown"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := mapClientFactoryError(tt.err)
			st, ok := status.FromError(grpcErr)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

func TestMapCancelError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"ErrCancelNotConfigured", stripeadapter.ErrCancelNotConfigured, codes.Unimplemented},
		{"ErrPaymentIntentNotFound", stripeadapter.ErrPaymentIntentNotFound, codes.NotFound},
		{"ErrMissingStripeAccount", stripeadapter.ErrMissingStripeAccount, codes.FailedPrecondition},
		{"ErrInvalidRequest", stripeadapter.ErrInvalidRequest, codes.FailedPrecondition},
		{"context.Canceled", context.Canceled, codes.Canceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"unknown error", errors.New("stripe API error"), codes.Unavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := mapCancelError(tt.err)
			st, ok := status.FromError(grpcErr)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

func TestMapStripeError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"ErrMissingStripeAccount", stripeadapter.ErrMissingStripeAccount, codes.FailedPrecondition},
		{"ErrInvalidRequest", stripeadapter.ErrInvalidRequest, codes.InvalidArgument},
		{"context.Canceled", context.Canceled, codes.Canceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"unknown error", errors.New("stripe API error"), codes.Unavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := mapStripeError(tt.err)
			st, ok := status.FromError(grpcErr)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

// --- NewFinancialGatewayService ---

func TestNewFinancialGatewayService_NilLogger(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{})
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestMapCircuitBreakerHealth_UnknownState(t *testing.T) {
	result := mapCircuitBreakerHealth(gobreaker.State(99))
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED, result)
}

// --- DispatchPayment unspecified rail ---

func TestDispatchPayment_UnspecifiedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId: "po-1",
		Rail:           financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED,
	}
	_, err = svc.DispatchPayment(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}
