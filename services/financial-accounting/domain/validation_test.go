package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// testInstrument creates an Instrument for testing.
func testInstrument(code string, dimension string) Instrument {
	inst, err := NewInstrument(code, 1, dimension, 2)
	if err != nil {
		panic("testInstrument: " + err.Error())
	}
	return inst
}

// testMoneyWithInstrument creates Money with a specific instrument.
func testMoneyWithInstrument(amount int64, inst Instrument) Money {
	return NewMoney(decimal.NewFromInt(amount), inst)
}

// testPosting creates a LedgerPosting for testing.
func testPosting(direction PostingDirection, amount Money, accountID string) *LedgerPosting {
	posting, err := NewLedgerPosting(
		uuid.New(),
		direction,
		amount,
		accountID,
		time.Now(),
		"test-correlation",
	)
	if err != nil {
		panic("testPosting: " + err.Error())
	}
	return posting
}

func TestValidateDoubleEntryPair_ValidMonetaryPair(t *testing.T) {
	// Test valid double-entry pair with same currency
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	debitAmount := NewMoney(decimal.NewFromInt(100), usdInst)
	creditAmount := NewMoney(decimal.NewFromInt(100), usdInst)

	debit := testPosting(PostingDirectionDebit, debitAmount, "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, creditAmount, "ACC-REVENUE")

	err := ValidateDoubleEntryPair(debit, credit)
	if err != nil {
		t.Errorf("Expected valid pair to pass, got error: %v", err)
	}
}

func TestValidateDoubleEntryPair_ValidDifferentAmounts(t *testing.T) {
	// ValidateDoubleEntryPair only checks instrument match, not amounts
	// This is intentional - use ValidatePostingPairBalance for amount validation
	gbpInst := MustCurrencyToInstrument(CurrencyGBP)
	debitAmount := NewMoney(decimal.NewFromInt(100), gbpInst)
	creditAmount := NewMoney(decimal.NewFromInt(50), gbpInst) // Different amount is OK for pair validation

	debit := testPosting(PostingDirectionDebit, debitAmount, "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, creditAmount, "ACC-REVENUE")

	err := ValidateDoubleEntryPair(debit, credit)
	if err != nil {
		t.Errorf("Expected pair validation to pass (amounts not checked), got error: %v", err)
	}
}

func TestValidateDoubleEntryPair_RejectsMismatchedCurrencies(t *testing.T) {
	// Test rejection when debit is USD and credit is EUR
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	eurInst := MustCurrencyToInstrument(CurrencyEUR)

	debitAmount := NewMoney(decimal.NewFromInt(100), usdInst)
	creditAmount := NewMoney(decimal.NewFromInt(100), eurInst)

	debit := testPosting(PostingDirectionDebit, debitAmount, "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, creditAmount, "ACC-REVENUE")

	err := ValidateDoubleEntryPair(debit, credit)
	if err == nil {
		t.Error("Expected error for mismatched currencies, got nil")
	}

	if !errors.Is(err, ErrDoubleEntryInstrumentMismatch) {
		t.Errorf("Expected ErrDoubleEntryInstrumentMismatch, got: %v", err)
	}

	// Verify error message is descriptive
	expectedContains := []string{"USD", "EUR", "debit", "credit"}
	for _, expected := range expectedContains {
		if !containsString(err.Error(), expected) {
			t.Errorf("Expected error message to contain '%s', got: %v", expected, err)
		}
	}
}

func TestValidateDoubleEntryPair_RejectsMismatchedInstrumentTypes(t *testing.T) {
	// Test rejection when mixing monetary (USD) with commodity (KWH)
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	kwhInst := testInstrument("KWH", "ENERGY")

	// Note: In practice, the type system prevents mixing Money and Asset at compile time.
	// But this test validates runtime behavior if instruments were somehow mixed.
	debitAmount := NewMoney(decimal.NewFromInt(100), usdInst)
	creditAmount := NewMoney(decimal.NewFromInt(100), kwhInst)

	debit := testPosting(PostingDirectionDebit, debitAmount, "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, creditAmount, "ACC-ENERGY")

	err := ValidateDoubleEntryPair(debit, credit)
	if err == nil {
		t.Error("Expected error for mismatched instrument types, got nil")
	}

	if !errors.Is(err, ErrDoubleEntryInstrumentMismatch) {
		t.Errorf("Expected ErrDoubleEntryInstrumentMismatch, got: %v", err)
	}
}

func TestValidateDoubleEntryPair_NilPostings(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	amount := NewMoney(decimal.NewFromInt(100), usdInst)
	posting := testPosting(PostingDirectionDebit, amount, "ACC-CASH")

	tests := []struct {
		name   string
		debit  *LedgerPosting
		credit *LedgerPosting
	}{
		{"nil debit", nil, posting},
		{"nil credit", posting, nil},
		{"both nil", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDoubleEntryPair(tt.debit, tt.credit)
			if err == nil {
				t.Error("Expected error for nil posting, got nil")
			}
		})
	}
}

func TestValidateDoubleEntryPair_WrongDirections(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	amount := NewMoney(decimal.NewFromInt(100), usdInst)

	debit := testPosting(PostingDirectionDebit, amount, "ACC-1")
	credit := testPosting(PostingDirectionCredit, amount, "ACC-2")

	tests := []struct {
		name   string
		first  *LedgerPosting
		second *LedgerPosting
		errMsg string
	}{
		{
			name:   "both debits",
			first:  debit,
			second: debit,
			errMsg: "CREDIT",
		},
		{
			name:   "both credits",
			first:  credit,
			second: credit,
			errMsg: "DEBIT",
		},
		{
			name:   "reversed order",
			first:  credit,
			second: debit,
			errMsg: "DEBIT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDoubleEntryPair(tt.first, tt.second)
			if err == nil {
				t.Error("Expected error for wrong directions, got nil")
			}
			if !containsString(err.Error(), tt.errMsg) {
				t.Errorf("Expected error to mention %s, got: %v", tt.errMsg, err)
			}
		})
	}
}

func TestValidateTransactionBalance_SingleCurrencyBalanced(t *testing.T) {
	gbpInst := MustCurrencyToInstrument(CurrencyGBP)

	// Simple balanced transaction: 100 GBP debit = 100 GBP credit
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), gbpInst), "ACC-CASH"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), gbpInst), "ACC-REVENUE"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected balanced transaction to pass, got error: %v", err)
	}
}

func TestValidateTransactionBalance_MultiPostingBalanced(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)

	// Multi-posting balanced: 100 USD debit = 60 + 40 USD credits
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-CASH"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(60), usdInst), "ACC-REVENUE"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(40), usdInst), "ACC-TAX"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected multi-posting balanced transaction to pass, got error: %v", err)
	}
}

func TestValidateTransactionBalance_MultiCurrencyBalanced(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	eurInst := MustCurrencyToInstrument(CurrencyEUR)

	// FX transaction: USD and EUR both independently balance
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-USD-CASH"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-FX"),
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(85), eurInst), "ACC-FX"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(85), eurInst), "ACC-EUR-CASH"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected multi-currency balanced transaction to pass, got error: %v", err)
	}
}

func TestValidateTransactionBalance_Unbalanced(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)

	// Unbalanced: 100 USD debit vs 90 USD credit
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-CASH"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(90), usdInst), "ACC-REVENUE"),
	}

	err := ValidateTransactionBalance(postings)
	if err == nil {
		t.Error("Expected error for unbalanced transaction, got nil")
	}

	if !errors.Is(err, ErrUnbalancedTransaction) {
		t.Errorf("Expected ErrUnbalancedTransaction, got: %v", err)
	}

	// Verify error message is descriptive
	if !containsString(err.Error(), "USD") {
		t.Errorf("Expected error to mention USD, got: %v", err)
	}
	if !containsString(err.Error(), "debits exceed credits") {
		t.Errorf("Expected error to mention direction, got: %v", err)
	}
}

func TestValidateTransactionBalance_CreditsExceedDebits(t *testing.T) {
	gbpInst := MustCurrencyToInstrument(CurrencyGBP)

	// Unbalanced: 50 GBP debit vs 100 GBP credit
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(50), gbpInst), "ACC-CASH"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), gbpInst), "ACC-REVENUE"),
	}

	err := ValidateTransactionBalance(postings)
	if err == nil {
		t.Error("Expected error for unbalanced transaction, got nil")
	}

	if !containsString(err.Error(), "credits exceed debits") {
		t.Errorf("Expected error to mention credits exceed debits, got: %v", err)
	}
}

func TestValidateTransactionBalance_OneCurrencyUnbalanced(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	eurInst := MustCurrencyToInstrument(CurrencyEUR)

	// USD balanced, EUR unbalanced
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-USD"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-USD"),
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(85), eurInst), "ACC-EUR"),
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(80), eurInst), "ACC-EUR"), // 5 EUR unbalanced
	}

	err := ValidateTransactionBalance(postings)
	if err == nil {
		t.Error("Expected error for partially unbalanced transaction, got nil")
	}

	if !containsString(err.Error(), "EUR") {
		t.Errorf("Expected error to mention EUR, got: %v", err)
	}
}

func TestValidateTransactionBalance_EmptyPostings(t *testing.T) {
	err := ValidateTransactionBalance([]*LedgerPosting{})
	if err == nil {
		t.Error("Expected error for empty postings, got nil")
	}

	if !errors.Is(err, ErrEmptyPostings) {
		t.Errorf("Expected ErrEmptyPostings, got: %v", err)
	}
}

func TestValidateTransactionBalance_NilPostingsInSlice(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)

	// Include nil postings - they should be skipped
	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-CASH"),
		nil, // Should be skipped
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-REVENUE"),
		nil, // Should be skipped
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected balanced transaction with nil entries to pass, got error: %v", err)
	}
}

func TestValidateTransactionBalance_DecimalPrecision(t *testing.T) {
	gbpInst := MustCurrencyToInstrument(CurrencyGBP)

	// Test with decimal amounts (using string constructor for precision)
	debitAmount, _ := NewMoneyFromString("99.99", gbpInst)
	creditAmount, _ := NewMoneyFromString("99.99", gbpInst)

	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, debitAmount, "ACC-CASH"),
		testPosting(PostingDirectionCredit, creditAmount, "ACC-REVENUE"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected decimal balanced transaction to pass, got error: %v", err)
	}
}

func TestValidatePostingPairBalance_ValidPair(t *testing.T) {
	gbpInst := MustCurrencyToInstrument(CurrencyGBP)
	amount := NewMoney(decimal.NewFromInt(100), gbpInst)

	debit := testPosting(PostingDirectionDebit, amount, "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, amount, "ACC-REVENUE")

	err := ValidatePostingPairBalance(debit, credit)
	if err != nil {
		t.Errorf("Expected valid pair to pass, got error: %v", err)
	}
}

func TestValidatePostingPairBalance_AmountMismatch(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)

	debit := testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-CASH")
	credit := testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(90), usdInst), "ACC-REVENUE")

	err := ValidatePostingPairBalance(debit, credit)
	if err == nil {
		t.Error("Expected error for amount mismatch, got nil")
	}

	if !errors.Is(err, ErrUnbalancedTransaction) {
		t.Errorf("Expected ErrUnbalancedTransaction, got: %v", err)
	}

	// Verify both amounts are in the error message
	if !containsString(err.Error(), "100") || !containsString(err.Error(), "90") {
		t.Errorf("Expected error to contain both amounts, got: %v", err)
	}
}

func TestValidatePostingPairBalance_InstrumentMismatch(t *testing.T) {
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	eurInst := MustCurrencyToInstrument(CurrencyEUR)

	debit := testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdInst), "ACC-USD")
	credit := testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), eurInst), "ACC-EUR")

	err := ValidatePostingPairBalance(debit, credit)
	if err == nil {
		t.Error("Expected error for instrument mismatch, got nil")
	}

	// Should fail at instrument check, not amount check
	if !errors.Is(err, ErrDoubleEntryInstrumentMismatch) {
		t.Errorf("Expected ErrDoubleEntryInstrumentMismatch, got: %v", err)
	}
}

func TestValidateTransactionBalance_CommodityBalanced(t *testing.T) {
	// Test with commodity instruments (energy)
	kwhInst := testInstrument("KWH", "ENERGY")

	postings := []*LedgerPosting{
		testPosting(PostingDirectionDebit, testMoneyWithInstrument(500, kwhInst), "ACC-INVENTORY"),
		testPosting(PostingDirectionCredit, testMoneyWithInstrument(500, kwhInst), "ACC-PURCHASE"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected commodity balanced transaction to pass, got error: %v", err)
	}
}

func TestValidateTransactionBalance_MixedMonetaryAndCommodity(t *testing.T) {
	// Real-world scenario: purchasing energy for cash
	// This creates separate balanced groups for each instrument type
	usdInst := MustCurrencyToInstrument(CurrencyUSD)
	kwhInst := testInstrument("KWH", "ENERGY")

	postings := []*LedgerPosting{
		// USD side: pay cash
		testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(1000), usdInst), "ACC-CASH"),
		testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(1000), usdInst), "ACC-EXPENSE"),
		// KWH side: receive energy
		testPosting(PostingDirectionDebit, testMoneyWithInstrument(500, kwhInst), "ACC-ENERGY-INVENTORY"),
		testPosting(PostingDirectionCredit, testMoneyWithInstrument(500, kwhInst), "ACC-ENERGY-RECEIVED"),
	}

	err := ValidateTransactionBalance(postings)
	if err != nil {
		t.Errorf("Expected mixed balanced transaction to pass, got error: %v", err)
	}
}

func TestValidateDoubleEntryPair_SameInstrumentDifferentVersions(t *testing.T) {
	// Test that different versions of the same instrument code are considered different
	usdV1, _ := NewInstrument("USD", 1, "CURRENCY", 2)
	usdV2, _ := NewInstrument("USD", 2, "CURRENCY", 2)

	debit := testPosting(PostingDirectionDebit, NewMoney(decimal.NewFromInt(100), usdV1), "ACC-1")
	credit := testPosting(PostingDirectionCredit, NewMoney(decimal.NewFromInt(100), usdV2), "ACC-2")

	err := ValidateDoubleEntryPair(debit, credit)
	if err == nil {
		t.Error("Expected error for different instrument versions, got nil")
	}

	if !errors.Is(err, ErrDoubleEntryInstrumentMismatch) {
		t.Errorf("Expected ErrDoubleEntryInstrumentMismatch, got: %v", err)
	}
}

// containsString is a helper to check if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
