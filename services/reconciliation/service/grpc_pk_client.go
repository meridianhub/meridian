package service

import (
	"context"
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/shopspring/decimal"
)

// GrpcPositionKeepingClient adapts the generated PK gRPC client to the
// PositionKeepingClient interface used by BalanceAssertor.
type GrpcPositionKeepingClient struct {
	client positionkeepingv1.PositionKeepingServiceClient
}

// NewGrpcPositionKeepingClient creates a new adapter wrapping the PK gRPC client.
func NewGrpcPositionKeepingClient(client positionkeepingv1.PositionKeepingServiceClient) *GrpcPositionKeepingClient {
	return &GrpcPositionKeepingClient{client: client}
}

// GetPositionSummary fetches position logs for the given account and instrument code,
// aggregating total debits and credits across all pages.
func (c *GrpcPositionKeepingClient) GetPositionSummary(ctx context.Context, accountID, instrumentCode string) (*PositionSummary, error) {
	totalDebits := decimal.Zero
	totalCredits := decimal.Zero
	pageToken := ""

	for {
		resp, err := c.client.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
			AccountId: accountID,
			Pagination: &commonv1.Pagination{
				PageSize:  100,
				PageToken: pageToken,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("listing position logs: %w", err)
		}

		for _, log := range resp.GetLogs() {
			for _, entry := range log.GetTransactionLogEntries() {
				m := entry.GetAmount().GetAmount()
				if m == nil {
					continue
				}

				// Filter by instrument code using the entry's currency code.
				if instrumentCode != "" && m.GetCurrencyCode() != instrumentCode {
					continue
				}

				amount, err := moneyToDecimal(m)
				if err != nil {
					return nil, fmt.Errorf("converting amount for entry in log %s: %w", log.GetLogId(), err)
				}

				switch entry.GetDirection() {
				case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
					totalDebits = totalDebits.Add(amount)
				case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
					totalCredits = totalCredits.Add(amount)
				case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
					// Skip entries with unspecified direction
				}
			}
		}

		nextToken := ""
		if resp.GetPagination() != nil {
			nextToken = resp.GetPagination().GetNextPageToken()
		}
		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	return &PositionSummary{
		InstrumentCode: instrumentCode,
		TotalDebits:    totalDebits,
		TotalCredits:   totalCredits,
	}, nil
}
