package domain

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// BalanceComputer errors.
var (
	// ErrEmptyEntries is returned when no entries are provided for computation.
	ErrEmptyEntries = errors.New("no entries provided for balance computation")

	// ErrNoInstrument is returned when unable to determine the instrument for a zero balance.
	ErrNoInstrument = errors.New("unable to determine instrument for zero balance")
)

// BalanceComputer computes different balance types from transaction log entries.
// It supports BIAN-compliant balance calculations including Opening, Closing,
// Current, Available, Ledger, Reserve, and Free balances.
//
// All computations are immutable - input data is never modified.
type BalanceComputer struct{}

// NewBalanceComputer creates a new BalanceComputer instance.
func NewBalanceComputer() *BalanceComputer {
	return &BalanceComputer{}
}

// ComputeOpening creates an Opening balance from the provided opening balance amount.
// This simply wraps the provided opening balance with the appropriate balance type.
func (bc *BalanceComputer) ComputeOpening(openingBalance Money, asOf time.Time) Balance {
	return Balance{
		Type:   BalanceTypeOpening,
		Amount: openingBalance,
		AsOf:   asOf,
	}
}

// ComputeCurrent calculates the Current balance from opening balance plus all transactions.
// Current balance includes ALL transactions regardless of status, providing the most
// up-to-date view of the position.
//
// For DEBIT entries: amount is added (increases the balance)
// For CREDIT entries: amount is subtracted (decreases the balance)
//
// Returns ErrInstrumentMismatch if entries have different currencies than the opening balance.
// Returns a zero balance with the opening balance's instrument if entries is empty.
func (bc *BalanceComputer) ComputeCurrent(openingBalance Money, entries []*TransactionLogEntry, asOf time.Time) (Balance, error) {
	sum, err := bc.sumEntries(entries, openingBalance.Instrument)
	if err != nil {
		return Balance{}, err
	}

	current, err := openingBalance.Add(sum)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeCurrent,
		Amount: current,
		AsOf:   asOf,
	}, nil
}

// ComputeLedger calculates the Ledger balance from entries that match the status filter.
// Typically used with a filter like: func(s TransactionStatus) bool { return s == TransactionStatusPosted }
//
// The statusFilter function receives the status associated with each entry and returns
// true if the entry should be included in the sum.
//
// Note: Since TransactionLogEntry does not contain status directly, the caller must provide
// a way to determine entry status. This is typically done by having the entry indices correspond
// to known statuses, or by using a closure that captures a status lookup function.
//
// For simpler use cases where all entries should be summed, use ComputeLedgerFromEntries.
//
// Returns ErrInstrumentMismatch if entries have different instruments.
// Returns ErrNoInstrument if entries is empty (cannot determine instrument for zero balance).
func (bc *BalanceComputer) ComputeLedger(entries []*TransactionLogEntry, _ func(TransactionStatus) bool, asOf time.Time) (Balance, error) {
	if len(entries) == 0 {
		return Balance{}, ErrNoInstrument
	}

	// For ledger balance, the caller should provide pre-filtered entries.
	// Since TransactionLogEntry doesn't contain status directly, the statusFilter
	// parameter is reserved for future use when entries include status information.
	// Currently, we sum all provided entries (caller is responsible for filtering by status).
	sum, err := bc.sumEntries(entries, entries[0].Amount.Instrument)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeLedger,
		Amount: sum,
		AsOf:   asOf,
	}, nil
}

// ComputeLedgerFromEntries calculates the Ledger balance by summing all provided entries.
// This is a convenience method when entries have already been filtered by status.
//
// Returns ErrInstrumentMismatch if entries have different instruments.
// Returns ErrNoInstrument if entries is empty.
func (bc *BalanceComputer) ComputeLedgerFromEntries(entries []*TransactionLogEntry, asOf time.Time) (Balance, error) {
	if len(entries) == 0 {
		return Balance{}, ErrNoInstrument
	}

	sum, err := bc.sumEntries(entries, entries[0].Amount.Instrument)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeLedger,
		Amount: sum,
		AsOf:   asOf,
	}, nil
}

// ComputeReserve creates a Reserve balance from the provided reserve amount.
// Reserve represents funds that are held or reserved (liens queried from Current Account externally).
func (bc *BalanceComputer) ComputeReserve(reserveAmount Money, asOf time.Time) Balance {
	return Balance{
		Type:   BalanceTypeReserve,
		Amount: reserveAmount,
		AsOf:   asOf,
	}
}

// ComputeAvailable calculates the Available balance.
// Available = Current - Reserve + Overdraft
//
// Available balance represents funds available for immediate withdrawal or use,
// excluding reserved funds and adding any overdraft allowance.
//
// Returns ErrInstrumentMismatch if amounts have different currencies.
func (bc *BalanceComputer) ComputeAvailable(currentBalance Money, reserveAmount Money, overdraftLimit Money, asOf time.Time) (Balance, error) {
	// Validate all amounts have the same instrument
	if !currentBalance.Instrument.Equal(reserveAmount.Instrument) {
		return Balance{}, ErrInstrumentMismatch
	}
	if !currentBalance.Instrument.Equal(overdraftLimit.Instrument) {
		return Balance{}, ErrInstrumentMismatch
	}

	// Compute: subtract reserve from current, then add overdraft allowance
	afterReserve, err := currentBalance.Subtract(reserveAmount)
	if err != nil {
		return Balance{}, err
	}

	available, err := afterReserve.Add(overdraftLimit)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeAvailable,
		Amount: available,
		AsOf:   asOf,
	}, nil
}

// ComputeFree calculates the Free balance.
// Free = Current - Reserve
//
// Free balance represents unencumbered funds with no restrictions.
//
// Returns ErrInstrumentMismatch if amounts have different currencies.
func (bc *BalanceComputer) ComputeFree(currentBalance Money, reserveAmount Money, asOf time.Time) (Balance, error) {
	// Validate amounts have the same instrument
	if !currentBalance.Instrument.Equal(reserveAmount.Instrument) {
		return Balance{}, ErrInstrumentMismatch
	}

	free, err := currentBalance.Subtract(reserveAmount)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeFree,
		Amount: free,
		AsOf:   asOf,
	}, nil
}

// ComputeClosing calculates the Closing balance for a period.
// Closing = Opening + sum of transactions up to periodEnd
//
// Only entries with Timestamp <= periodEnd are included in the calculation.
// The closing balance becomes the opening balance for the next period.
//
// Returns ErrInstrumentMismatch if entries have different currencies than the opening balance.
// Returns a balance equal to openingBalance if no entries fall within the period.
func (bc *BalanceComputer) ComputeClosing(openingBalance Money, entries []*TransactionLogEntry, periodEnd time.Time) (Balance, error) {
	// Filter entries up to periodEnd
	var filteredEntries []*TransactionLogEntry
	for _, entry := range entries {
		if !entry.Timestamp.After(periodEnd) {
			filteredEntries = append(filteredEntries, entry)
		}
	}

	// Sum filtered entries
	sum, err := bc.sumEntries(filteredEntries, openingBalance.Instrument)
	if err != nil {
		return Balance{}, err
	}

	closing, err := openingBalance.Add(sum)
	if err != nil {
		return Balance{}, err
	}

	return Balance{
		Type:   BalanceTypeClosing,
		Amount: closing,
		AsOf:   periodEnd,
	}, nil
}

// sumEntries calculates the net sum of transaction entries.
// DEBIT entries add to the sum, CREDIT entries subtract from the sum.
//
// The expectedInstrument is used to create a zero starting value and validate
// that all entries have matching instruments.
//
// Returns a zero Money with the expected instrument if entries is empty.
// Returns ErrInstrumentMismatch if any entry has a different instrument.
func (bc *BalanceComputer) sumEntries(entries []*TransactionLogEntry, expectedInstrument Instrument) (Money, error) {
	// Start with zero using the expected instrument
	sum := NewQty[Monetary](decimal.Zero, expectedInstrument)

	for _, entry := range entries {
		if entry == nil {
			continue
		}

		// Validate instrument matches
		if !entry.Amount.Instrument.Equal(expectedInstrument) {
			return Money{}, ErrInstrumentMismatch
		}

		var err error
		switch entry.Direction {
		case PostingDirectionDebit:
			sum, err = sum.Add(entry.Amount)
		case PostingDirectionCredit:
			sum, err = sum.Subtract(entry.Amount)
		}
		if err != nil {
			return Money{}, err
		}
	}

	return sum, nil
}
