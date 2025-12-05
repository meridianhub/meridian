package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Test errors for order audit tests.
var errListServiceUnavailable = errors.New("list service unavailable")

// mockPaymentOrderListClient implements PaymentOrderServiceClient for order audit testing.
type mockPaymentOrderListClient struct {
	paymentorderv1.PaymentOrderServiceClient
	listResponse *paymentorderv1.ListPaymentOrdersResponse
	listError    error
}

func (m *mockPaymentOrderListClient) ListPaymentOrders(
	_ context.Context,
	_ *paymentorderv1.ListPaymentOrdersRequest,
	_ ...grpc.CallOption,
) (*paymentorderv1.ListPaymentOrdersResponse, error) {
	if m.listError != nil {
		return nil, m.listError
	}
	return m.listResponse, nil
}

func TestValidateOrderAuditConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *OrderAuditConfig
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
			name: "empty AccountID",
			cfg: &OrderAuditConfig{
				IdempotencyKey: "test-key",
			},
			wantErr: true,
			errMsg:  "AccountID is required",
		},
		{
			name: "empty IdempotencyKey",
			cfg: &OrderAuditConfig{
				AccountID: "test-account",
			},
			wantErr: true,
			errMsg:  "IdempotencyKey is required",
		},
		{
			name: "valid config",
			cfg: &OrderAuditConfig{
				AccountID:      "test-account",
				IdempotencyKey: "test-key",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOrderAuditConfig(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.True(t, errors.Is(err, ErrOrderAuditConfigInvalid))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRunOrderAudit_UniqueOrderFound(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:     "order-123",
					DebtorAccountId:    "test-account",
					IdempotencyKey:     "test-key-123",
					Status:             paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					LienId:             "lien-456",
					GatewayReferenceId: "gateway-789",
					CreatedAt:          timestamppb.Now(),
					UpdatedAt:          timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "test-key-123",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "test-account", result.AccountID)
	assert.Equal(t, "test-key-123", result.IdempotencyKey)
	assert.Equal(t, 1, result.OrdersFound)
	assert.False(t, result.DuplicateOrdersFound)
	assert.Equal(t, OrderStatusUniqueFound, result.OrderStatus)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	assert.Nil(t, result.Error)

	// Verify order summary
	require.Len(t, result.MatchingOrders, 1)
	assert.Equal(t, "order-123", result.MatchingOrders[0].PaymentOrderID)
	assert.Equal(t, "PAYMENT_ORDER_STATUS_COMPLETED", result.MatchingOrders[0].Status)
	assert.Equal(t, "lien-456", result.MatchingOrders[0].LienID)
	assert.Equal(t, "gateway-789", result.MatchingOrders[0].GatewayReferenceID)
}

func TestRunOrderAudit_DuplicatesFound(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-123",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "duplicate-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
				{
					PaymentOrderId:  "order-456",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "duplicate-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "duplicate-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.OrdersFound)
	assert.True(t, result.DuplicateOrdersFound)
	assert.Equal(t, OrderStatusDuplicatesFound, result.OrderStatus)
	assert.Equal(t, AuditVerdictFail, result.Verdict)
	assert.True(t, errors.Is(result.Error, ErrOrderAuditDuplicates))
	assert.Len(t, result.MatchingOrders, 2)
}

func TestRunOrderAudit_NoneFound(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-999",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "other-key", // Different key
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "missing-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.OrdersFound)
	assert.False(t, result.DuplicateOrdersFound)
	assert.Equal(t, OrderStatusNoneFound, result.OrderStatus)
	assert.Equal(t, AuditVerdictFail, result.Verdict)
	assert.True(t, errors.Is(result.Error, ErrOrderAuditNoneFound))
	assert.Empty(t, result.MatchingOrders)
}

func TestRunOrderAudit_EmptyResponse(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "any-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.OrdersFound)
	assert.Equal(t, OrderStatusNoneFound, result.OrderStatus)
	assert.Equal(t, AuditVerdictFail, result.Verdict)
}

func TestRunOrderAudit_ListError(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listError: errListServiceUnavailable,
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "test-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictError, result.Verdict)
	assert.True(t, errors.Is(result.Error, ErrOrderAuditListFailed))
}

func TestRunOrderAudit_FiltersByIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	// Response contains multiple orders but only one matches the idempotency key
	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-1",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "key-A",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
				{
					PaymentOrderId:  "order-2",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "target-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
				{
					PaymentOrderId:  "order-3",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "key-B",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "target-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.OrdersFound)
	assert.Equal(t, OrderStatusUniqueFound, result.OrderStatus)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	require.Len(t, result.MatchingOrders, 1)
	assert.Equal(t, "order-2", result.MatchingOrders[0].PaymentOrderID)
}

func TestRunOrderAudit_ExecutingStatus(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-123",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "test-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "test-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	assert.Equal(t, "PAYMENT_ORDER_STATUS_EXECUTING", result.MatchingOrders[0].Status)
}

func TestRunOrderAudit_NilLogger(t *testing.T) {
	ctx := context.Background()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-123",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "test-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       timestamppb.Now(),
					UpdatedAt:       timestamppb.Now(),
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       slog.Default(),
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "test-key",
		Logger:         nil, // nil logger should default to slog.Default()
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
}

func TestOrderStatus_String(t *testing.T) {
	tests := []struct {
		status   OrderStatus
		expected string
	}{
		{OrderStatusUniqueFound, "UNIQUE_FOUND"},
		{OrderStatusDuplicatesFound, "DUPLICATES_FOUND"},
		{OrderStatusNoneFound, "NONE_FOUND"},
		{OrderStatus(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestNewOrderAuditConfig(t *testing.T) {
	logger := slog.Default()

	cfg := NewOrderAuditConfig("account-123", "key-456", logger)

	require.NotNil(t, cfg)
	assert.Equal(t, "account-123", cfg.AccountID)
	assert.Equal(t, "key-456", cfg.IdempotencyKey)
	assert.Equal(t, logger, cfg.Logger)
}

func TestNewOrderAuditConfig_NilLogger(t *testing.T) {
	cfg := NewOrderAuditConfig("account-123", "key-456", nil)

	require.NotNil(t, cfg)
	assert.Equal(t, "account-123", cfg.AccountID)
	assert.Equal(t, "key-456", cfg.IdempotencyKey)
	assert.NotNil(t, cfg.Logger) // Should default to slog.Default()
}

func TestRunOrderAudit_NilConfigReturnsError(t *testing.T) {
	ctx := context.Background()
	clients := &Clients{
		logger: slog.Default(),
	}

	result, err := RunOrderAudit(ctx, clients, nil)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, errors.Is(err, ErrOrderAuditConfigInvalid))
}

func TestRunOrderAudit_OrderWithNilTimestamps(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockPaymentOrderListClient{
		listResponse: &paymentorderv1.ListPaymentOrdersResponse{
			PaymentOrders: []*paymentorderv1.PaymentOrder{
				{
					PaymentOrderId:  "order-123",
					DebtorAccountId: "test-account",
					IdempotencyKey:  "test-key",
					Status:          paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					CreatedAt:       nil, // nil timestamp
					UpdatedAt:       nil, // nil timestamp
				},
			},
		},
	}

	clients := &Clients{
		PaymentOrder: mockClient,
		logger:       logger,
	}

	cfg := &OrderAuditConfig{
		AccountID:      "test-account",
		IdempotencyKey: "test-key",
		Logger:         logger,
	}

	result, err := RunOrderAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	require.Len(t, result.MatchingOrders, 1)
	assert.Empty(t, result.MatchingOrders[0].CreatedAt)
	assert.Empty(t, result.MatchingOrders[0].UpdatedAt)
}
