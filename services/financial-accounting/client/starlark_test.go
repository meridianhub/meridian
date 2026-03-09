package client

import (
	"context"
	"testing"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockFinancialAccountingClient implements financialaccountingv1.FinancialAccountingServiceClient for testing
type mockFinancialAccountingClient struct {
	InitiateFinancialBookingLogFunc func(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	UpdateFinancialBookingLogFunc   func(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	CaptureLedgerPostingFunc        func(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	UpdateLedgerPostingFunc         func(ctx context.Context, req *financialaccountingv1.UpdateLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateLedgerPostingResponse, error)
}

func (m *mockFinancialAccountingClient) InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	if m.InitiateFinancialBookingLogFunc != nil {
		return m.InitiateFinancialBookingLogFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	if m.UpdateFinancialBookingLogFunc != nil {
		return m.UpdateFinancialBookingLogFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	if m.CaptureLedgerPostingFunc != nil {
		return m.CaptureLedgerPostingFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) UpdateLedgerPosting(ctx context.Context, req *financialaccountingv1.UpdateLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	if m.UpdateLedgerPostingFunc != nil {
		return m.UpdateLedgerPostingFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) RetrieveFinancialBookingLog(_ context.Context, _ *financialaccountingv1.RetrieveFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) ListFinancialBookingLogs(_ context.Context, _ *financialaccountingv1.ListFinancialBookingLogsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) ControlFinancialBookingLog(_ context.Context, _ *financialaccountingv1.ControlFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.ControlFinancialBookingLogResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) RetrieveLedgerPosting(_ context.Context, _ *financialaccountingv1.RetrieveLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFinancialAccountingClient) ListLedgerPostings(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// TestRegisterStarlarkHandlers_AllHandlersRegistered verifies that all 7 handlers are registered with correct metadata.
func TestRegisterStarlarkHandlers_AllHandlersRegistered(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	mockClient := &Client{
		financialAccounting: &mockFinancialAccountingClient{},
	}

	err := RegisterStarlarkHandlers(registry, mockClient)
	require.NoError(t, err)

	// Verify all 7 handlers are registered
	handlers := []string{
		"financial_accounting.initiate_booking_log",
		"financial_accounting.update_booking_log",
		"financial_accounting.capture_posting",
		"financial_accounting.compensate_posting",
		"financial_accounting.create_booking",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
	}

	for _, name := range handlers {
		handler, metadata, err := registry.GetWithMetadata(name)
		require.NoError(t, err, "handler %s should be registered", name)
		require.NotNil(t, handler, "handler %s should not be nil", name)
		require.NotNil(t, metadata, "metadata for %s should not be nil", name)

		// Verify CategorySettlement metadata
		assert.Equal(t, saga.HandlerCategorySettlement, metadata.Category, "handler %s should have CategorySettlement", name)

		// Verify ProducesInstruments for handlers that produce money
		expectedInstruments := []string{"USD", "EUR", "GBP", "NZD"}
		if name != "financial_accounting.update_booking_log" && name != "financial_accounting.compensate_posting" && name != "financial_accounting.reverse_entries" {
			// Most handlers produce money instruments
			assert.Equal(t, expectedInstruments, metadata.ProducesInstruments, "handler %s should produce money instruments", name)
		}
	}
}

// TestInitiateBookingLogHandler_Success tests the happy path for initiate_booking_log.
func TestInitiateBookingLogHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		InitiateFinancialBookingLogFunc: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return &financialaccountingv1.InitiateFinancialBookingLogResponse{
				FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
					Id: "booking-log-123",
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := initiateBookingLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"product_service_reference": "product-123",
		"business_unit_reference":   "bu-456",
		"chart_of_accounts_rules":   "standard",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "booking-log-123", resultMap["log_id"])
	assert.Equal(t, "INITIATED", resultMap["status"])
}

// TestInitiateBookingLogHandler_MissingParam tests error handling for missing required parameters.
func TestInitiateBookingLogHandler_MissingParam(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := initiateBookingLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		// Missing product_service_reference
		"business_unit_reference": "bu-456",
	}

	result, err := handler(ctx, params)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "product_service_reference")
}

// TestCapturePostingHandler_Success tests the happy path for capture_posting.
func TestCapturePostingHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		CaptureLedgerPostingFunc: func(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return &financialaccountingv1.CaptureLedgerPostingResponse{
				LedgerPosting: &financialaccountingv1.LedgerPosting{
					Id: "posting-123",
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := capturePostingHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"booking_log_id": "log-123",
		"account_id":     "account-456",
		"amount":         "100.00",
		"currency":       "USD",
		"direction":      "DEBIT",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "posting-123", resultMap["posting_id"])
	assert.Equal(t, "POSTED", resultMap["status"])
}

// TestCompensatePostingHandler_Success tests compensation handler.
func TestCompensatePostingHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		UpdateLedgerPostingFunc: func(_ context.Context, req *financialaccountingv1.UpdateLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
			return &financialaccountingv1.UpdateLedgerPostingResponse{
				LedgerPosting: &financialaccountingv1.LedgerPosting{
					Id: req.Id,
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := compensatePostingHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"posting_id": "posting-123",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "posting-123", resultMap["posting_id"])
	assert.Equal(t, "COMPENSATED", resultMap["status"])
}

// TestPostEntriesHandler_Success tests posting multiple GL entries.
func TestPostEntriesHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		CaptureLedgerPostingFunc: func(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return &financialaccountingv1.CaptureLedgerPostingResponse{
				LedgerPosting: &financialaccountingv1.LedgerPosting{
					Id: "posting-batch-123",
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash-account",
				"amount":     "100.00",
				"currency":   "USD",
				"direction":  "DEBIT",
			},
			map[string]any{
				"account_id": "revenue-account",
				"amount":     "100.00",
				"currency":   "USD",
				"direction":  "CREDIT",
			},
		},
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "POSTED", resultMap["status"])
	assert.NotEmpty(t, resultMap["posting_ids"])
}

// TestPostEntriesHandler_UnbalancedEntries tests validation of balanced journal entries.
func TestPostEntriesHandler_UnbalancedEntries(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash-account",
				"amount":     "100.00",
				"currency":   "USD",
				"direction":  "DEBIT",
			},
			map[string]any{
				"account_id": "revenue-account",
				"amount":     "50.00", // Unbalanced - should be 100.00
				"currency":   "USD",
				"direction":  "CREDIT",
			},
		},
	}

	result, err := handler(ctx, params)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unbalanced")
}

// TestPostEntriesHandler_InvalidEntriesType tests error handling for invalid entries parameter.
func TestPostEntriesHandler_InvalidEntriesType(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"booking_log_id": "log-123",
		"entries":        "not-an-array", // Should be array
	}

	result, err := handler(ctx, params)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "must be array")
}

// TestReverseEntriesHandler_Success tests compensation handler for reversing entries.
func TestReverseEntriesHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		UpdateFinancialBookingLogFunc: func(_ context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
			return &financialaccountingv1.UpdateFinancialBookingLogResponse{
				FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
					Id: req.Id,
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := reverseEntriesHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"booking_log_id": "log-123",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "log-123", resultMap["log_id"])
	assert.Equal(t, "REVERSED", resultMap["status"])
}

// TestUpdateBookingLogHandler_Success tests updating a booking log.
func TestUpdateBookingLogHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		UpdateFinancialBookingLogFunc: func(_ context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
			return &financialaccountingv1.UpdateFinancialBookingLogResponse{
				FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
					Id: req.Id,
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := updateBookingLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"log_id": "log-123",
		"status": "POSTED",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "log-123", resultMap["log_id"])
	assert.Equal(t, "UPDATED", resultMap["status"])
}

// TestCreateBookingHandler_Success tests creating a booking log (alias for initiate).
func TestCreateBookingHandler_Success(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		InitiateFinancialBookingLogFunc: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return &financialaccountingv1.InitiateFinancialBookingLogResponse{
				FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
					Id: "booking-log-456",
				},
			}, nil
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := createBookingHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"product_service_reference": "product-789",
		"business_unit_reference":   "bu-101",
		"chart_of_accounts_rules":   "standard",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "booking-log-456", resultMap["booking_id"])
	assert.Equal(t, "CREATED", resultMap["status"])
}

// TestHandlerErrorPropagation verifies that gRPC errors are properly propagated.
func TestHandlerErrorPropagation(t *testing.T) {
	mockClient := &mockFinancialAccountingClient{
		InitiateFinancialBookingLogFunc: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return nil, status.Error(codes.Internal, "database connection failed")
		},
	}

	client := &Client{financialAccounting: mockClient}
	handler := initiateBookingLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"product_service_reference": "product-123",
		"business_unit_reference":   "bu-456",
		"chart_of_accounts_rules":   "standard",
	}

	result, err := handler(ctx, params)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "financial_accounting.initiate_booking_log")
	assert.Contains(t, err.Error(), "database connection failed")
}
