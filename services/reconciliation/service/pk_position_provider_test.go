package service

import (
	"context"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubPKClient implements positionkeepingv1.PositionKeepingServiceClient for testing.
type stubPKClient struct {
	positionkeepingv1.PositionKeepingServiceClient
	resp *positionkeepingv1.ListFinancialPositionLogsResponse
	err  error
}

func (s *stubPKClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest, _ ...grpc.CallOption) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return s.resp, s.err
}

func TestNewPKPositionProvider(t *testing.T) {
	client := &stubPKClient{}
	provider := NewPKPositionProvider(client)
	assert.NotNil(t, provider)
}

func TestFetchPositions_Success(t *testing.T) {
	client := &stubPKClient{
		resp: &positionkeepingv1.ListFinancialPositionLogsResponse{
			Logs: []*positionkeepingv1.FinancialPositionLog{
				{
					LogId:     "log-1",
					AccountId: "acc-001",
					TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
						{
							Amount:    &commonv1.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 100, Nanos: 500000000}},
							Direction: commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
						},
						{
							Amount:    &commonv1.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 30, Nanos: 0}},
							Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
						},
					},
				},
			},
			Pagination: &commonv1.PaginationResponse{NextPageToken: "next-page"},
		},
	}

	provider := NewPKPositionProvider(client)
	page, err := provider.FetchPositions(context.Background(), "acc-001", 10, "")
	require.NoError(t, err)
	require.Len(t, page.Records, 1)

	rec := page.Records[0]
	assert.Equal(t, "acc-001", rec.AccountID)
	assert.Equal(t, "GBP", rec.InstrumentCode)
	// 100.5 credit - 30 debit = 70.5
	assert.True(t, decimal.NewFromFloat(70.5).Equal(rec.Balance))
	assert.Equal(t, "position-keeping", rec.SourceSystem)
	assert.Equal(t, "log-1", rec.Attributes["log_id"])
	assert.Equal(t, "next-page", page.NextPageToken)
}

func TestFetchPositions_GRPCError(t *testing.T) {
	client := &stubPKClient{err: status.Error(codes.Unavailable, "service down")}

	provider := NewPKPositionProvider(client)
	_, err := provider.FetchPositions(context.Background(), "acc-001", 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list financial position logs from PK")
}

func TestFetchPositions_EmptyLogs(t *testing.T) {
	client := &stubPKClient{
		resp: &positionkeepingv1.ListFinancialPositionLogsResponse{
			Logs: nil,
		},
	}

	provider := NewPKPositionProvider(client)
	page, err := provider.FetchPositions(context.Background(), "acc-001", 10, "")
	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Empty(t, page.NextPageToken)
}

func TestFetchPositions_UnspecifiedDirection(t *testing.T) {
	client := &stubPKClient{
		resp: &positionkeepingv1.ListFinancialPositionLogsResponse{
			Logs: []*positionkeepingv1.FinancialPositionLog{
				{
					LogId:     "log-1",
					AccountId: "acc-001",
					TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
						{
							Amount:    &commonv1.MoneyAmount{Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0}},
							Direction: commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
						},
					},
				},
			},
		},
	}

	provider := NewPKPositionProvider(client)
	page, err := provider.FetchPositions(context.Background(), "acc-001", 10, "")
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.True(t, decimal.Zero.Equal(page.Records[0].Balance))
	assert.Equal(t, "GBP", page.Records[0].InstrumentCode)
}

func TestFetchPositions_NilAmount(t *testing.T) {
	client := &stubPKClient{
		resp: &positionkeepingv1.ListFinancialPositionLogsResponse{
			Logs: []*positionkeepingv1.FinancialPositionLog{
				{
					LogId:     "log-1",
					AccountId: "acc-001",
					TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
						{
							Amount:    &commonv1.MoneyAmount{Amount: nil},
							Direction: commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
						},
					},
				},
			},
		},
	}

	provider := NewPKPositionProvider(client)
	page, err := provider.FetchPositions(context.Background(), "acc-001", 10, "")
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.True(t, decimal.Zero.Equal(page.Records[0].Balance))
	assert.Equal(t, "UNKNOWN", page.Records[0].InstrumentCode)
}

func TestMoneyToDecimal(t *testing.T) {
	tests := []struct {
		name    string
		money   *money.Money
		want    string
		wantErr error
	}{
		{"units only", &money.Money{Units: 100, Nanos: 0}, "100", nil},
		{"units and nanos", &money.Money{Units: 42, Nanos: 750000000}, "42.75", nil},
		{"zero", &money.Money{Units: 0, Nanos: 0}, "0", nil},
		{"negative", &money.Money{Units: -10, Nanos: -500000000}, "-10.5", nil},
		{"nil returns error", nil, "0", ErrNilMoneyValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := moneyToDecimal(tt.money)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got.String())
			}
		})
	}
}
