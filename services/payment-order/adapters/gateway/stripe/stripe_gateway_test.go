package stripe

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockPaymentIntentCreator implements PaymentIntentCreator for testing.
type mockPaymentIntentCreator struct {
	createFn func(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error)
}

func (m *mockPaymentIntentCreator) Create(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
	return m.createFn(ctx, params)
}

func testGatewayRequest() gateway.PaymentRequest {
	return gateway.PaymentRequest{
		PaymentOrderID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DebtorAccountID:   "cus_test123",
		CreditorReference: "pm_test456",
		Amount:            domain.MustNewMoney("GBP", 10000), // GBP 100.00
		IdempotencyKey:    "test-idempotency-key",
	}
}

func tenantContext(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

func TestStripeGatewayAdapter_SendPayment_Success(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			// Verify parameter construction
			assert.Equal(t, int64(10000), *params.Amount)
			assert.Equal(t, "gbp", *params.Currency)
			assert.Equal(t, "cus_test123", *params.Customer)
			assert.Equal(t, "pm_test456", *params.PaymentMethod)
			assert.True(t, *params.Confirm)
			assert.True(t, *params.OffSession)
			assert.Equal(t, "11111111-1111-1111-1111-111111111111", params.Metadata["payment_order_id"])
			assert.Equal(t, "tenant_a", params.Metadata["tenant_id"])

			// Verify connected account is set
			require.NotNil(t, params.StripeAccount)
			assert.Equal(t, "acct_tenant_a", *params.StripeAccount)

			return &stripego.PaymentIntent{
				ID:     "pi_test_success",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_test_success", resp.GatewayReferenceID)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.Equal(t, int64(0), resp.PlatformFeeAmount)
}

func TestStripeGatewayAdapter_SendPayment_WithPlatformFee(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, int64(10000), *params.Amount)
			// 2.5% of 10000 = 250
			require.NotNil(t, params.ApplicationFeeAmount)
			assert.Equal(t, int64(250), *params.ApplicationFeeAmount)

			return &stripego.PaymentIntent{
				ID:     "pi_with_fee",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{
		PlatformFee: &PlatformFeeConfig{
			Type:  PlatformFeeTypePercentage,
			Value: decimal.NewFromFloat(2.5),
		},
	}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_with_fee", resp.GatewayReferenceID)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.Equal(t, int64(250), resp.PlatformFeeAmount)
}

func TestStripeGatewayAdapter_SendPayment_WithFlatFee(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, int64(10000), *params.Amount)
			// Flat fee of 150 cents
			require.NotNil(t, params.ApplicationFeeAmount)
			assert.Equal(t, int64(150), *params.ApplicationFeeAmount)

			return &stripego.PaymentIntent{
				ID:     "pi_flat_fee",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{
		PlatformFee: &PlatformFeeConfig{
			Type:  PlatformFeeTypeFlat,
			Value: decimal.NewFromInt(150),
		},
	}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_flat_fee", resp.GatewayReferenceID)
	assert.Equal(t, int64(150), resp.PlatformFeeAmount)
}

func TestStripeGatewayAdapter_SendPayment_ZeroPlatformFee_OmitsApplicationFee(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Nil(t, params.ApplicationFeeAmount, "should not set application fee when platform fee is zero")

			return &stripego.PaymentIntent{
				ID:     "pi_no_fee",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_no_fee", resp.GatewayReferenceID)
	assert.Equal(t, int64(0), resp.PlatformFeeAmount)
}

func TestStripeGatewayAdapter_SendPayment_StatusMapping(t *testing.T) {
	tests := []struct {
		name           string
		stripeStatus   stripego.PaymentIntentStatus
		expectedStatus gateway.Status
	}{
		{"succeeded maps to ACCEPTED", stripego.PaymentIntentStatusSucceeded, gateway.StatusAccepted},
		{"requires_payment_method maps to REJECTED", stripego.PaymentIntentStatusRequiresPaymentMethod, gateway.StatusRejected},
		{"canceled maps to REJECTED", stripego.PaymentIntentStatusCanceled, gateway.StatusRejected},
		{"processing maps to PENDING", stripego.PaymentIntentStatusProcessing, gateway.StatusPending},
		{"requires_action maps to PENDING", stripego.PaymentIntentStatusRequiresAction, gateway.StatusPending},
		{"requires_capture maps to PENDING", stripego.PaymentIntentStatusRequiresCapture, gateway.StatusPending},
		{"requires_confirmation maps to PENDING", stripego.PaymentIntentStatusRequiresConfirmation, gateway.StatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockPaymentIntentCreator{
				createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
					return &stripego.PaymentIntent{
						ID:     "pi_status_test",
						Status: tt.stripeStatus,
					}, nil
				},
			}

			adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

			ctx := tenantContext("tenant_a")
			ctx = WithStripeAccount(ctx, "acct_tenant_a")
			resp, err := adapter.SendPayment(ctx, testGatewayRequest())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, resp.Status)
		})
	}
}

func TestStripeGatewayAdapter_SendPayment_CardError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeCard,
				Msg:  "Your card was declined.",
				Code: "card_declined",
			}
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err, "card errors should not return an error; they map to REJECTED")
	assert.Equal(t, gateway.StatusRejected, resp.Status)
	assert.Contains(t, resp.Message, "Your card was declined.")
}

func TestStripeGatewayAdapter_SendPayment_RateLimitError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type:           stripego.ErrorTypeAPI,
				Msg:            "rate limit exceeded",
				HTTPStatusCode: 429,
			}
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err, "rate limit errors are transient and should be returned as errors")
}

func TestStripeGatewayAdapter_SendPayment_InvalidRequestError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeInvalidRequest,
				Msg:  "Invalid currency: xyz",
			}
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err, "invalid request errors indicate a programming issue and should be returned as errors")
	assert.True(t, errors.Is(err, ErrInvalidRequest))
}

func TestStripeGatewayAdapter_SendPayment_APIError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeAPI,
				Msg:  "internal server error",
			}
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err, "API errors are transient and should be returned for retry")
}

func TestStripeGatewayAdapter_SendPayment_NonStripeError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, errors.New("network timeout")
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err)
}

func TestStripeGatewayAdapter_SendPayment_MissingStripeAccount(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_should_not_reach",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	// No stripe account set in context
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMissingStripeAccount))
}

func TestStripeGatewayAdapter_SendPayment_CurrencyLowercase(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			// Currency must be lowercase per Stripe API
			assert.Equal(t, "gbp", *params.Currency)
			// Also verify it's truly lowercase even if domain returns uppercase
			assert.Equal(t, strings.ToLower(*params.Currency), *params.Currency)

			return &stripego.PaymentIntent{
				ID:     "pi_currency",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
}

func TestStripeGatewayAdapter_SendPayment_IdempotencyKey(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			// Verify idempotency key is set on params
			require.NotNil(t, params.IdempotencyKey)
			assert.NotEmpty(t, *params.IdempotencyKey)

			return &stripego.PaymentIntent{
				ID:     "pi_idempotent",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
}

func TestStripeGatewayAdapter_SendPayment_CardErrorWithPaymentIntent(t *testing.T) {
	// When Stripe returns a card error with an embedded PaymentIntent,
	// we should use the PaymentIntent ID as the gateway reference.
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeCard,
				Msg:  "insufficient funds",
				Code: "insufficient_funds",
				PaymentIntent: &stripego.PaymentIntent{
					ID:     "pi_failed_card",
					Status: stripego.PaymentIntentStatusRequiresPaymentMethod,
				},
			}
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	resp, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusRejected, resp.Status)
	assert.Equal(t, "pi_failed_card", resp.GatewayReferenceID)
}

func TestStripeGatewayAdapter_SendPayment_ContextCancelled(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, context.Canceled
		},
	}

	adapter := NewGatewayAdapter(mock, GatewayAdapterConfig{}, slog.Default())

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err := adapter.SendPayment(ctx, testGatewayRequest())
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

// Compile-time interface check
var _ gateway.PaymentGateway = (*GatewayAdapter)(nil)
