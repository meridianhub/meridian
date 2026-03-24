package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPositionDataProvider is a configurable test implementation of PositionDataProvider.
type stubPositionDataProvider struct {
	pages []PositionPage
	idx   int
	err   error
}

func (s *stubPositionDataProvider) FetchPositions(_ context.Context, _ string, _ int32, _ string) (*PositionPage, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.idx >= len(s.pages) {
		return &PositionPage{}, nil
	}
	page := s.pages[s.idx]
	s.idx++
	return &page, nil
}

func TestPositionRecord_Fields(t *testing.T) {
	rec := PositionRecord{
		AccountID:      "ACC-001",
		InstrumentCode: "GBP",
		Balance:        decimal.NewFromFloat(1500.75),
		SourceSystem:   "position-keeping",
		Attributes:     map[string]string{"log_id": "log-123"},
	}

	assert.Equal(t, "ACC-001", rec.AccountID)
	assert.Equal(t, "GBP", rec.InstrumentCode)
	assert.True(t, decimal.NewFromFloat(1500.75).Equal(rec.Balance))
	assert.Equal(t, "position-keeping", rec.SourceSystem)
	assert.Equal(t, "log-123", rec.Attributes["log_id"])
}

func TestPositionRecord_NilAttributes(t *testing.T) {
	rec := PositionRecord{
		AccountID:      "ACC-001",
		InstrumentCode: "EUR",
		Balance:        decimal.Zero,
		SourceSystem:   "ledger",
	}

	assert.Nil(t, rec.Attributes)
	assert.True(t, rec.Balance.IsZero())
}

func TestPositionPage_Fields(t *testing.T) {
	records := []PositionRecord{
		{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100)},
		{AccountID: "ACC-002", InstrumentCode: "EUR", Balance: decimal.NewFromFloat(200)},
	}
	page := PositionPage{
		Records:       records,
		NextPageToken: "cursor-token-abc",
	}

	assert.Len(t, page.Records, 2)
	assert.Equal(t, "cursor-token-abc", page.NextPageToken)
}

func TestPositionPage_EmptyRecords(t *testing.T) {
	page := PositionPage{
		Records:       nil,
		NextPageToken: "",
	}

	assert.Empty(t, page.Records)
	assert.Empty(t, page.NextPageToken)
}

func TestPositionDataProvider_SinglePage(t *testing.T) {
	provider := &stubPositionDataProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(500), SourceSystem: "pk"},
				},
				NextPageToken: "",
			},
		},
	}

	var dp PositionDataProvider = provider
	page, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")

	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.Equal(t, "ACC-001", page.Records[0].AccountID)
	assert.Equal(t, "GBP", page.Records[0].InstrumentCode)
	assert.Empty(t, page.NextPageToken, "single page should have no next token")
}

func TestPositionDataProvider_MultiPageAggregation(t *testing.T) {
	provider := &stubPositionDataProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100)},
				},
				NextPageToken: "page2",
			},
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "EUR", Balance: decimal.NewFromFloat(200)},
				},
				NextPageToken: "",
			},
		},
	}

	var dp PositionDataProvider = provider

	// Fetch first page
	page1, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")
	require.NoError(t, err)
	require.Len(t, page1.Records, 1)
	assert.Equal(t, "GBP", page1.Records[0].InstrumentCode)
	assert.Equal(t, "page2", page1.NextPageToken)

	// Fetch second page
	page2, err := dp.FetchPositions(context.Background(), "ACC-001", 10, page1.NextPageToken)
	require.NoError(t, err)
	require.Len(t, page2.Records, 1)
	assert.Equal(t, "EUR", page2.Records[0].InstrumentCode)
	assert.Empty(t, page2.NextPageToken)
}

func TestPositionDataProvider_Error(t *testing.T) {
	provider := &stubPositionDataProvider{
		err: errors.New("position keeping unavailable"),
	}

	var dp PositionDataProvider = provider
	page, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")

	require.Error(t, err)
	assert.Nil(t, page)
	assert.Contains(t, err.Error(), "position keeping unavailable")
}

func TestPositionDataProvider_EmptyPage(t *testing.T) {
	provider := &stubPositionDataProvider{
		pages: []PositionPage{
			{Records: nil, NextPageToken: ""},
		},
	}

	var dp PositionDataProvider = provider
	page, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")

	require.NoError(t, err)
	assert.Empty(t, page.Records)
	assert.Empty(t, page.NextPageToken)
}

func TestPositionDataProvider_NegativeBalance(t *testing.T) {
	// Negative balances are valid (debit-heavy accounts)
	provider := &stubPositionDataProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(-250.50)},
				},
			},
		},
	}

	var dp PositionDataProvider = provider
	page, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")

	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.True(t, decimal.NewFromFloat(-250.50).Equal(page.Records[0].Balance))
}

func TestPositionDataProvider_MultipleSourceSystems(t *testing.T) {
	provider := &stubPositionDataProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "position-keeping"},
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(50), SourceSystem: "ledger"},
				},
			},
		},
	}

	var dp PositionDataProvider = provider
	page, err := dp.FetchPositions(context.Background(), "ACC-001", 10, "")

	require.NoError(t, err)
	require.Len(t, page.Records, 2)

	sources := map[string]bool{}
	for _, rec := range page.Records {
		sources[rec.SourceSystem] = true
	}
	assert.True(t, sources["position-keeping"])
	assert.True(t, sources["ledger"])
}
