package client

import (
	"context"
	"testing"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// --- validateBalancedEntries error paths ---

func TestValidateBalancedEntries_InvalidAmountString(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash",
				"amount":     "not-a-number",
				"currency":   "USD",
				"direction":  "DEBIT",
			},
		},
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid entry amount")
}

func TestValidateBalancedEntries_AmountNotString(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash",
				"amount":     123.45, // not a string
				"currency":   "USD",
				"direction":  "DEBIT",
			},
		},
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEntryAmountMustBeString)
}

func TestValidateBalancedEntries_DirectionNotString(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash",
				"amount":     "100.00",
				"currency":   "USD",
				"direction":  42, // not a string
			},
		},
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEntryDirectionMustBeString)
}

func TestValidateBalancedEntries_InvalidDirection(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			map[string]any{
				"account_id": "cash",
				"amount":     "100.00",
				"currency":   "USD",
				"direction":  "SIDEWAYS", // invalid direction
			},
		},
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDirection)
}

func TestValidateBalancedEntries_EntryNotObject(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"entries": []any{
			"not-a-map", // entry is a string, not a map
		},
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEntryMustBeObject)
}

// --- parseEntriesArray error paths ---

func TestPostEntriesHandler_MissingEntriesParam(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := postEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		// "entries" key is missing entirely
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingEntriesParam)
}

// --- updateBookingLogHandler additional status paths ---

func TestUpdateBookingLogHandler_CancelledStatus(t *testing.T) {
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

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"log_id": "log-456",
		"status": "CANCELLED",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "log-456", resultMap["log_id"])
}

func TestUpdateBookingLogHandler_InvalidStatus(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := updateBookingLogHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"log_id": "log-789",
		"status": "INVALID_STATUS",
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatus)
}

func TestUpdateBookingLogHandler_PendingStatus(t *testing.T) {
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

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"log_id": "log-101",
		"status": "PENDING",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "log-101", resultMap["log_id"])
}

// --- reverseEntriesHandler missing param ---

func TestReverseEntriesHandler_MissingParam(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := reverseEntriesHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		// "booking_log_id" is missing
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
}

// --- compensatePostingHandler missing param ---

func TestCompensatePostingHandler_MissingPostingID(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := compensatePostingHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		// "posting_id" is missing
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
}

// --- capturePostingHandler error paths ---

func TestCapturePostingHandler_InvalidAmount(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := capturePostingHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"account_id":     "cash-account",
		"amount":         "not-a-number",
		"currency":       "USD",
		"direction":      "DEBIT",
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid amount")
}

func TestCapturePostingHandler_InvalidDirection(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := capturePostingHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		"account_id":     "cash-account",
		"amount":         "100.00",
		"currency":       "USD",
		"direction":      "SIDEWAYS",
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDirection)
}

func TestCapturePostingHandler_MissingParam(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := capturePostingHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		"booking_log_id": "log-123",
		// account_id, amount, currency, direction all missing
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
}

// --- createBookingHandler error path ---

func TestCreateBookingHandler_InitiateError(t *testing.T) {
	client := &Client{financialAccounting: &mockFinancialAccountingClient{}}
	handler := createBookingHandler(client)

	ctx := &saga.StarlarkContext{Context: context.Background()}
	params := map[string]any{
		// missing required params causes param validation to fail before gRPC call
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
}
