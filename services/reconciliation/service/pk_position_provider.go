package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
)

// ErrNilMoneyValue is returned when a nil money value is encountered during conversion.
var ErrNilMoneyValue = errors.New("nil money value")

// PKPositionProvider implements PositionDataProvider using PK's gRPC client.
// It uses ListFinancialPositionLogs with cursor-based pagination to fetch
// current position data for reconciliation.
type PKPositionProvider struct {
	client positionkeepingv1.PositionKeepingServiceClient
}

// NewPKPositionProvider creates a new provider backed by the PK gRPC service.
func NewPKPositionProvider(client positionkeepingv1.PositionKeepingServiceClient) *PKPositionProvider {
	return &PKPositionProvider{client: client}
}

// FetchPositions retrieves a page of position logs from PK and maps them to PositionRecords.
func (p *PKPositionProvider) FetchPositions(ctx context.Context, accountID string, pageSize int32, pageToken string) (*PositionPage, error) {
	resp, err := p.client.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
		AccountId: accountID,
		Pagination: &commonv1.Pagination{
			PageSize:  pageSize,
			PageToken: pageToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list financial position logs from PK: %w", err)
	}

	records := make([]PositionRecord, 0, len(resp.GetLogs()))
	for _, log := range resp.GetLogs() {
		records = append(records, mapLogToPositionRecord(ctx, log))
	}

	nextToken := ""
	if resp.GetPagination() != nil {
		nextToken = resp.GetPagination().GetNextPageToken()
	}

	return &PositionPage{
		Records:       records,
		NextPageToken: nextToken,
	}, nil
}

// mapLogToPositionRecord converts a single PK position log to a PositionRecord,
// aggregating entries into a net balance.
func mapLogToPositionRecord(ctx context.Context, log *positionkeepingv1.FinancialPositionLog) PositionRecord {
	balance := decimal.Zero
	instrumentCode := ""

	for _, entry := range log.GetTransactionLogEntries() {
		amount, err := moneyToDecimal(entry.GetAmount().GetAmount())
		if err != nil {
			slog.WarnContext(ctx, "skipping entry with invalid amount",
				"log_id", log.GetLogId(),
				"error", err,
			)
			continue
		}

		if instrumentCode == "" {
			if m := entry.GetAmount().GetAmount(); m != nil {
				instrumentCode = m.GetCurrencyCode()
			}
		}

		switch entry.GetDirection() {
		case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
			balance = balance.Add(amount)
		case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
			balance = balance.Sub(amount)
		case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
			slog.WarnContext(ctx, "skipping entry with unspecified direction",
				"log_id", log.GetLogId(),
			)
		}
	}

	if instrumentCode == "" {
		instrumentCode = "UNKNOWN"
	}

	return PositionRecord{
		AccountID:      log.GetAccountId(),
		InstrumentCode: instrumentCode,
		Balance:        balance,
		SourceSystem:   "position-keeping",
		Attributes:     map[string]string{"log_id": log.GetLogId()},
	}
}

// moneyToDecimal converts a google.type.Money to a decimal.Decimal.
// Money stores amounts as units (int64) + nanos (int32, 0-999999999).
func moneyToDecimal(m *money.Money) (decimal.Decimal, error) {
	if m == nil {
		return decimal.Zero, ErrNilMoneyValue
	}
	units := decimal.NewFromInt(m.GetUnits())
	nanos := decimal.NewFromInt(int64(m.GetNanos())).Div(decimal.NewFromInt(1_000_000_000))
	return units.Add(nanos), nil
}
