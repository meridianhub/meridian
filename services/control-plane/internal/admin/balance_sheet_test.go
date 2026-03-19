package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
)

// mockPKClient is a test double for PositionKeepingClient.
type mockPKClient struct {
	logs []*positionkeepingv1.FinancialPositionLog
	err  error
}

func (m *mockPKClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &positionkeepingv1.ListFinancialPositionLogsResponse{
		Logs: m.logs,
	}, nil
}

func (m *mockPKClient) GetAccountBalance(_ context.Context, _ *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return nil, nil
}

// makeLog creates a test FinancialPositionLog with transaction entries.
func makeLog(accountID, logID, currencyCode string, entries ...txnEntry) *positionkeepingv1.FinancialPositionLog {
	protoEntries := make([]*positionkeepingv1.TransactionLogEntry, 0, len(entries))
	for _, e := range entries {
		dir := commonv1.PostingDirection_POSTING_DIRECTION_DEBIT
		if e.direction == "CREDIT" {
			dir = commonv1.PostingDirection_POSTING_DIRECTION_CREDIT
		}
		protoEntries = append(protoEntries, &positionkeepingv1.TransactionLogEntry{
			EntryId:       "entry-" + accountID,
			TransactionId: "txn-" + accountID,
			AccountId:     accountID,
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: currencyCode,
					Units:        e.units,
					Nanos:        e.nanos,
				},
			},
			Direction:   dir,
			Timestamp:   timestamppb.Now(),
			Description: "test entry",
		})
	}

	return &positionkeepingv1.FinancialPositionLog{
		LogId:                 logID,
		AccountId:             accountID,
		TransactionLogEntries: protoEntries,
		StatusTracking: &positionkeepingv1.StatusTracking{
			CurrentStatus:   commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			StatusUpdatedAt: timestamppb.Now(),
		},
		CreatedAt: timestamppb.Now(),
		UpdatedAt: timestamppb.Now(),
		Version:   1,
	}
}

type txnEntry struct {
	units     int64
	nanos     int32
	direction string
}

func TestGetBalanceSheet_MultiAssetAggregation(t *testing.T) {
	// Two GBP accounts (assets) + one KWH account (asset)
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_STRIPE_NOSTRO_001", "log-1", "GBP",
			txnEntry{units: 10000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("acme_STRIPE_NOSTRO_002", "log-2", "GBP",
			txnEntry{units: 2450, nanos: 0, direction: "DEBIT"},
		),
		makeLog("acme_ENERGY_INVENTORY_001", "log-3", "KWH",
			txnEntry{units: 45000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "acme", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, bs)
	assert.Equal(t, "acme", bs.TenantID)
	require.Len(t, bs.Sections, 3)

	// ASSETS section
	assets := bs.Sections[0]
	assert.Equal(t, ClassificationAssets, assets.Classification)
	assert.Len(t, assets.LineItems, 2) // STRIPE_NOSTRO + ENERGY_INVENTORY

	// Find STRIPE_NOSTRO line item
	var nostroItem *LineItem
	var energyItem *LineItem
	for i := range assets.LineItems {
		if assets.LineItems[i].AccountType == "STRIPE_NOSTRO" {
			nostroItem = &assets.LineItems[i]
		}
		if assets.LineItems[i].AccountType == "ENERGY_INVENTORY" {
			energyItem = &assets.LineItems[i]
		}
	}

	require.NotNil(t, nostroItem)
	assert.Equal(t, "GBP", nostroItem.Instrument)
	assert.True(t, nostroItem.Quantity.Equal(decimal.NewFromInt(12450)))
	assert.Equal(t, NormalBalanceDebit, nostroItem.NormalBalance)
	assert.Equal(t, int32(2), nostroItem.AccountCount)

	require.NotNil(t, energyItem)
	assert.Equal(t, "KWH", energyItem.Instrument)
	assert.True(t, energyItem.Quantity.Equal(decimal.NewFromInt(45000)))

	// Check totals
	assert.True(t, assets.Totals["GBP"].Equal(decimal.NewFromInt(12450)))
	assert.True(t, assets.Totals["KWH"].Equal(decimal.NewFromInt(45000)))
}

func TestGetBalanceSheet_ClassificationLogic(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		// Asset account (DEBIT normal balance)
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
		// Liability account (CREDIT normal balance)
		makeLog("tenant_ACCOUNTS_PAYABLE_001", "log-2", "GBP",
			txnEntry{units: 2000, nanos: 0, direction: "DEBIT"},
		),
		// Equity account
		makeLog("tenant_RETAINED_EARNINGS_001", "log-3", "GBP",
			txnEntry{units: 3000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "tenant", time.Now().UTC())
	require.NoError(t, err)

	// ASSETS
	assets := bs.Sections[0]
	assert.Equal(t, ClassificationAssets, assets.Classification)
	assert.Len(t, assets.LineItems, 1)
	assert.Equal(t, "CASH", assets.LineItems[0].AccountType)

	// LIABILITIES
	liabilities := bs.Sections[1]
	assert.Equal(t, ClassificationLiabilities, liabilities.Classification)
	assert.Len(t, liabilities.LineItems, 1)
	assert.Equal(t, "ACCOUNTS_PAYABLE", liabilities.LineItems[0].AccountType)

	// EQUITY
	equity := bs.Sections[2]
	assert.Equal(t, ClassificationEquity, equity.Classification)
	assert.Len(t, equity.LineItems, 1)
	assert.Equal(t, "RETAINED_EARNINGS", equity.LineItems[0].AccountType)
}

func TestGetBalanceSheet_EmptyLogs(t *testing.T) {
	client := &mockPKClient{logs: nil}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "empty_tenant", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, bs)
	assert.Equal(t, "empty_tenant", bs.TenantID)
	assert.Len(t, bs.Sections, 3)

	// All sections should be empty
	for _, section := range bs.Sections {
		assert.Empty(t, section.LineItems)
		assert.Empty(t, section.Totals)
	}
}

func TestGetBalanceSheet_DebitCreditNetting(t *testing.T) {
	// Account with both debit and credit entries
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 10000, nanos: 0, direction: "DEBIT"},
			txnEntry{units: 3000, nanos: 0, direction: "CREDIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "tenant", time.Now().UTC())
	require.NoError(t, err)

	assets := bs.Sections[0]
	require.Len(t, assets.LineItems, 1)
	// 10000 - 3000 = 7000
	assert.True(t, assets.LineItems[0].Quantity.Equal(decimal.NewFromInt(7000)))
}

func TestGetBalanceSheet_NanosHandling(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 100, nanos: 500000000, direction: "DEBIT"}, // 100.50
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "tenant", time.Now().UTC())
	require.NoError(t, err)

	assets := bs.Sections[0]
	require.Len(t, assets.LineItems, 1)
	expected, _ := decimal.NewFromString("100.5")
	assert.True(t, assets.LineItems[0].Quantity.Equal(expected))
}

func TestGetPositionDetails_FiltersByAccountTypeAndInstrument(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("tenant_CASH_002", "log-2", "GBP",
			txnEntry{units: 3000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("tenant_ENERGY_INVENTORY_001", "log-3", "KWH",
			txnEntry{units: 1000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	result, err := svc.GetPositionDetails(context.Background(), "tenant", "CASH", "GBP", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "CASH", result.AccountType)
	assert.Equal(t, "GBP", result.Instrument)
	assert.Len(t, result.Positions, 2)
	assert.True(t, result.Total.Equal(decimal.NewFromInt(8000)))
}

func TestGetPositionDetails_NoMatches(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	result, err := svc.GetPositionDetails(context.Background(), "tenant", "NONEXISTENT", "GBP", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Positions)
	assert.True(t, result.Total.IsZero())
}

func TestExportBalanceSheetCSV_ContainsData(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("acme_ACCOUNTS_PAYABLE_001", "log-2", "GBP",
			txnEntry{units: 2000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)

	csv, err := svc.ExportBalanceSheetCSV(context.Background(), "acme", time.Now().UTC())
	require.NoError(t, err)
	assert.Contains(t, csv, "Balance Sheet Export")
	assert.Contains(t, csv, "acme")
	assert.Contains(t, csv, "classification")
	assert.Contains(t, csv, "ASSETS")
	assert.Contains(t, csv, "LIABILITIES")
	assert.Contains(t, csv, "CASH")
	assert.Contains(t, csv, "ACCOUNTS_PAYABLE")
	assert.Contains(t, csv, "GBP")
}

func TestExtractAccountType(t *testing.T) {
	tests := []struct {
		accountID string
		expected  string
	}{
		{"acme_STRIPE_NOSTRO_001", "STRIPE_NOSTRO"},
		{"acme_CASH_001", "CASH"},
		{"acme_ACCOUNTS_PAYABLE_001", "ACCOUNTS_PAYABLE"},
		{"acme_RETAINED_EARNINGS_001", "RETAINED_EARNINGS"},
		{"acme_ENERGY_INVENTORY_001", "ENERGY_INVENTORY"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			result := extractAccountType(tt.accountID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClassifyNormalBalance(t *testing.T) {
	tests := []struct {
		accountType string
		expected    NormalBalance
	}{
		{"CASH", NormalBalanceDebit},
		{"STRIPE_NOSTRO", NormalBalanceDebit},
		{"ENERGY_INVENTORY", NormalBalanceDebit},
		{"ACCOUNTS_PAYABLE", NormalBalanceCredit},
		{"CUSTOMER_DEPOSIT", NormalBalanceCredit},
		{"DEFERRED_REVENUE", NormalBalanceCredit},
		{"RETAINED_EARNINGS", NormalBalanceCredit},
		{"OWNER_EQUITY", NormalBalanceCredit},
		{"CAPITAL", NormalBalanceCredit},
	}

	for _, tt := range tests {
		t.Run(tt.accountType, func(t *testing.T) {
			result := classifyNormalBalance(tt.accountType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClassifyAccount(t *testing.T) {
	tests := []struct {
		accountType   string
		normalBalance NormalBalance
		expected      BalanceSheetClassification
	}{
		{"CASH", NormalBalanceDebit, ClassificationAssets},
		{"STRIPE_NOSTRO", NormalBalanceDebit, ClassificationAssets},
		{"ACCOUNTS_PAYABLE", NormalBalanceCredit, ClassificationLiabilities},
		{"CUSTOMER_DEPOSIT", NormalBalanceCredit, ClassificationLiabilities},
		{"RETAINED_EARNINGS", NormalBalanceCredit, ClassificationEquity},
		{"OWNER_EQUITY", NormalBalanceCredit, ClassificationEquity},
		{"CAPITAL", NormalBalanceCredit, ClassificationEquity},
	}

	for _, tt := range tests {
		t.Run(tt.accountType, func(t *testing.T) {
			result := classifyAccount(tt.accountType, tt.normalBalance)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeLogBalance_DebitOnly(t *testing.T) {
	log := makeLog("test_CASH_001", "log-1", "GBP",
		txnEntry{units: 100, nanos: 0, direction: "DEBIT"},
		txnEntry{units: 200, nanos: 0, direction: "DEBIT"},
	)
	result := computeLogBalance(log)
	assert.True(t, result.Equal(decimal.NewFromInt(300)))
}

func TestComputeLogBalance_Mixed(t *testing.T) {
	log := makeLog("test_CASH_001", "log-1", "GBP",
		txnEntry{units: 1000, nanos: 0, direction: "DEBIT"},
		txnEntry{units: 300, nanos: 0, direction: "CREDIT"},
		txnEntry{units: 200, nanos: 500000000, direction: "DEBIT"}, // 200.50
	)
	result := computeLogBalance(log)
	expected, _ := decimal.NewFromString("900.5")
	assert.True(t, result.Equal(expected))
}

func TestComputeLogBalance_EmptyLog(t *testing.T) {
	log := makeLog("test_CASH_001", "log-1", "GBP")
	result := computeLogBalance(log)
	assert.True(t, result.IsZero())
}

func TestExtractInstrument(t *testing.T) {
	log := makeLog("test_CASH_001", "log-1", "GBP",
		txnEntry{units: 100, nanos: 0, direction: "DEBIT"},
	)
	assert.Equal(t, "GBP", extractInstrument(log))

	emptyLog := makeLog("test_CASH_001", "log-2", "GBP")
	assert.Equal(t, "UNKNOWN", extractInstrument(emptyLog))
}

// paginatedMockPKClient returns logs across multiple pages to test pagination.
type paginatedMockPKClient struct {
	// pages maps page token -> (logs, nextPageToken). Empty string key is the first page.
	pages map[string]struct {
		logs      []*positionkeepingv1.FinancialPositionLog
		nextToken string
	}
}

func (m *paginatedMockPKClient) ListFinancialPositionLogs(_ context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	pageToken := req.GetPagination().GetPageToken()
	page := m.pages[pageToken]
	return &positionkeepingv1.ListFinancialPositionLogsResponse{
		Logs: page.logs,
		Pagination: &commonv1.PaginationResponse{
			NextPageToken: page.nextToken,
		},
	}, nil
}

func (m *paginatedMockPKClient) GetAccountBalance(_ context.Context, _ *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return nil, nil
}

func TestGetBalanceSheet_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("rpc error: unavailable")}
	svc := NewBalanceSheetService(client, nil)

	_, err := svc.GetBalanceSheet(context.Background(), "acme", time.Now().UTC())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch position logs")
}

func TestGetPositionDetails_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("rpc error: unavailable")}
	svc := NewBalanceSheetService(client, nil)

	_, err := svc.GetPositionDetails(context.Background(), "acme", "CASH", "GBP", time.Now().UTC())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch position logs")
}

func TestExportBalanceSheetCSV_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("rpc error: unavailable")}
	svc := NewBalanceSheetService(client, nil)

	_, err := svc.ExportBalanceSheetCSV(context.Background(), "acme", time.Now().UTC())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch position logs")
}

func TestSanitizeCSVCell(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"normal text", "CASH", "CASH"},
		{"equals formula", "=SUM(A1:A10)", "'=SUM(A1:A10)"},
		{"plus formula", "+1+2", "'+1+2"},
		{"minus formula", "-1-2", "'-1-2"},
		{"at formula", "@SUM(A1)", "'@SUM(A1)"},
		{"normal number", "5000", "5000"},
		{"normal with spaces", "ACCOUNTS PAYABLE", "ACCOUNTS PAYABLE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeCSVCell(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetBalanceSheet_ZeroAsOf(t *testing.T) {
	// When asOf is zero, the service should default to now (not error)
	client := &mockPKClient{logs: nil}
	svc := NewBalanceSheetService(client, nil)

	bs, err := svc.GetBalanceSheet(context.Background(), "acme", time.Time{})
	require.NoError(t, err)
	require.NotNil(t, bs)
	// asOf should have been set to approximately now
	assert.False(t, bs.AsOf.IsZero())
}

func TestGetBalanceSheet_UnixEpochAsOf(t *testing.T) {
	// A nil proto Timestamp converts to Unix epoch — should be treated as "now"
	client := &mockPKClient{logs: nil}
	svc := NewBalanceSheetService(client, nil)

	epoch := time.Unix(0, 0)
	bs, err := svc.GetBalanceSheet(context.Background(), "acme", epoch)
	require.NoError(t, err)
	require.NotNil(t, bs)
	assert.NotEqual(t, int64(0), bs.AsOf.Unix())
}

func TestExtractInstrument_NilAmount(t *testing.T) {
	// Log with an entry whose Amount is nil
	log := &positionkeepingv1.FinancialPositionLog{
		LogId:     "log-nil-amount",
		AccountId: "test_CASH_001",
		TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
			{
				EntryId:   "entry-1",
				Amount:    nil,
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			},
		},
		CreatedAt: timestamppb.Now(),
		UpdatedAt: timestamppb.Now(),
	}
	assert.Equal(t, instrumentUnknown, extractInstrument(log))
}

func TestExtractInstrument_EmptyCurrencyCode(t *testing.T) {
	log := makeLog("test_CASH_001", "log-1", "",
		txnEntry{units: 100, nanos: 0, direction: "DEBIT"},
	)
	// makeLog uses "" as the currency code, extractInstrument should return UNKNOWN
	assert.Equal(t, instrumentUnknown, extractInstrument(log))
}

func TestEntryAmount_NilEntry(t *testing.T) {
	// Entry with nil Amount
	entry := &positionkeepingv1.TransactionLogEntry{
		Amount: nil,
	}
	result := entryAmount(entry)
	assert.True(t, result.IsZero())
}

func TestEntryAmount_NilMoney(t *testing.T) {
	// Entry with MoneyAmount but nil inner Amount
	entry := &positionkeepingv1.TransactionLogEntry{
		Amount: &commonv1.MoneyAmount{
			Amount: nil,
		},
	}
	result := entryAmount(entry)
	assert.True(t, result.IsZero())
}

func TestComputeLogBalance_UnspecifiedDirection(t *testing.T) {
	// Entries with UNSPECIFIED direction should be ignored
	log := &positionkeepingv1.FinancialPositionLog{
		LogId:     "log-unspecified",
		AccountId: "test_CASH_001",
		TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
			{
				EntryId: "entry-1",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
						Nanos:        0,
					},
				},
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
			},
			{
				EntryId: "entry-2",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        200,
						Nanos:        0,
					},
				},
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			},
		},
		CreatedAt: timestamppb.Now(),
		UpdatedAt: timestamppb.Now(),
	}
	result := computeLogBalance(log)
	// Only the DEBIT entry should count
	assert.True(t, result.Equal(decimal.NewFromInt(200)))
}

func TestExtractAccountType_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		expected  string
	}{
		{"single segment no underscore", "simple", "simple"},
		{"tenant with trailing numeric only", "acme_001", "001"},
		{"empty segments", "acme__CASH__001", "CASH"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAccountType(tt.accountID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsUpperAlpha(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"CASH", true},
		{"Cash", false},
		{"cash", false},
		{"123", false},
		{"CASH1", false},
		{"", false},
		{"A", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, isUpperAlpha(tt.input))
		})
	}
}

func TestGetBalanceSheet_Pagination(t *testing.T) {
	page1Logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_CASH_001", "log-1", "GBP",
			txnEntry{units: 1000, nanos: 0, direction: "DEBIT"},
		),
	}
	page2Logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_CASH_002", "log-2", "GBP",
			txnEntry{units: 2000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &paginatedMockPKClient{
		pages: map[string]struct {
			logs      []*positionkeepingv1.FinancialPositionLog
			nextToken string
		}{
			"":      {logs: page1Logs, nextToken: "page2"},
			"page2": {logs: page2Logs, nextToken: ""},
		},
	}

	svc := NewBalanceSheetService(client, nil)
	bs, err := svc.GetBalanceSheet(context.Background(), "acme", time.Now())
	require.NoError(t, err)

	// Should have aggregated logs from both pages
	assetsSection := bs.Sections[0]
	require.Len(t, assetsSection.LineItems, 1)
	assert.Equal(t, "CASH", assetsSection.LineItems[0].AccountType)
	assert.True(t, assetsSection.LineItems[0].Quantity.Equal(decimal.NewFromInt(3000)),
		"expected 3000 (1000+2000 from two pages), got %s", assetsSection.LineItems[0].Quantity)
	assert.Equal(t, int32(2), assetsSection.LineItems[0].AccountCount)
}
