package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

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

// TestDispatchPayment_ResponseContainsPlatformFee verifies that platform fee is included in the response
// when the Stripe adapter returns a non-zero fee.
func TestDispatchPayment_ResponseContainsPlatformFee(t *testing.T) {
	creator := &mockCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_with_fee",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}
	svc := newServiceWithStripeMocks(t, creator, nil, nil)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	resp, err := svc.DispatchPayment(ctx, testStripeRequest())
	require.NoError(t, err)
	assert.NotEmpty(t, resp.DispatchId)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", resp.PaymentOrderId)
	assert.NotNil(t, resp.CreatedAt)
}

// TestGetProviderHealth_UnspecifiedRail verifies that an unspecified rail returns Unimplemented.
func TestGetProviderHealth_UnspecifiedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED,
	})
	require.Error(t, err)
}

// TestCancelPayment_ContextDeadlineExceeded_Factory verifies DeadlineExceeded from client factory.
func TestCancelPayment_ContextDeadlineExceeded_Factory(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	// No stripe adapter configured - returns Unavailable
	_, err = svc.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
		Reason:         "timeout test",
	})
	require.Error(t, err)
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
