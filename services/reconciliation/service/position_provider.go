package service

import (
	"context"

	"github.com/shopspring/decimal"
)

// PositionRecord represents a single position entry fetched from Position Keeping.
// This is the reconciliation service's view of PK data, decoupled from the proto types.
type PositionRecord struct {
	AccountID      string
	InstrumentCode string
	Balance        decimal.Decimal
	SourceSystem   string
	Attributes     map[string]string
}

// PositionPage represents a page of position records with cursor-based pagination.
type PositionPage struct {
	Records       []PositionRecord
	NextPageToken string
}

// PositionDataProvider abstracts fetching position data from Position Keeping.
// This interface decouples the snapshot capturer from the PK gRPC client,
// enabling unit testing with mocks and allowing future changes to the data source.
type PositionDataProvider interface {
	// FetchPositions retrieves a page of current position balances for the given account.
	// Use an empty pageToken for the first page. Returns empty NextPageToken when no more pages.
	FetchPositions(ctx context.Context, accountID string, pageSize int32, pageToken string) (*PositionPage, error)
}
