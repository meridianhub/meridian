package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
)

// --- isValidBookingLogTransition ---

func TestIsValidBookingLogTransition(t *testing.T) {
	tests := []struct {
		name string
		from domain.TransactionStatus
		to   domain.TransactionStatus
		want bool
	}{
		// From PENDING
		{"pending to pending", domain.TransactionStatusPending, domain.TransactionStatusPending, true},
		{"pending to posted", domain.TransactionStatusPending, domain.TransactionStatusPosted, true},
		{"pending to failed", domain.TransactionStatusPending, domain.TransactionStatusFailed, true},
		{"pending to cancelled", domain.TransactionStatusPending, domain.TransactionStatusCancelled, true},
		{"pending to reversed is invalid", domain.TransactionStatusPending, domain.TransactionStatusReversed, false},

		// From POSTED
		{"posted to reversed", domain.TransactionStatusPosted, domain.TransactionStatusReversed, true},
		{"posted to pending is invalid", domain.TransactionStatusPosted, domain.TransactionStatusPending, false},
		{"posted to posted is invalid", domain.TransactionStatusPosted, domain.TransactionStatusPosted, false},
		{"posted to failed is invalid", domain.TransactionStatusPosted, domain.TransactionStatusFailed, false},
		{"posted to cancelled is invalid", domain.TransactionStatusPosted, domain.TransactionStatusCancelled, false},

		// Terminal states - no transitions allowed
		{"failed to pending is invalid", domain.TransactionStatusFailed, domain.TransactionStatusPending, false},
		{"failed to posted is invalid", domain.TransactionStatusFailed, domain.TransactionStatusPosted, false},
		{"failed to failed is invalid", domain.TransactionStatusFailed, domain.TransactionStatusFailed, false},
		{"cancelled to pending is invalid", domain.TransactionStatusCancelled, domain.TransactionStatusPending, false},
		{"reversed to pending is invalid", domain.TransactionStatusReversed, domain.TransactionStatusPending, false},

		// Unknown from status
		{"unknown to pending is invalid", domain.TransactionStatus("UNKNOWN"), domain.TransactionStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidBookingLogTransition(tt.from, tt.to)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- extractUserFromContext edge cases ---
// Note: TestExtractUserFromContext already exists in financial_accounting_service_test.go.
// These tests cover additional edge cases.

func TestExtractUserFromContext_EmptyStringReturnsSystem(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "")
	result := extractUserFromContext(ctx)
	assert.Equal(t, "system", result)
}

func TestExtractUserFromContext_NoContextReturnsSystem(t *testing.T) {
	result := extractUserFromContext(context.Background())
	assert.Equal(t, "system", result)
}

// --- decimalFromCents ---

func TestDecimalFromCents(t *testing.T) {
	tests := []struct {
		name     string
		cents    int64
		expected string
	}{
		{"zero", 0, "0"},
		{"100 cents is 1.00", 100, "1"},
		{"150 cents is 1.50", 150, "1.5"},
		{"1 cent is 0.01", 1, "0.01"},
		{"negative cents", -500, "-5"},
		{"large amount", 1234567, "12345.67"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decimalFromCents(tt.cents)
			assert.Equal(t, tt.expected, result.String())
		})
	}
}

// --- toProtoFinancialBookingLog ---

func TestToProtoFinancialBookingLog_Nil(t *testing.T) {
	result := toProtoFinancialBookingLog(nil)
	assert.Nil(t, result)
}

func TestToProtoFinancialBookingLog_WithPostings(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)
	bookingLogID := uuid.New()
	now := time.Now().UTC()

	posting1, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionDebit,
		amount,
		"ACC-001",
		now,
		"corr-1",
	)
	require.NoError(t, err)

	posting2, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		amount,
		"ACC-002",
		now,
		"corr-1",
	)
	require.NoError(t, err)

	bookingLog := domain.NewFinancialBookingLog(
		"CHECKING",
		"product-ref",
		"business-unit",
		"chart-rules",
		domain.CurrencyGBP,
	)
	withPostings := bookingLog.WithPosting(posting1).WithPosting(posting2)

	result := toProtoFinancialBookingLog(&withPostings)
	require.NotNil(t, result)
	assert.Equal(t, withPostings.ID.String(), result.Id)
	assert.Equal(t, "CHECKING", result.FinancialAccountType)
	assert.Equal(t, "product-ref", result.ProductServiceReference)
	assert.Equal(t, "business-unit", result.BusinessUnitReference)
	assert.Equal(t, "chart-rules", result.ChartOfAccountsRules)
	assert.Equal(t, "GBP", result.BaseInstrumentCode)
	assert.Len(t, result.Postings, 2)
	assert.NotNil(t, result.CreatedAt)
	assert.NotNil(t, result.UpdatedAt)
}

func TestToProtoFinancialBookingLog_NoPostings(t *testing.T) {
	bookingLog := domain.NewFinancialBookingLog(
		"SAVINGS",
		"product-ref",
		"bu-ref",
		"rules",
		domain.CurrencyUSD,
	)

	result := toProtoFinancialBookingLog(bookingLog)
	require.NotNil(t, result)
	assert.Empty(t, result.Postings)
}

// --- toProtoAccountType / fromProtoAccountType ---

func TestToProtoAccountType(t *testing.T) {
	assert.Equal(t, "CHECKING", toProtoAccountType("CHECKING"))
	assert.Equal(t, "", toProtoAccountType(""))
}

func TestFromProtoAccountType(t *testing.T) {
	assert.Equal(t, "SAVINGS", fromProtoAccountType("SAVINGS"))
	assert.Equal(t, "", fromProtoAccountType(""))
}

// --- WithRegistry and WithInstrumentResolver options ---
// Uses mockInstrumentRegistry from financial_accounting_service_test.go
// Uses mockInstrumentResolver from list_ledger_postings_test.go

func TestWithRegistry_SetsField(t *testing.T) {
	svc := &FinancialAccountingService{}
	registry := &mockInstrumentRegistry{}

	opt := WithRegistry(registry)
	opt(svc)

	assert.Equal(t, registry, svc.registry)
}

func TestWithInstrumentResolver_SetsField(t *testing.T) {
	svc := &FinancialAccountingService{}
	resolver := &mockInstrumentResolver{}

	opt := WithInstrumentResolver(resolver)
	opt(svc)

	assert.Equal(t, resolver, svc.instrumentResolver)
}

// --- AccountResolver cacheKey ---

func TestAccountResolver_CacheKey(t *testing.T) {
	resolver := &AccountResolver{}

	key := resolver.cacheKey(ClearingAccountTypeDeposit, "GBP")
	assert.Equal(t, "DEPOSIT:GBP", key)

	key = resolver.cacheKey(ClearingAccountTypeSettlement, "USD")
	assert.Equal(t, "SETTLEMENT:USD", key)
}

// --- NewPostingService constructors ---

func TestNewPostingService(t *testing.T) {
	svc := NewPostingService(nil, "bank-cash-001")
	require.NotNil(t, svc)
	assert.Equal(t, "bank-cash-001", svc.bankCashAccountID)
	assert.Nil(t, svc.accountResolver)
	assert.NotNil(t, svc.logger)
}

func TestNewPostingServiceWithConfig_NoResolver(t *testing.T) {
	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "cash-002",
	})
	require.NotNil(t, svc)
	assert.Equal(t, "cash-002", svc.bankCashAccountID)
	assert.Nil(t, svc.accountResolver)
	assert.NotNil(t, svc.logger)
}

// --- resolveClearingAccountForDeposit ---

func TestResolveClearingAccountForDeposit_NoResolver(t *testing.T) {
	svc := NewPostingService(nil, "static-fallback")

	result := svc.resolveClearingAccountForDeposit(context.Background(), "GBP")
	assert.Equal(t, "static-fallback", result)
}
