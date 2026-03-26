package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Test constants and sentinel errors.
const testAccountID = "test-account-123"

var (
	errNotImplemented     = errors.New("not implemented")
	errPaymentRejected    = errors.New("payment rejected: insufficient funds")
	errServiceUnavailable = errors.New("service unavailable")
)

func TestGenerateIdempotencyKey(t *testing.T) {
	key1 := GenerateIdempotencyKey()
	key2 := GenerateIdempotencyKey()

	// Keys should have correct prefix
	assert.True(t, strings.HasPrefix(key1, "HORIZON-TXN-"), "key should start with HORIZON-TXN-")
	assert.True(t, strings.HasPrefix(key2, "HORIZON-TXN-"), "key should start with HORIZON-TXN-")

	// Keys should be unique
	assert.NotEqual(t, key1, key2, "keys should be unique")

	// Key should be valid format (alphanumeric with hyphens)
	assert.Regexp(t, `^HORIZON-TXN-\d+-[a-f0-9]{8}$`, key1)
}

func TestGenerateCorrelationID(t *testing.T) {
	id1 := GenerateCorrelationID()
	id2 := GenerateCorrelationID()

	// IDs should have correct prefix
	assert.True(t, strings.HasPrefix(id1, "horizon-demo-"), "id should start with horizon-demo-")

	// IDs should be unique
	assert.NotEqual(t, id1, id2, "ids should be unique")

	// ID should be valid UUID format after prefix
	assert.Regexp(t, `^horizon-demo-[a-f0-9-]{36}$`, id1)
}

func TestPenceToPoundsPayment(t *testing.T) {
	tests := []struct {
		name          string
		pence         int64
		expectedUnits int64
		expectedNanos int32
	}{
		{
			name:          "100 GBP (10000 pence)",
			pence:         10000,
			expectedUnits: 100,
			expectedNanos: 0,
		},
		{
			name:          "1 penny",
			pence:         1,
			expectedUnits: 0,
			expectedNanos: 10000000,
		},
		{
			name:          "99 pence",
			pence:         99,
			expectedUnits: 0,
			expectedNanos: 990000000,
		},
		{
			name:          "100.99 GBP",
			pence:         10099,
			expectedUnits: 100,
			expectedNanos: 990000000,
		},
		{
			name:          "1000 GBP",
			pence:         100000,
			expectedUnits: 1000,
			expectedNanos: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := penceToPoundsPayment(tt.pence)

			assert.Equal(t, "GBP", result.GetCurrencyCode())
			assert.Equal(t, tt.expectedUnits, result.GetUnits())
			assert.Equal(t, tt.expectedNanos, result.GetNanos())
		})
	}
}

func TestValidateSabotageConfig(t *testing.T) {
	validConfig := func() *SabotageConfig {
		cfg := DefaultSabotageConfig()
		cfg.DebtorAccountID = testAccountID
		return cfg
	}

	t.Run("valid config", func(t *testing.T) {
		err := validateSabotageConfig(validConfig())
		assert.NoError(t, err)
	})

	t.Run("nil config", func(t *testing.T) {
		err := validateSabotageConfig(nil)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "nil")
	})

	t.Run("empty DebtorAccountID", func(t *testing.T) {
		cfg := validConfig()
		cfg.DebtorAccountID = ""
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "DebtorAccountID")
	})

	t.Run("zero AmountPence", func(t *testing.T) {
		cfg := validConfig()
		cfg.AmountPence = 0
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "AmountPence")
	})

	t.Run("negative AmountPence", func(t *testing.T) {
		cfg := validConfig()
		cfg.AmountPence = -100
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
	})

	t.Run("zero InitialTimeout", func(t *testing.T) {
		cfg := validConfig()
		cfg.InitialTimeout = 0
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "InitialTimeout")
	})

	t.Run("zero MinTimeout", func(t *testing.T) {
		cfg := validConfig()
		cfg.MinTimeout = 0
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "MinTimeout")
	})

	t.Run("MinTimeout exceeds InitialTimeout", func(t *testing.T) {
		cfg := validConfig()
		cfg.MinTimeout = 100 * time.Millisecond
		cfg.InitialTimeout = 50 * time.Millisecond
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "exceed")
	})

	t.Run("zero MaxAttempts", func(t *testing.T) {
		cfg := validConfig()
		cfg.MaxAttempts = 0
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "MaxAttempts")
	})

	t.Run("empty CreditorReference", func(t *testing.T) {
		cfg := validConfig()
		cfg.CreditorReference = ""
		err := validateSabotageConfig(cfg)
		assert.ErrorIs(t, err, ErrSabotageConfigInvalid)
		assert.Contains(t, err.Error(), "CreditorReference")
	})
}

func TestDefaultSabotageConfig(t *testing.T) {
	cfg := DefaultSabotageConfig()

	assert.Equal(t, int64(10000), cfg.AmountPence)
	assert.Equal(t, DefaultSabotageTimeout, cfg.InitialTimeout)
	assert.Equal(t, DefaultMinSabotageTimeout, cfg.MinTimeout)
	assert.Equal(t, DefaultMaxSabotageAttempts, cfg.MaxAttempts)
	assert.Equal(t, DefaultCreditorReference, cfg.CreditorReference)
	assert.NotNil(t, cfg.Logger)

	// DebtorAccountID should be empty (caller must set it)
	assert.Empty(t, cfg.DebtorAccountID)
}

func TestBuildPaymentRequest(t *testing.T) {
	cfg := &SabotageConfig{
		DebtorAccountID:   "test-account-456",
		AmountPence:       10000,
		CreditorReference: "GB82WEST12345698765432",
	}
	idempotencyKey := "HORIZON-TXN-123-abc12345"
	correlationID := "horizon-demo-uuid-here"

	req := buildPaymentRequest(cfg, idempotencyKey, correlationID)

	assert.Equal(t, "test-account-456", req.GetDebtorAccountId())
	assert.Equal(t, "GB82WEST12345698765432", req.GetCreditorReference())
	assert.Equal(t, idempotencyKey, req.GetIdempotencyKey().GetKey())
	assert.Equal(t, correlationID, req.GetCorrelationId())

	// Verify amount conversion
	amount := req.GetAmount().GetAmount()
	assert.Equal(t, "GBP", amount.GetCurrencyCode())
	assert.Equal(t, int64(100), amount.GetUnits())
	assert.Equal(t, int32(0), amount.GetNanos())
}

// mockPaymentOrderClient implements paymentorderv1.PaymentOrderServiceClient for testing.
type mockPaymentOrderClient struct {
	paymentorderv1.PaymentOrderServiceClient
	initiateFunc func(ctx context.Context, req *paymentorderv1.InitiatePaymentOrderRequest, opts ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error)
}

func (m *mockPaymentOrderClient) InitiatePaymentOrder(ctx context.Context, req *paymentorderv1.InitiatePaymentOrderRequest, opts ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
	if m.initiateFunc != nil {
		return m.initiateFunc(ctx, req, opts...)
	}
	return nil, errNotImplemented
}

func TestRunSabotage_ClientTimeout(t *testing.T) {
	// Mock client that takes longer than the timeout
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(ctx context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			// Wait until context times out
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID
	cfg.InitialTimeout = 20 * time.Millisecond // Short timeout to speed up test
	cfg.MinTimeout = 5 * time.Millisecond

	result, err := RunSabotage(context.Background(), clients, cfg)

	require.NoError(t, err)
	assert.True(t, result.Success, "sabotage should succeed when client times out")
	assert.NotEmpty(t, result.IdempotencyKey)
	assert.NotEmpty(t, result.CorrelationID)
	assert.Equal(t, cfg.InitialTimeout, result.FinalTimeout)
	require.Len(t, result.Attempts, 1)
	assert.True(t, result.Attempts[0].TimedOut)
}

func TestRunSabotage_AdaptiveCalibration(t *testing.T) {
	callCount := 0
	// First call succeeds quickly, second call times out
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(ctx context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: respond quickly (within timeout)
				return &paymentorderv1.InitiatePaymentOrderResponse{
					PaymentOrder: &paymentorderv1.PaymentOrder{
						PaymentOrderId: "po-first-attempt",
						Status:         paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED,
						CreatedAt:      timestamppb.Now(),
						UpdatedAt:      timestamppb.Now(),
					},
				}, nil
			}
			// Subsequent calls: wait until timeout
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID
	cfg.InitialTimeout = 100 * time.Millisecond
	cfg.MinTimeout = 10 * time.Millisecond

	result, err := RunSabotage(context.Background(), clients, cfg)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, 2, len(result.Attempts), "should have 2 attempts (one success, one timeout)")

	// First attempt should have succeeded without timeout
	assert.False(t, result.Attempts[0].TimedOut)
	assert.Equal(t, "po-first-attempt", result.Attempts[0].PaymentOrderID)

	// Second attempt should have timed out
	assert.True(t, result.Attempts[1].TimedOut)

	// Final timeout should be half of initial
	assert.Equal(t, 50*time.Millisecond, result.FinalTimeout)
}

func TestRunSabotage_CalibrationFailure(t *testing.T) {
	// Mock client that always responds quickly
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			return &paymentorderv1.InitiatePaymentOrderResponse{
				PaymentOrder: &paymentorderv1.PaymentOrder{
					PaymentOrderId: "po-fast-response",
					Status:         paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED,
					CreatedAt:      timestamppb.Now(),
					UpdatedAt:      timestamppb.Now(),
				},
			}, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID
	cfg.InitialTimeout = 100 * time.Millisecond
	cfg.MinTimeout = 50 * time.Millisecond // High min timeout, will reach it after one halving
	cfg.MaxAttempts = 5

	result, err := RunSabotage(context.Background(), clients, cfg)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSabotageCalibrationFail)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "minimum timeout")
}

func TestRunSabotage_MaxAttemptsReached(t *testing.T) {
	callCount := 0
	// Mock client that always responds just before timeout
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			callCount++
			return &paymentorderv1.InitiatePaymentOrderResponse{
				PaymentOrder: &paymentorderv1.PaymentOrder{
					PaymentOrderId: "po-fast-response",
					Status:         paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED,
					CreatedAt:      timestamppb.Now(),
					UpdatedAt:      timestamppb.Now(),
				},
			}, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID
	cfg.InitialTimeout = 1 * time.Second  // Long timeout
	cfg.MinTimeout = 1 * time.Millisecond // Very short min
	cfg.MaxAttempts = 2                   // But only 2 attempts allowed

	result, err := RunSabotage(context.Background(), clients, cfg)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSabotageCalibrationFail)
	assert.Contains(t, err.Error(), "max attempts")
	assert.Equal(t, 2, len(result.Attempts))
}

func TestRunSabotage_PaymentError(t *testing.T) {
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			return nil, errPaymentRejected
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID

	result, err := RunSabotage(context.Background(), clients, cfg)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSabotagePaymentFailed)
	assert.False(t, result.Success)
	require.Len(t, result.Attempts, 1)
	assert.NotNil(t, result.Attempts[0].Error)
}

func TestRunSabotage_NilClients(t *testing.T) {
	cfg := DefaultSabotageConfig()
	cfg.DebtorAccountID = testAccountID

	// This will panic if we don't handle nil, but the validation should catch config issues
	// The nil client check happens at runtime during the call
	// For this test, we're mainly checking config validation works
	err := validateSabotageConfig(cfg)
	assert.NoError(t, err)
}

func TestSabotageAttempt_ToAttemptReport_Timeout(t *testing.T) {
	attempt := SabotageAttempt{
		AttemptNumber: 1,
		Timeout:       30 * time.Millisecond,
		Duration:      30 * time.Millisecond,
		TimedOut:      true,
		Error:         context.DeadlineExceeded,
	}

	report := attempt.ToAttemptReport("HORIZON-TXN-123")

	assert.Equal(t, 1, report.Attempt)
	assert.Equal(t, "HORIZON-TXN-123", report.IdempotencyKey)
	assert.Equal(t, AttemptStatusClientTimeout, report.Status)
	assert.Equal(t, int64(30), report.DurationMs)
	assert.Contains(t, report.Error, "deadline exceeded")
	assert.Empty(t, report.PaymentOrderID)
}

func TestSabotageAttempt_ToAttemptReport_Success(t *testing.T) {
	attempt := SabotageAttempt{
		AttemptNumber:  2,
		Timeout:        30 * time.Millisecond,
		Duration:       15 * time.Millisecond,
		TimedOut:       false,
		PaymentOrderID: "po-xyz-123",
	}

	report := attempt.ToAttemptReport("HORIZON-TXN-456")

	assert.Equal(t, 2, report.Attempt)
	assert.Equal(t, "HORIZON-TXN-456", report.IdempotencyKey)
	assert.Equal(t, AttemptStatusSuccess, report.Status)
	assert.Equal(t, int64(15), report.DurationMs)
	assert.Empty(t, report.Error)
	assert.Equal(t, "po-xyz-123", report.PaymentOrderID)
}

func TestSabotageAttempt_ToAttemptReport_Error(t *testing.T) {
	attempt := SabotageAttempt{
		AttemptNumber: 1,
		Timeout:       30 * time.Millisecond,
		Duration:      5 * time.Millisecond,
		TimedOut:      false,
		Error:         errServiceUnavailable,
	}

	report := attempt.ToAttemptReport("HORIZON-TXN-789")

	assert.Equal(t, AttemptStatusError, report.Status)
	assert.Equal(t, "service unavailable", report.Error)
}

func TestExecuteSabotageAttempt_ContextCancellation(t *testing.T) {
	mockClient := &mockPaymentOrderClient{
		initiateFunc: func(ctx context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &SabotageConfig{
		DebtorAccountID:   "test-account",
		AmountPence:       10000,
		CreditorReference: DefaultCreditorReference,
	}

	attempt := executeSabotageAttempt(
		context.Background(),
		clients,
		cfg,
		"test-key",
		"test-correlation",
		1,
		10*time.Millisecond,
	)

	assert.True(t, attempt.TimedOut)
	assert.NotNil(t, attempt.Error)
	// Allow tolerance for timer precision variability on CI runners.
	// With a 10ms timeout, measured duration can be as low as 1-2ms
	// due to coarse timer resolution and scheduling on virtualized CI.
	assert.GreaterOrEqual(t, attempt.Duration.Milliseconds(), int64(1))
}
