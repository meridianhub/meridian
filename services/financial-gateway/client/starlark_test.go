package client

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// --- requireInt64Param ---

func TestRequireInt64Param(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		key     string
		want    int64
		wantErr error
	}{
		{
			name:   "int64 value",
			params: map[string]any{"amount": int64(1000)},
			key:    "amount",
			want:   1000,
		},
		{
			name:   "int value coerced to int64",
			params: map[string]any{"amount": int(500)},
			key:    "amount",
			want:   500,
		},
		{
			name:   "float64 exact integer",
			params: map[string]any{"amount": float64(750)},
			key:    "amount",
			want:   750,
		},
		{
			name:    "float64 with fraction rejected",
			params:  map[string]any{"amount": float64(100.5)},
			key:     "amount",
			wantErr: saga.ErrInvalidParamType,
		},
		{
			name:    "string value rejected",
			params:  map[string]any{"amount": "100"},
			key:     "amount",
			wantErr: saga.ErrInvalidParamType,
		},
		{
			name:    "missing key",
			params:  map[string]any{},
			key:     "amount",
			wantErr: saga.ErrMissingParam,
		},
		{
			name:    "nil value is wrong type",
			params:  map[string]any{"amount": nil},
			key:     "amount",
			wantErr: saga.ErrInvalidParamType,
		},
		{
			name:   "zero value is valid",
			params: map[string]any{"amount": int64(0)},
			key:    "amount",
			want:   0,
		},
		{
			name:   "negative value is valid (validation is caller's job)",
			params: map[string]any{"amount": int64(-100)},
			key:    "amount",
			want:   -100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requireInt64Param(tt.params, tt.key)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// --- stringToPaymentRail ---

func TestStringToPaymentRail(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    financialgatewayv1.PaymentRail
		wantErr bool
	}{
		{"STRIPE", "STRIPE", financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, false},
		{"SWIFT", "SWIFT", financialgatewayv1.PaymentRail_PAYMENT_RAIL_SWIFT, false},
		{"ACH", "ACH", financialgatewayv1.PaymentRail_PAYMENT_RAIL_ACH, false},
		{"FEDNOW", "FEDNOW", financialgatewayv1.PaymentRail_PAYMENT_RAIL_FEDNOW, false},
		{"lowercase rejected", "stripe", financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED, true},
		{"empty string rejected", "", financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED, true},
		{"unknown rail rejected", "SEPA", financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stringToPaymentRail(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, saga.ErrInvalidParamType)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- dispatchStatusToString ---

func TestDispatchStatusToString(t *testing.T) {
	tests := []struct {
		status financialgatewayv1.DispatchStatus
		want   string
	}{
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_UNSPECIFIED, "UNSPECIFIED"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING, "PENDING"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING, "DISPATCHING"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED, "DELIVERED"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_ACKNOWLEDGED, "ACKNOWLEDGED"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_RETRYING, "RETRYING"},
		{financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED, "FAILED"},
		{financialgatewayv1.DispatchStatus(999), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, dispatchStatusToString(tt.status))
		})
	}
}

// --- extractStringMetadata ---

func TestExtractStringMetadata(t *testing.T) {
	t.Run("nil params", func(t *testing.T) {
		assert.Nil(t, extractStringMetadata(map[string]any{}))
	})

	t.Run("metadata key absent", func(t *testing.T) {
		assert.Nil(t, extractStringMetadata(map[string]any{"other": "value"}))
	})

	t.Run("metadata is nil", func(t *testing.T) {
		assert.Nil(t, extractStringMetadata(map[string]any{"metadata": nil}))
	})

	t.Run("metadata is wrong type", func(t *testing.T) {
		assert.Nil(t, extractStringMetadata(map[string]any{"metadata": "not a map"}))
	})

	t.Run("valid string metadata", func(t *testing.T) {
		result := extractStringMetadata(map[string]any{
			"metadata": map[string]any{
				"key1": "value1",
				"key2": "value2",
			},
		})
		assert.Equal(t, map[string]string{"key1": "value1", "key2": "value2"}, result)
	})

	t.Run("non-string values silently dropped", func(t *testing.T) {
		result := extractStringMetadata(map[string]any{
			"metadata": map[string]any{
				"keep": "this",
				"drop": 42,
				"also": true,
			},
		})
		assert.Equal(t, map[string]string{"keep": "this"}, result)
	})
}

// --- parseDispatchPaymentParams ---

func TestParseDispatchPaymentParams(t *testing.T) {
	validParams := func() map[string]any {
		return map[string]any{
			"payment_order_id":         "po-123",
			"amount_minor_units":       int64(5000),
			"currency":                 "GBP",
			"customer_reference":       "cus_test",
			"payment_method_reference": "pm_test",
			"idempotency_key":          "idem-key",
			"rail":                     "STRIPE",
		}
	}

	t.Run("happy path", func(t *testing.T) {
		p, err := parseDispatchPaymentParams(validParams())
		require.NoError(t, err)
		assert.Equal(t, "po-123", p.paymentOrderID)
		assert.Equal(t, int64(5000), p.amountMinorUnits)
		assert.Equal(t, "GBP", p.currency)
		assert.Equal(t, "cus_test", p.customerReference)
		assert.Equal(t, "pm_test", p.paymentMethodReference)
		assert.Equal(t, "idem-key", p.idempotencyKey)
		assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, p.rail)
	})

	// Test each required param missing individually
	requiredStringParams := []string{
		"payment_order_id", "currency", "customer_reference",
		"payment_method_reference", "idempotency_key",
	}
	for _, key := range requiredStringParams {
		t.Run("missing "+key, func(t *testing.T) {
			params := validParams()
			delete(params, key)
			_, err := parseDispatchPaymentParams(params)
			require.Error(t, err)
			require.ErrorIs(t, err, saga.ErrMissingParam)
		})
	}

	t.Run("missing amount_minor_units", func(t *testing.T) {
		params := validParams()
		delete(params, "amount_minor_units")
		_, err := parseDispatchPaymentParams(params)
		require.ErrorIs(t, err, saga.ErrMissingParam)
	})

	t.Run("missing rail", func(t *testing.T) {
		params := validParams()
		delete(params, "rail")
		_, err := parseDispatchPaymentParams(params)
		require.ErrorIs(t, err, saga.ErrMissingParam)
	})

	t.Run("invalid rail value", func(t *testing.T) {
		params := validParams()
		params["rail"] = "SEPA"
		_, err := parseDispatchPaymentParams(params)
		require.ErrorIs(t, err, saga.ErrInvalidParamType)
	})

	t.Run("amount as string rejected", func(t *testing.T) {
		params := validParams()
		params["amount_minor_units"] = "5000"
		_, err := parseDispatchPaymentParams(params)
		require.ErrorIs(t, err, saga.ErrInvalidParamType)
	})

	t.Run("amount as fractional float rejected", func(t *testing.T) {
		params := validParams()
		params["amount_minor_units"] = float64(100.5)
		_, err := parseDispatchPaymentParams(params)
		require.ErrorIs(t, err, saga.ErrInvalidParamType)
	})
}

// --- parseDispatchRefundParams ---

func TestParseDispatchRefundParams(t *testing.T) {
	validParams := func() map[string]any {
		return map[string]any{
			"payment_order_id":          "po-refund",
			"refund_amount_minor_units": int64(2500),
			"idempotency_key":           "refund-key",
		}
	}

	t.Run("happy path with reason", func(t *testing.T) {
		params := validParams()
		params["reason"] = "Customer request"
		p, err := parseDispatchRefundParams(params)
		require.NoError(t, err)
		assert.Equal(t, "po-refund", p.paymentOrderID)
		assert.Equal(t, int64(2500), p.refundAmountMinorUnits)
		assert.Equal(t, "refund-key", p.idempotencyKey)
		assert.Equal(t, "Customer request", p.reason)
	})

	t.Run("default reason when not provided", func(t *testing.T) {
		p, err := parseDispatchRefundParams(validParams())
		require.NoError(t, err)
		assert.Equal(t, "Refund requested", p.reason)
	})

	t.Run("empty reason gets default", func(t *testing.T) {
		params := validParams()
		params["reason"] = ""
		p, err := parseDispatchRefundParams(params)
		require.NoError(t, err)
		assert.Equal(t, "Refund requested", p.reason)
	})

	t.Run("missing payment_order_id", func(t *testing.T) {
		params := validParams()
		delete(params, "payment_order_id")
		_, err := parseDispatchRefundParams(params)
		require.ErrorIs(t, err, saga.ErrMissingParam)
	})

	t.Run("missing refund_amount_minor_units", func(t *testing.T) {
		params := validParams()
		delete(params, "refund_amount_minor_units")
		_, err := parseDispatchRefundParams(params)
		require.ErrorIs(t, err, saga.ErrMissingParam)
	})

	t.Run("missing idempotency_key", func(t *testing.T) {
		params := validParams()
		delete(params, "idempotency_key")
		_, err := parseDispatchRefundParams(params)
		require.ErrorIs(t, err, saga.ErrMissingParam)
	})
}

// --- Starlark Handler Tests (via mock gRPC server) ---

func TestDispatchPaymentHandler_Success(t *testing.T) {
	server := &fakeServer{
		dispatchFn: func(_ context.Context, req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return &financialgatewayv1.DispatchPaymentResponse{
				DispatchId:            "dispatch-1",
				PaymentOrderId:        req.GetPaymentOrderId(),
				Status:                financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED,
				ProviderReference:     "pi_test_123",
				PlatformFeeMinorUnits: 250,
			}, nil
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := dispatchPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-idem",
	}

	result, err := handler(ctx, map[string]any{
		"payment_order_id":         "po-123",
		"amount_minor_units":       int64(10000),
		"currency":                 "GBP",
		"customer_reference":       "cus_test",
		"payment_method_reference": "pm_test",
		"idempotency_key":          "idem-key",
		"rail":                     "STRIPE",
	})
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pi_test_123", m["provider_reference_id"])
	assert.Equal(t, "DELIVERED", m["status"])
	assert.Equal(t, int64(250), m["platform_fee_minor_units"])
}

func TestDispatchPaymentHandler_ValidationError(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	handler := dispatchPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	// Missing required params
	_, err := handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestDispatchPaymentHandler_GRPCError(t *testing.T) {
	server := &fakeServer{
		dispatchFn: func(_ context.Context, _ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Unavailable, "stripe down")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := dispatchPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-idem",
	}

	_, err := handler(ctx, map[string]any{
		"payment_order_id":         "po-123",
		"amount_minor_units":       int64(10000),
		"currency":                 "GBP",
		"customer_reference":       "cus_test",
		"payment_method_reference": "pm_test",
		"idempotency_key":          "idem-key",
		"rail":                     "STRIPE",
	})
	require.Error(t, err)
}

func TestCancelPaymentHandler_Success(t *testing.T) {
	server := &fakeServer{
		cancelFn: func(_ context.Context, req *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
			return &financialgatewayv1.CancelPaymentResponse{
				PaymentOrderId: req.GetPaymentOrderId(),
				Status:         "CANCELLED",
				Reason:         req.GetReason(),
			}, nil
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := cancelPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	result, err := handler(ctx, map[string]any{
		"payment_order_id": "po-cancel-1",
		"reason":           "customer request",
	})
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "po-cancel-1", m["payment_order_id"])
	assert.Equal(t, "CANCELLED", m["status"])
	assert.Equal(t, "customer request", m["reason"])
}

func TestCancelPaymentHandler_MissingPaymentOrderID(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	handler := cancelPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	_, err := handler(ctx, map[string]any{})
	require.Error(t, err)
}

func TestCancelPaymentHandler_GRPCError(t *testing.T) {
	server := &fakeServer{
		cancelFn: func(_ context.Context, _ *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := cancelPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	_, err := handler(ctx, map[string]any{
		"payment_order_id": "po-1",
	})
	require.Error(t, err)
}

func TestDispatchRefundHandler_Success(t *testing.T) {
	server := &fakeServer{
		refundFn: func(_ context.Context, _ *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error) {
			return &financialgatewayv1.DispatchRefundResponse{
				DispatchId:        "refund-1",
				Status:            financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING,
				ProviderReference: "re_test_123",
			}, nil
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := dispatchRefundHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	result, err := handler(ctx, map[string]any{
		"payment_order_id":          "po-refund-1",
		"refund_amount_minor_units": int64(5000),
		"idempotency_key":           "refund-idem",
		"reason":                    "item returned",
	})
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refund-1", m["refund_reference_id"])
	assert.Equal(t, "PENDING", m["status"])
	assert.Equal(t, "re_test_123", m["provider_reference"])
}

func TestDispatchRefundHandler_ValidationError(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	handler := dispatchRefundHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	_, err := handler(ctx, map[string]any{})
	require.Error(t, err)
}

func TestDispatchRefundHandler_GRPCError(t *testing.T) {
	server := &fakeServer{
		refundFn: func(_ context.Context, _ *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error) {
			return nil, status.Error(codes.Unimplemented, "not implemented")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := dispatchRefundHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	_, err := handler(ctx, map[string]any{
		"payment_order_id":          "po-1",
		"refund_amount_minor_units": int64(1000),
		"idempotency_key":           "key",
	})
	require.Error(t, err)
}

func TestCancelPaymentHandler_WithCausationID(t *testing.T) {
	server := &fakeServer{
		cancelFn: func(_ context.Context, req *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
			assert.Equal(t, "cause-123", req.GetCausationId())
			return &financialgatewayv1.CancelPaymentResponse{
				PaymentOrderId: req.GetPaymentOrderId(),
				Status:         "CANCELLED",
			}, nil
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	handler := cancelPaymentHandler(c)
	ctx := &saga.StarlarkContext{
		Context:       context.Background(),
		CorrelationID: uuid.New(),
	}

	_, err := handler(ctx, map[string]any{
		"payment_order_id": "po-1",
		"causation_id":     "cause-123",
	})
	require.NoError(t, err)
}

// --- applyRefundOptionalFields ---

func TestApplyRefundOptionalFields(t *testing.T) {
	corrID := uuid.New()
	ctx := &saga.StarlarkContext{
		CorrelationID: corrID,
	}

	t.Run("defaults correlation_id from context", func(t *testing.T) {
		req := &financialgatewayv1.DispatchRefundRequest{}
		params := map[string]any{}

		applyRefundOptionalFields(req, ctx, params)
		assert.Equal(t, corrID.String(), req.CorrelationId)
		assert.Empty(t, req.CausationId)
		assert.Nil(t, req.Metadata)
	})

	t.Run("explicit correlation_id overrides context", func(t *testing.T) {
		req := &financialgatewayv1.DispatchRefundRequest{}
		params := map[string]any{
			"correlation_id": "custom-corr",
		}

		applyRefundOptionalFields(req, ctx, params)
		assert.Equal(t, "custom-corr", req.CorrelationId)
	})

	t.Run("sets causation_id when provided", func(t *testing.T) {
		req := &financialgatewayv1.DispatchRefundRequest{}
		params := map[string]any{
			"causation_id": "cause-123",
		}

		applyRefundOptionalFields(req, ctx, params)
		assert.Equal(t, "cause-123", req.CausationId)
	})

	t.Run("passes through metadata", func(t *testing.T) {
		req := &financialgatewayv1.DispatchRefundRequest{}
		params := map[string]any{
			"metadata": map[string]any{
				"key1": "val1",
			},
		}

		applyRefundOptionalFields(req, ctx, params)
		assert.Equal(t, map[string]string{"key1": "val1"}, req.Metadata)
	})
}

// --- prepareClientContext ---

func TestPrepareClientContext(t *testing.T) {
	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		CorrelationID:  uuid.New(),
		IdempotencyKey: "test-idem-key",
	}

	result := prepareClientContext(ctx)
	assert.NotNil(t, result)
}

// --- RegisterStarlarkHandlers ---

func TestRegisterStarlarkHandlers(t *testing.T) {
	registry := saga.NewHandlerRegistry()

	// Use nil client - registration should succeed (handlers are closures, not called during registration)
	err := RegisterStarlarkHandlers(registry, nil)
	require.NoError(t, err)
}

// --- buildDispatchPaymentRequest ---

func TestBuildDispatchPaymentRequest(t *testing.T) {
	corrID := uuid.New()
	ctx := &saga.StarlarkContext{
		CorrelationID: corrID,
	}

	t.Run("basic request construction", func(t *testing.T) {
		p := dispatchPaymentParams{
			paymentOrderID:         "po-123",
			amountMinorUnits:       5000,
			currency:               "GBP",
			customerReference:      "cus_test",
			paymentMethodReference: "pm_test",
			idempotencyKey:         "idem-key",
			rail:                   financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		}
		params := map[string]any{
			"metadata": map[string]any{
				"debtor_account_id": "acct-debtor",
			},
		}

		req := buildDispatchPaymentRequest(p, ctx, params)
		assert.Equal(t, "po-123", req.PaymentOrderId)
		assert.Equal(t, int64(5000), req.AmountUnits)
		assert.Equal(t, "GBP", req.InstrumentCode)
		assert.Equal(t, "idem-key", req.IdempotencyKey.Key)
		assert.Equal(t, "acct-debtor", req.DebtorAccountId)
		assert.Equal(t, "cus_test", req.Metadata["customer_reference"])
		assert.Equal(t, "pm_test", req.Metadata["payment_method_reference"])
		assert.Equal(t, corrID.String(), req.CorrelationId)
	})

	t.Run("creditor_account_id falls back to debtor", func(t *testing.T) {
		p := dispatchPaymentParams{
			paymentOrderID:   "po-456",
			amountMinorUnits: 1000,
			currency:         "USD",
			idempotencyKey:   "key",
			rail:             financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		}
		params := map[string]any{
			"metadata": map[string]any{
				"debtor_account_id": "acct-debtor",
			},
		}

		req := buildDispatchPaymentRequest(p, ctx, params)
		assert.Equal(t, "acct-debtor", req.CreditorAccountId, "should fall back to debtor when creditor not set")
	})

	t.Run("explicit creditor_account_id used when provided", func(t *testing.T) {
		p := dispatchPaymentParams{
			paymentOrderID:   "po-789",
			amountMinorUnits: 1000,
			currency:         "USD",
			idempotencyKey:   "key",
			rail:             financialgatewayv1.PaymentRail_PAYMENT_RAIL_SWIFT,
		}
		params := map[string]any{
			"metadata": map[string]any{
				"debtor_account_id":   "acct-debtor",
				"creditor_account_id": "acct-creditor",
			},
		}

		req := buildDispatchPaymentRequest(p, ctx, params)
		assert.Equal(t, "acct-creditor", req.CreditorAccountId)
		assert.Equal(t, "acct-debtor", req.DebtorAccountId)
	})

	t.Run("explicit correlation_id from params overrides context", func(t *testing.T) {
		p := dispatchPaymentParams{
			paymentOrderID:   "po-corr",
			amountMinorUnits: 100,
			currency:         "USD",
			idempotencyKey:   "key",
			rail:             financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		}
		params := map[string]any{
			"correlation_id": "explicit-corr-id",
		}

		req := buildDispatchPaymentRequest(p, ctx, params)
		assert.Equal(t, "explicit-corr-id", req.CorrelationId)
	})

	t.Run("no metadata creates empty map", func(t *testing.T) {
		p := dispatchPaymentParams{
			paymentOrderID:         "po-no-meta",
			amountMinorUnits:       100,
			currency:               "USD",
			customerReference:      "cus",
			paymentMethodReference: "pm",
			idempotencyKey:         "key",
			rail:                   financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		}
		params := map[string]any{}

		req := buildDispatchPaymentRequest(p, ctx, params)
		require.NotNil(t, req.Metadata)
		assert.Equal(t, "cus", req.Metadata["customer_reference"])
	})
}
