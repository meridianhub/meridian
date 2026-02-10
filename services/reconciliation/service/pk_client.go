package service

import (
	"context"

	"github.com/shopspring/decimal"
)

// PositionSummary holds aggregated debit/credit totals for an instrument code.
type PositionSummary struct {
	InstrumentCode string
	TotalDebits    decimal.Decimal
	TotalCredits   decimal.Decimal
}

// PositionKeepingClient defines the interface for querying Position Keeping service
// to retrieve aggregated position summaries for balance assertion checks.
type PositionKeepingClient interface {
	// GetPositionSummary returns aggregated debits and credits for an account
	// and instrument code. If accountID is empty for cross-account scope,
	// the implementation should aggregate across all accounts for the instrument.
	GetPositionSummary(ctx context.Context, accountID, instrumentCode string) (*PositionSummary, error)
}
