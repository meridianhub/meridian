package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

// TestResolveOpeningBalance_CurrencyResolution targets two mutation-testing
// survivors on the `len(log.TransactionLogEntries) > 0` guard in
// resolveOpeningBalance (balance.go:157):
//
//   - CONDITIONALS_BOUNDARY (`> 0` -> `>= 0`): an empty entry slice would then
//     index TransactionLogEntries[0] and panic. The empty-log case below pins
//     the fallback path so the boundary cannot drift.
//   - CONDITIONALS_NEGATION (`> 0` -> `<= 0`): a log with entries would skip the
//     entry-currency branch and fall through to the hard-coded GBP default,
//     resolving the wrong currency for the opening balance. The non-GBP-entry
//     case asserts the resolved currency comes from the entry, not the default.
//
// Currency resolution feeds every downstream balance computation, so a wrong
// currency here silently misattributes a balance to the wrong instrument.
func TestResolveOpeningBalance_CurrencyResolution(t *testing.T) {
	t.Run("empty log with no opening balance falls back to GBP", func(t *testing.T) {
		// No initial entry, no opening balance: TransactionLogEntries is empty
		// and HasOpeningBalance() is false, so resolveOpeningBalance must take
		// the GBP fallback rather than indexing into the empty slice.
		log, err := domain.NewFinancialPositionLog("ACC-EMPTY", nil, nil)
		if err != nil {
			t.Fatalf("failed to create log: %v", err)
		}
		if log.HasOpeningBalance() {
			t.Fatal("precondition failed: expected HasOpeningBalance() to be false")
		}
		if len(log.TransactionLogEntries) != 0 {
			t.Fatalf("precondition failed: expected 0 entries, got %d", len(log.TransactionLogEntries))
		}

		opening, currency := service.ResolveOpeningBalanceForTesting(log)

		if currency.Code != string(domain.CurrencyGBP) {
			t.Errorf("currency = %q, want GBP fallback", currency.Code)
		}
		if !opening.Amount.IsZero() {
			t.Errorf("opening balance = %s, want zero", opening.Amount)
		}
	})

	t.Run("log with non-GBP entry resolves the entry currency", func(t *testing.T) {
		// A USD entry and no opening balance: resolveOpeningBalance must read the
		// instrument from the first transaction entry (USD), not default to GBP.
		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-USD",
			domain.MustNewMoney(decimal.NewFromInt(250), domain.CurrencyUSD),
			domain.PostingDirectionDebit,
			time.Now(),
			"USD entry",
			"REF-USD",
			domain.TransactionSourceManual,
		)
		if err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}

		log, err := domain.NewFinancialPositionLog("ACC-USD", entry, nil)
		if err != nil {
			t.Fatalf("failed to create log: %v", err)
		}
		if log.HasOpeningBalance() {
			t.Fatal("precondition failed: expected HasOpeningBalance() to be false")
		}

		opening, currency := service.ResolveOpeningBalanceForTesting(log)

		if currency.Code != string(domain.CurrencyUSD) {
			t.Errorf("currency = %q, want USD (resolved from entry, not GBP default)", currency.Code)
		}
		// The opening balance amount is always zero here; only the currency is
		// derived from the entry to avoid double-counting the entry itself.
		if !opening.Amount.IsZero() {
			t.Errorf("opening balance = %s, want zero", opening.Amount)
		}
		if opening.Instrument.Code != string(domain.CurrencyUSD) {
			t.Errorf("opening instrument = %q, want USD", opening.Instrument.Code)
		}
	})

	t.Run("log constructed with opening balance uses its instrument", func(t *testing.T) {
		log, err := domain.NewFinancialPositionLogWithOpeningBalance(
			"ACC-OB",
			domain.MustNewMoney(decimal.NewFromInt(1000), domain.CurrencyEUR),
			time.Now().Add(-time.Hour),
			"migration-ref",
		)
		if err != nil {
			t.Fatalf("failed to create log with opening balance: %v", err)
		}
		if !log.HasOpeningBalance() {
			t.Fatal("precondition failed: expected HasOpeningBalance() to be true")
		}

		opening, currency := service.ResolveOpeningBalanceForTesting(log)

		if currency.Code != string(domain.CurrencyEUR) {
			t.Errorf("currency = %q, want EUR (from opening balance instrument)", currency.Code)
		}
		// Opening balance entry is already represented as a transaction, so the
		// resolved opening amount is zero to avoid double-counting.
		if !opening.Amount.IsZero() {
			t.Errorf("opening balance = %s, want zero", opening.Amount)
		}
	})
}
