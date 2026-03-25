package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// newServiceWithFee creates a service with a flat platform fee configured.
func newServiceWithFee(t *testing.T, creator stripeadapter.PaymentIntentCreator, feeMinorUnits int64) *FinancialGatewayService {
	t.Helper()

	fee := &stripeadapter.PlatformFeeConfig{
		Type:  stripeadapter.PlatformFeeTypeFlat,
		Value: decimal.NewFromInt(feeMinorUnits),
	}

	adapter, err := stripeadapter.NewPaymentIntentAdapter(creator, stripeadapter.PaymentIntentAdapterConfig{
		PlatformFee: fee,
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
		TenantCacheTTL:     60000000000,
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

// TestNewFinancialGatewayService_WithPartialConfig verifies that a service can be created
// with adapter set but no ClientFactory (partial config is allowed at construction time).
func TestNewFinancialGatewayService_WithPartialConfig(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}

	adapter, err := stripeadapter.NewPaymentIntentAdapter(creator, stripeadapter.PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	// ClientFactory is nil - service construction should still succeed
	svc, err := NewFinancialGatewayService(Config{
		StripeAdapter: adapter,
		Logger:        slog.Default(),
	})
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

// TestDispatchPayment_ResponseContainsPlatformFee verifies that PlatformFeeMinorUnits is
// populated in the response when the adapter is configured with a flat fee.
func TestDispatchPayment_ResponseContainsPlatformFee(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_with_fee",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}
	// testStripeRequest has AmountUnits=10000; flat fee of 500 minor units
	svc := newServiceWithFee(t, creator, 500)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	resp, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.NoError(t, err)
	assert.Equal(t, int64(500), resp.PlatformFeeMinorUnits)
	assert.Equal(t, "pi_with_fee", resp.ProviderReference)
}

// TestGetProviderHealth_UnspecifiedRail verifies that an unspecified rail returns Unimplemented.
func TestGetProviderHealth_UnspecifiedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

// TestCancelPayment_AdapterNotConfigured_ReturnsUnavailable verifies that CancelPayment
// returns Unavailable when the stripe adapter is not configured.
func TestCancelPayment_AdapterNotConfigured_ReturnsUnavailable(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
		Reason:         "test",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// TestDispatchPayment_StripeSuccess_ResponseFields verifies all response fields for a successful dispatch.
func TestDispatchPayment_StripeSuccess_ResponseFields(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_field_check",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	resp, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.NoError(t, err)

	assert.NotEmpty(t, resp.DispatchId, "dispatch ID must be a non-empty UUID")
	assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, resp.Rail)
	assert.Equal(t, "pi_field_check", resp.ProviderReference)
	assert.NotNil(t, resp.CreatedAt)
}
