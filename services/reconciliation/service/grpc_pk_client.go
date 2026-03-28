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

		debits, credits, err := aggregateLogEntries(resp.GetLogs(), instrumentCode)
		if err != nil {
			return nil, err
		}
		totalDebits = totalDebits.Add(debits)
		totalCredits = totalCredits.Add(credits)

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

// aggregateLogEntries sums debits and credits from position log entries,
// filtering by instrument code when specified.
func aggregateLogEntries(logs []*positionkeepingv1.FinancialPositionLog, instrumentCode string) (decimal.Decimal, decimal.Decimal, error) {
	debits := decimal.Zero
	credits := decimal.Zero

	for _, log := range logs {
		for _, entry := range log.GetTransactionLogEntries() {
			m := entry.GetAmount().GetAmount()
			if m == nil {
				continue
			}

			if instrumentCode != "" && m.GetCurrencyCode() != instrumentCode {
				continue
			}

			amount, err := moneyToDecimal(m)
			if err != nil {
				return decimal.Zero, decimal.Zero, fmt.Errorf("converting amount for entry in log %s: %w", log.GetLogId(), err)
			}

			switch entry.GetDirection() {
			case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
				debits = debits.Add(amount)
			case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
				credits = credits.Add(amount)
			case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
				// Skip entries with unspecified direction
			}
		}
	}

	return debits, credits, nil
}
