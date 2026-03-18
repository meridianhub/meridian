package stripe

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockPaymentIntentCreator implements PaymentIntentCreator for testing.
type mockPaymentIntentCreator struct {
	createFn func(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error)
}

func (m *mockPaymentIntentCreator) Create(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
	return m.createFn(ctx, params)
}

func testDispatchRequest() *financialgatewayv1.DispatchPaymentRequest {
	return &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       10000,
		InstrumentCode:    "GBP",
		DebtorAccountId:   "cus_test123",
		CreditorAccountId: "acct_creditor",
		Reference:         "pm_test456",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-idempotency-key"},
	}
}

func tenantContext(id string) context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(id))
}

func TestPaymentIntentAdapter_DispatchPayment_Success(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, int64(10000), *params.Amount)
			assert.Equal(t, "gbp", *params.Currency)
			assert.Equal(t, "cus_test123", *params.Customer)
			assert.Equal(t, "pm_test456", *params.PaymentMethod)
			assert.True(t, *params.Confirm)
			assert.True(t, *params.OffSession)
			assert.Equal(t, "11111111-1111-1111-1111-111111111111", params.Metadata["payment_order_id"])
			assert.Equal(t, "tenant_a", params.Metadata["tenant_id"])

			require.NotNil(t, params.StripeAccount)
			assert.Equal(t, "acct_tenant_a", *params.StripeAccount)

			return &stripego.PaymentIntent{
				ID:     "pi_test_success",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_test_success", result.ProviderReference)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED, result.Status)
	assert.Equal(t, int64(0), result.PlatformFeeAmount)
}

func TestPaymentIntentAdapter_DispatchPayment_WithPlatformFee(t *testing.T) {
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

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{
		PlatformFee: &PlatformFeeConfig{
			Type:  PlatformFeeTypePercentage,
			Value: decimal.NewFromFloat(2.5),
		},
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err)
	assert.Equal(t, "pi_with_fee", result.ProviderReference)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED, result.Status)
	assert.Equal(t, int64(250), result.PlatformFeeAmount)
}

func TestPaymentIntentAdapter_DispatchPayment_StatusMapping(t *testing.T) {
	tests := []struct {
		name           string
		stripeStatus   stripego.PaymentIntentStatus
		expectedStatus financialgatewayv1.DispatchStatus
	}{
		{"succeeded maps to DELIVERED", stripego.PaymentIntentStatusSucceeded, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED},
		{"requires_payment_method maps to FAILED", stripego.PaymentIntentStatusRequiresPaymentMethod, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED},
		{"canceled maps to FAILED", stripego.PaymentIntentStatusCanceled, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED},
		{"processing maps to DISPATCHING", stripego.PaymentIntentStatusProcessing, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING},
		{"requires_action maps to DISPATCHING", stripego.PaymentIntentStatusRequiresAction, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING},
		{"requires_capture maps to DISPATCHING", stripego.PaymentIntentStatusRequiresCapture, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING},
		{"requires_confirmation maps to DISPATCHING", stripego.PaymentIntentStatusRequiresConfirmation, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING},
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

			adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
			require.NoError(t, err)

			ctx := tenantContext("tenant_a")
			ctx = WithStripeAccount(ctx, "acct_tenant_a")
			result, err := adapter.DispatchPayment(ctx, testDispatchRequest())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestPaymentIntentAdapter_DispatchPayment_CardError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeCard,
				Msg:  "Your card was declined.",
				Code: "card_declined",
			}
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err, "card errors should not return an error; they map to FAILED")
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, result.Status)
	assert.Contains(t, result.Message, "Your card was declined.")
}

func TestPaymentIntentAdapter_DispatchPayment_CardErrorWithPaymentIntent(t *testing.T) {
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

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, result.Status)
	assert.Equal(t, "pi_failed_card", result.ProviderReference)
}

func TestPaymentIntentAdapter_DispatchPayment_InvalidRequestError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeInvalidRequest,
				Msg:  "Invalid currency: xyz",
			}
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRequest))
}

func TestPaymentIntentAdapter_DispatchPayment_APIError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				Type: stripego.ErrorTypeAPI,
				Msg:  "internal server error",
			}
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.Error(t, err, "API errors are transient and should be returned for retry")
}

func TestPaymentIntentAdapter_DispatchPayment_NonStripeError(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, errors.New("network timeout")
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.Error(t, err)
}

func TestPaymentIntentAdapter_DispatchPayment_MissingStripeAccount(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return &stripego.PaymentIntent{
				ID:     "pi_should_not_reach",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	// No stripe account set in context
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMissingStripeAccount))
}

func TestPaymentIntentAdapter_DispatchPayment_CurrencyLowercase(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, "gbp", *params.Currency)

			return &stripego.PaymentIntent{
				ID:     "pi_currency",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err)
}

func TestPaymentIntentAdapter_DispatchPayment_IdempotencyKey(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			require.NotNil(t, params.IdempotencyKey)
			assert.NotEmpty(t, *params.IdempotencyKey)

			return &stripego.PaymentIntent{
				ID:     "pi_idempotent",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.NoError(t, err)
}

func TestPaymentIntentAdapter_DispatchPayment_ContextCancelled(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, context.Canceled
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, testDispatchRequest())
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestPaymentIntentAdapter_DispatchPayment_MetadataPassthrough(t *testing.T) {
	mock := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, "bar", params.Metadata["foo"])
			assert.Equal(t, "11111111-1111-1111-1111-111111111111", params.Metadata["payment_order_id"])

			return &stripego.PaymentIntent{
				ID:     "pi_meta",
				Status: stripego.PaymentIntentStatusSucceeded,
			}, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(mock, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	req := testDispatchRequest()
	req.Metadata = map[string]string{"foo": "bar"}

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.DispatchPayment(ctx, req)
	require.NoError(t, err)
}

func TestNewPaymentIntentAdapter_NilCreator(t *testing.T) {
	_, err := NewPaymentIntentAdapter(nil, PaymentIntentAdapterConfig{}, slog.Default())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNilCreator))
}

// --- CancelPayment Tests ---

type mockPaymentIntentCanceller struct {
	cancelFn func(ctx context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error)
}

func (m *mockPaymentIntentCanceller) Cancel(ctx context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
	return m.cancelFn(ctx, id, params)
}

type mockPaymentIntentResolver struct {
	findFn func(ctx context.Context, paymentOrderID string) (string, error)
}

func (m *mockPaymentIntentResolver) FindByPaymentOrderID(ctx context.Context, paymentOrderID string) (string, error) {
	return m.findFn(ctx, paymentOrderID)
}

func TestPaymentIntentAdapter_CancelPayment_Success(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			assert.Equal(t, "pi_to_cancel", id)
			assert.NotNil(t, params.CancellationReason)
			assert.Equal(t, "requested_by_customer", *params.CancellationReason)

			require.NotNil(t, params.StripeAccount)
			assert.Equal(t, "acct_tenant_a", *params.StripeAccount)

			return &stripego.PaymentIntent{
				ID:     "pi_to_cancel",
				Status: stripego.PaymentIntentStatusCanceled,
			}, nil
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, paymentOrderID string) (string, error) {
			assert.Equal(t, "po-cancel-123", paymentOrderID)
			return "pi_to_cancel", nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.CancelPayment(ctx, "po-cancel-123", "customer requested")
	require.NoError(t, err)
	assert.Equal(t, "pi_to_cancel", result.ProviderReference)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, result.Status)
}

func TestPaymentIntentAdapter_CancelPayment_NotConfigured(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.CancelPayment(ctx, "po-123", "reason")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCancelNotConfigured))
}

func TestPaymentIntentAdapter_CancelPayment_MissingStripeAccount(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_x", nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	// No stripe account in context
	_, err = adapter.CancelPayment(ctx, "po-123", "reason")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMissingStripeAccount))
}

func TestPaymentIntentAdapter_CancelPayment_ResolverNotFound(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			t.Fatal("cancel should not be called when resolver fails")
			return nil, nil
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "", ErrPaymentIntentNotFound
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.CancelPayment(ctx, "po-not-found", "reason")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPaymentIntentNotFound))
}

func TestPaymentIntentAdapter_CancelPayment_AlreadyCancelled(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				HTTPStatusCode: 400,
				Type:           stripego.ErrorTypeInvalidRequest,
				Msg:            "You cannot cancel this PaymentIntent because it has a status of canceled.",
			}
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_already_cancelled", nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.CancelPayment(ctx, "po-already-cancelled", "reason")
	require.NoError(t, err, "already-cancelled should succeed idempotently")
	assert.Equal(t, "pi_already_cancelled", result.ProviderReference)
	assert.Equal(t, financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, result.Status)
}

func TestPaymentIntentAdapter_CancelPayment_AlreadySucceeded(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, _ string, _ *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			return nil, &stripego.Error{
				HTTPStatusCode: 400,
				Type:           stripego.ErrorTypeInvalidRequest,
				Msg:            "You cannot cancel this PaymentIntent because it has a status of succeeded.",
			}
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_already_succeeded", nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	_, err = adapter.CancelPayment(ctx, "po-already-succeeded", "reason")
	require.Error(t, err, "already-succeeded should return error, not silent success")
	assert.Contains(t, err.Error(), "cannot be cancelled")
}

func TestPaymentIntentAdapter_CancelPayment_EmptyReason(t *testing.T) {
	creator := &mockPaymentIntentCreator{
		createFn: func(_ context.Context, _ *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error) {
			return nil, nil
		},
	}
	canceller := &mockPaymentIntentCanceller{
		cancelFn: func(_ context.Context, _ string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error) {
			assert.Nil(t, params.CancellationReason, "empty reason should not set CancellationReason")
			return &stripego.PaymentIntent{
				ID:     "pi_no_reason",
				Status: stripego.PaymentIntentStatusCanceled,
			}, nil
		},
	}
	resolver := &mockPaymentIntentResolver{
		findFn: func(_ context.Context, _ string) (string, error) {
			return "pi_no_reason", nil
		},
	}

	adapter, err := NewPaymentIntentAdapter(creator, PaymentIntentAdapterConfig{
		Canceller: canceller,
		Resolver:  resolver,
	}, slog.Default())
	require.NoError(t, err)

	ctx := tenantContext("tenant_a")
	ctx = WithStripeAccount(ctx, "acct_tenant_a")
	result, err := adapter.CancelPayment(ctx, "po-no-reason", "")
	require.NoError(t, err)
	assert.Equal(t, "pi_no_reason", result.ProviderReference)
}
