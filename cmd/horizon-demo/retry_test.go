package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"google.golang.org/grpc"
)

// Test constants for retry tests.
const (
	retryTestAccountID      = "retry-test-account-123"
	retryTestIdempotencyKey = "HORIZON-TXN-1234567890-abc12345"
	retryTestCorrelationID  = "horizon-demo-test-correlation-id"
	retryTestCreditorRef    = "GB82WEST12345698765432"
	retryTestPaymentOrderID = "po-retry-test-12345"
)

// Static errors for retry tests.
var (
	errRetryServiceUnavailable = errors.New("service unavailable")
)

// mockRetryPaymentOrderClient implements PaymentOrderServiceClient for retry tests.
type mockRetryPaymentOrderClient struct {
	paymentorderv1.PaymentOrderServiceClient
	initiateFunc func(ctx context.Context, req *paymentorderv1.InitiatePaymentOrderRequest, opts ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error)
}

func (m *mockRetryPaymentOrderClient) InitiatePaymentOrder(ctx context.Context, req *paymentorderv1.InitiatePaymentOrderRequest, opts ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
	if m.initiateFunc != nil {
		return m.initiateFunc(ctx, req, opts...)
	}
	return nil, errNotImplemented
}

func TestValidateRetryConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RetryConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
			errMsg:  "config is nil",
		},
		{
			name: "missing idempotency key",
			cfg: &RetryConfig{
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "IdempotencyKey is required",
		},
		{
			name: "missing correlation ID",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "CorrelationID is required",
		},
		{
			name: "missing debtor account ID",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "DebtorAccountID is required",
		},
		{
			name: "zero amount",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       0,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "AmountPence must be positive",
		},
		{
			name: "negative amount",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       -100,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "AmountPence must be positive",
		},
		{
			name: "missing creditor reference",
			cfg: &RetryConfig{
				IdempotencyKey:  retryTestIdempotencyKey,
				CorrelationID:   retryTestCorrelationID,
				DebtorAccountID: retryTestAccountID,
				AmountPence:     10000,
				WaitBeforeRetry: DefaultRetryWait,
				Timeout:         DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "CreditorReference is required",
		},
		{
			name: "negative wait before retry",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   -1 * time.Second,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: true,
			errMsg:  "WaitBeforeRetry cannot be negative",
		},
		{
			name: "zero timeout",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           0,
			},
			wantErr: true,
			errMsg:  "Timeout must be positive",
		},
		{
			name: "valid config",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   DefaultRetryWait,
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: false,
		},
		{
			name: "valid config with zero wait (immediate retry)",
			cfg: &RetryConfig{
				IdempotencyKey:    retryTestIdempotencyKey,
				CorrelationID:     retryTestCorrelationID,
				DebtorAccountID:   retryTestAccountID,
				AmountPence:       10000,
				CreditorReference: retryTestCreditorRef,
				WaitBeforeRetry:   0, // Zero is valid (no wait)
				Timeout:           DefaultRetryTimeout,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRetryConfig(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
					return
				}
				if !errors.Is(err, ErrRetryConfigInvalid) {
					t.Errorf("expected ErrRetryConfigInvalid, got %v", err)
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunRetry_Success(t *testing.T) {
	mockClient := &mockRetryPaymentOrderClient{
		initiateFunc: func(_ context.Context, req *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			// Verify the request contains the expected idempotency key
			if req.GetIdempotencyKey().GetKey() != retryTestIdempotencyKey {
				t.Errorf("expected idempotency key %q, got %q", retryTestIdempotencyKey, req.GetIdempotencyKey().GetKey())
			}

			// Return existing payment order (idempotency hit)
			return &paymentorderv1.InitiatePaymentOrderResponse{
				PaymentOrder: &paymentorderv1.PaymentOrder{
					PaymentOrderId: retryTestPaymentOrderID,
					Status:         paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING,
				},
			}, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
		WaitBeforeRetry:   0, // No wait for tests
		Timeout:           5 * time.Second,
		Logger:            slog.Default(),
	}

	ctx := context.Background()
	result, err := RunRetry(ctx, clients, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Error("expected Success to be true")
	}

	if result.PaymentOrderID != retryTestPaymentOrderID {
		t.Errorf("expected PaymentOrderID %q, got %q", retryTestPaymentOrderID, result.PaymentOrderID)
	}

	if !result.IdempotencyHit {
		t.Error("expected IdempotencyHit to be true")
	}

	if result.IdempotencyKey != retryTestIdempotencyKey {
		t.Errorf("expected IdempotencyKey %q, got %q", retryTestIdempotencyKey, result.IdempotencyKey)
	}

	if result.Error != nil {
		t.Errorf("expected no error, got %v", result.Error)
	}
}

func TestRunRetry_ServiceError(t *testing.T) {
	mockClient := &mockRetryPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			return nil, errRetryServiceUnavailable
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
		WaitBeforeRetry:   0,
		Timeout:           5 * time.Second,
		Logger:            slog.Default(),
	}

	ctx := context.Background()
	result, err := RunRetry(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRetryFailed) {
		t.Errorf("expected ErrRetryFailed, got %v", err)
	}

	if result.Success {
		t.Error("expected Success to be false")
	}

	if result.Error == nil {
		t.Error("expected result.Error to be set")
	}
}

func TestRunRetry_NilPaymentOrder(t *testing.T) {
	mockClient := &mockRetryPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			// Return response with nil payment order
			return &paymentorderv1.InitiatePaymentOrderResponse{
				PaymentOrder: nil,
			}, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
		WaitBeforeRetry:   0,
		Timeout:           5 * time.Second,
		Logger:            slog.Default(),
	}

	ctx := context.Background()
	result, err := RunRetry(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRetryNoPaymentOrder) {
		t.Errorf("expected ErrRetryNoPaymentOrder, got %v", err)
	}

	if result.Success {
		t.Error("expected Success to be false")
	}
}

func TestRunRetry_EmptyPaymentOrderID(t *testing.T) {
	mockClient := &mockRetryPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			// Return payment order with empty ID
			return &paymentorderv1.InitiatePaymentOrderResponse{
				PaymentOrder: &paymentorderv1.PaymentOrder{
					PaymentOrderId: "",
					Status:         paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING,
				},
			}, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
		WaitBeforeRetry:   0,
		Timeout:           5 * time.Second,
		Logger:            slog.Default(),
	}

	ctx := context.Background()
	result, err := RunRetry(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRetryPaymentOrderIDNil) {
		t.Errorf("expected ErrRetryPaymentOrderIDNil, got %v", err)
	}

	if result.Success {
		t.Error("expected Success to be false")
	}
}

func TestRunRetry_ContextCancelledDuringWait(t *testing.T) {
	mockClient := &mockRetryPaymentOrderClient{
		initiateFunc: func(_ context.Context, _ *paymentorderv1.InitiatePaymentOrderRequest, _ ...grpc.CallOption) (*paymentorderv1.InitiatePaymentOrderResponse, error) {
			t.Fatal("should not reach payment call")
			return nil, nil
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
	}

	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
		WaitBeforeRetry:   10 * time.Second, // Long wait
		Timeout:           5 * time.Second,
		Logger:            slog.Default(),
	}

	// Create context that cancels quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := RunRetry(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !containsString(err.Error(), "context cancelled") {
		t.Errorf("expected error about context cancelled, got %v", err)
	}

	if result.Success {
		t.Error("expected Success to be false")
	}
}

func TestRunRetry_InvalidConfig(t *testing.T) {
	clients := &Clients{}

	// Nil config
	result, err := RunRetry(context.Background(), clients, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if result != nil {
		t.Error("expected nil result for invalid config")
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.WaitBeforeRetry != DefaultRetryWait {
		t.Errorf("expected WaitBeforeRetry %v, got %v", DefaultRetryWait, cfg.WaitBeforeRetry)
	}

	if cfg.Timeout != DefaultRetryTimeout {
		t.Errorf("expected Timeout %v, got %v", DefaultRetryTimeout, cfg.Timeout)
	}

	if cfg.Logger == nil {
		t.Error("expected Logger to be set")
	}
}

func TestNewRetryConfigFromSabotage(t *testing.T) {
	sabCfg := &SabotageConfig{
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       15000, // GBP 150.00
		CreditorReference: retryTestCreditorRef,
		Logger:            slog.Default(),
	}

	sabResult := &SabotageResult{
		IdempotencyKey: retryTestIdempotencyKey,
		CorrelationID:  retryTestCorrelationID,
	}

	retryCfg := NewRetryConfigFromSabotage(sabCfg, sabResult)

	if retryCfg.IdempotencyKey != retryTestIdempotencyKey {
		t.Errorf("expected IdempotencyKey %q, got %q", retryTestIdempotencyKey, retryCfg.IdempotencyKey)
	}

	if retryCfg.CorrelationID != retryTestCorrelationID {
		t.Errorf("expected CorrelationID %q, got %q", retryTestCorrelationID, retryCfg.CorrelationID)
	}

	if retryCfg.DebtorAccountID != retryTestAccountID {
		t.Errorf("expected DebtorAccountID %q, got %q", retryTestAccountID, retryCfg.DebtorAccountID)
	}

	if retryCfg.AmountPence != 15000 {
		t.Errorf("expected AmountPence 15000, got %d", retryCfg.AmountPence)
	}

	if retryCfg.CreditorReference != retryTestCreditorRef {
		t.Errorf("expected CreditorReference %q, got %q", retryTestCreditorRef, retryCfg.CreditorReference)
	}

	if retryCfg.WaitBeforeRetry != DefaultRetryWait {
		t.Errorf("expected WaitBeforeRetry %v, got %v", DefaultRetryWait, retryCfg.WaitBeforeRetry)
	}

	if retryCfg.Timeout != DefaultRetryTimeout {
		t.Errorf("expected Timeout %v, got %v", DefaultRetryTimeout, retryCfg.Timeout)
	}
}

func TestRetryResult_ToAttemptReport(t *testing.T) {
	tests := []struct {
		name           string
		result         *RetryResult
		attemptNum     int
		expectedStatus string
		expectedError  string
	}{
		{
			name: "successful retry",
			result: &RetryResult{
				IdempotencyKey: retryTestIdempotencyKey,
				PaymentOrderID: retryTestPaymentOrderID,
				Duration:       150 * time.Millisecond,
				Success:        true,
			},
			attemptNum:     2,
			expectedStatus: AttemptStatusSuccess,
			expectedError:  "",
		},
		{
			name: "failed retry",
			result: &RetryResult{
				IdempotencyKey: retryTestIdempotencyKey,
				Duration:       100 * time.Millisecond,
				Success:        false,
				Error:          errRetryServiceUnavailable,
			},
			attemptNum:     2,
			expectedStatus: AttemptStatusError,
			expectedError:  "service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := tt.result.ToAttemptReport(tt.attemptNum)

			if report.Attempt != tt.attemptNum {
				t.Errorf("expected Attempt %d, got %d", tt.attemptNum, report.Attempt)
			}

			if report.IdempotencyKey != tt.result.IdempotencyKey {
				t.Errorf("expected IdempotencyKey %q, got %q", tt.result.IdempotencyKey, report.IdempotencyKey)
			}

			if report.Status != tt.expectedStatus {
				t.Errorf("expected Status %q, got %q", tt.expectedStatus, report.Status)
			}

			if tt.expectedError != "" && report.Error != tt.expectedError {
				t.Errorf("expected Error %q, got %q", tt.expectedError, report.Error)
			}

			if report.PaymentOrderID != tt.result.PaymentOrderID {
				t.Errorf("expected PaymentOrderID %q, got %q", tt.result.PaymentOrderID, report.PaymentOrderID)
			}

			expectedDuration := tt.result.Duration.Milliseconds()
			if report.DurationMs != expectedDuration {
				t.Errorf("expected DurationMs %d, got %d", expectedDuration, report.DurationMs)
			}
		})
	}
}

func TestPenceToPoundsRetry(t *testing.T) {
	tests := []struct {
		name          string
		pence         int64
		expectedUnits int64
		expectedNanos int32
	}{
		{
			name:          "whole pounds only",
			pence:         10000, // GBP 100.00
			expectedUnits: 100,
			expectedNanos: 0,
		},
		{
			name:          "pounds with pence",
			pence:         15099, // GBP 150.99
			expectedUnits: 150,
			expectedNanos: 990000000,
		},
		{
			name:          "pence only",
			pence:         50, // GBP 0.50
			expectedUnits: 0,
			expectedNanos: 500000000,
		},
		{
			name:          "single penny",
			pence:         1, // GBP 0.01
			expectedUnits: 0,
			expectedNanos: 10000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money := penceToPoundsRetry(tt.pence)

			if money.CurrencyCode != "GBP" {
				t.Errorf("expected CurrencyCode GBP, got %q", money.CurrencyCode)
			}

			if money.Units != tt.expectedUnits {
				t.Errorf("expected Units %d, got %d", tt.expectedUnits, money.Units)
			}

			if money.Nanos != tt.expectedNanos {
				t.Errorf("expected Nanos %d, got %d", tt.expectedNanos, money.Nanos)
			}
		})
	}
}

func TestBuildRetryRequest(t *testing.T) {
	cfg := &RetryConfig{
		IdempotencyKey:    retryTestIdempotencyKey,
		CorrelationID:     retryTestCorrelationID,
		DebtorAccountID:   retryTestAccountID,
		AmountPence:       10000,
		CreditorReference: retryTestCreditorRef,
	}

	req := buildRetryRequest(cfg)

	if req.DebtorAccountId != retryTestAccountID {
		t.Errorf("expected DebtorAccountId %q, got %q", retryTestAccountID, req.DebtorAccountId)
	}

	if req.CreditorReference != retryTestCreditorRef {
		t.Errorf("expected CreditorReference %q, got %q", retryTestCreditorRef, req.CreditorReference)
	}

	if req.GetIdempotencyKey().GetKey() != retryTestIdempotencyKey {
		t.Errorf("expected IdempotencyKey %q, got %q", retryTestIdempotencyKey, req.GetIdempotencyKey().GetKey())
	}

	if req.CorrelationId != retryTestCorrelationID {
		t.Errorf("expected CorrelationId %q, got %q", retryTestCorrelationID, req.CorrelationId)
	}

	// Verify amount conversion
	if req.GetAmount().GetAmount().GetUnits() != 100 {
		t.Errorf("expected Amount Units 100, got %d", req.GetAmount().GetAmount().GetUnits())
	}

	if req.GetAmount().GetAmount().GetCurrencyCode() != "GBP" {
		t.Errorf("expected Amount CurrencyCode GBP, got %q", req.GetAmount().GetAmount().GetCurrencyCode())
	}
}

// containsString checks if s contains substr (helper for tests).
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
