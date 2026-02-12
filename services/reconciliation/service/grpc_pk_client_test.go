package service

import (
	"context"
	"errors"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
)

// stubPKServiceClient is a minimal test double for positionkeepingv1.PositionKeepingServiceClient.
// Only ListFinancialPositionLogs is used by GrpcPositionKeepingClient.
type stubPKServiceClient struct {
	positionkeepingv1.PositionKeepingServiceClient
	responses []*positionkeepingv1.ListFinancialPositionLogsResponse
	callIdx   int
	err       error
}

func (s *stubPKServiceClient) ListFinancialPositionLogs(
	_ context.Context,
	_ *positionkeepingv1.ListFinancialPositionLogsRequest,
	_ ...grpc.CallOption,
) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.callIdx >= len(s.responses) {
		return &positionkeepingv1.ListFinancialPositionLogsResponse{}, nil
	}
	resp := s.responses[s.callIdx]
	s.callIdx++
	return resp, nil
}

func makeEntry(currencyCode string, units int64, direction commonv1.PostingDirection) *positionkeepingv1.TransactionLogEntry {
	return &positionkeepingv1.TransactionLogEntry{
		Direction: direction,
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{CurrencyCode: currencyCode, Units: units, Nanos: 0},
		},
	}
}

func TestGrpcPositionKeepingClient_GetPositionSummary_SinglePage(t *testing.T) {
	stub := &stubPKServiceClient{
		responses: []*positionkeepingv1.ListFinancialPositionLogsResponse{
			{
				Logs: []*positionkeepingv1.FinancialPositionLog{
					{
						LogId:     "log-1",
						AccountId: "acct-1",
						TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
							makeEntry("GBP", 100, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT),
							makeEntry("GBP", 50, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT),
						},
					},
				},
			},
		},
	}

	client := NewGrpcPositionKeepingClient(stub)
	summary, err := client.GetPositionSummary(context.Background(), "acct-1", "GBP")

	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(summary.TotalDebits), "expected 100 debits, got %s", summary.TotalDebits)
	assert.True(t, decimal.NewFromInt(50).Equal(summary.TotalCredits), "expected 50 credits, got %s", summary.TotalCredits)
	assert.Equal(t, "GBP", summary.InstrumentCode)
}

func TestGrpcPositionKeepingClient_GetPositionSummary_FiltersInstrumentCode(t *testing.T) {
	stub := &stubPKServiceClient{
		responses: []*positionkeepingv1.ListFinancialPositionLogsResponse{
			{
				Logs: []*positionkeepingv1.FinancialPositionLog{
					{
						TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
							makeEntry("GBP", 100, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT),
							makeEntry("EUR", 200, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT),
						},
					},
				},
			},
		},
	}

	client := NewGrpcPositionKeepingClient(stub)
	summary, err := client.GetPositionSummary(context.Background(), "acct-1", "GBP")

	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(summary.TotalDebits), "should only include GBP debits")
	assert.True(t, decimal.Zero.Equal(summary.TotalCredits))
}

func TestGrpcPositionKeepingClient_GetPositionSummary_MultiPage(t *testing.T) {
	stub := &stubPKServiceClient{
		responses: []*positionkeepingv1.ListFinancialPositionLogsResponse{
			{
				Logs: []*positionkeepingv1.FinancialPositionLog{
					{
						TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
							makeEntry("GBP", 50, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT),
						},
					},
				},
				Pagination: &commonv1.PaginationResponse{NextPageToken: "page2"},
			},
			{
				Logs: []*positionkeepingv1.FinancialPositionLog{
					{
						TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
							makeEntry("GBP", 30, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT),
						},
					},
				},
			},
		},
	}

	client := NewGrpcPositionKeepingClient(stub)
	summary, err := client.GetPositionSummary(context.Background(), "acct-1", "GBP")

	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(80).Equal(summary.TotalDebits), "expected 50+30=80 debits across pages")
}

func TestGrpcPositionKeepingClient_GetPositionSummary_RPCError(t *testing.T) {
	stub := &stubPKServiceClient{
		err: errors.New("connection refused"),
	}

	client := NewGrpcPositionKeepingClient(stub)
	_, err := client.GetPositionSummary(context.Background(), "acct-1", "GBP")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing position logs")
}

func TestGrpcPositionKeepingClient_GetPositionSummary_EmptyResponse(t *testing.T) {
	stub := &stubPKServiceClient{
		responses: []*positionkeepingv1.ListFinancialPositionLogsResponse{
			{Logs: nil},
		},
	}

	client := NewGrpcPositionKeepingClient(stub)
	summary, err := client.GetPositionSummary(context.Background(), "acct-1", "GBP")

	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(summary.TotalDebits))
	assert.True(t, decimal.Zero.Equal(summary.TotalCredits))
}
