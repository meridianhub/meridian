// Package domain provides double-entry accounting validation for the financial-accounting service.
//
// This file contains validation functions that enforce double-entry accounting rules
// with dimension awareness. All validation operates on the Qty[Monetary] (Money) type
// which provides compile-time dimension safety.
//
// # Double-Entry Accounting Rules
//
// In double-entry bookkeeping, every transaction must satisfy two fundamental rules:
//  1. Paired postings must have matching instruments (same currency/asset type)
//  2. Total debits must equal total credits for each instrument
//
// These validations ensure mathematical correctness of the ledger while respecting
// the type safety provided by the Universal Asset System's Qty[D] type.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors for double-entry validation.
var (
	// ErrDoubleEntryInstrumentMismatch is returned when paired debit and credit postings
	// have different instrument codes. In double-entry accounting, paired entries must
	// use the same currency or asset type.
	ErrDoubleEntryInstrumentMismatch = errors.New("double-entry validation failed: debit and credit must have matching instruments")

	// ErrUnbalancedTransaction is returned when the sum of debits does not equal
	// the sum of credits for a given instrument within a transaction.
	ErrUnbalancedTransaction = errors.New("transaction is unbalanced: sum of debits must equal sum of credits for each instrument")

	// ErrEmptyPostings is returned when validation is attempted on an empty slice of postings.
	ErrEmptyPostings = errors.New("cannot validate empty postings slice")

	// ErrOddNumberOfPostings is returned when a double-entry pair validation
	// receives a transaction with an odd number of postings.
	ErrOddNumberOfPostings = errors.New("double-entry requires an even number of postings")

	// ErrNilPosting is returned when a nil posting is passed to a validation function.
	ErrNilPosting = errors.New("posting cannot be nil")

	// ErrInvalidDebitDirection is returned when the first posting is not a DEBIT.
	ErrInvalidDebitDirection = errors.New("first posting must be DEBIT")

	// ErrInvalidCreditDirection is returned when the second posting is not a CREDIT.
	ErrInvalidCreditDirection = errors.New("second posting must be CREDIT")
)

// ValidateDoubleEntryPair validates that a debit and credit posting have matching instruments.
//
// In double-entry accounting, paired entries must use the same instrument to maintain
// the accounting equation. This function validates that:
//   - Both postings have the same instrument code and version
//   - The debit is a DEBIT direction and credit is a CREDIT direction
//
// Returns nil if the pair is valid, or an error describing the mismatch.
//
// Example valid pair:
//
//	debit:  100 USD DEBIT  to Cash account
//	credit: 100 USD CREDIT to Revenue account
//
// Example invalid pair (would return error):
//
//	debit:  100 USD DEBIT  to Cash account
//	credit: 100 EUR CREDIT to Revenue account
func ValidateDoubleEntryPair(debit, credit *LedgerPosting) error {
	if debit == nil || credit == nil {
		return ErrNilPosting
	}

	// Validate directions
	if debit.Direction != PostingDirectionDebit {
		return fmt.Errorf("%w: got %s", ErrInvalidDebitDirection, debit.Direction)
	}
	if credit.Direction != PostingDirectionCredit {
		return fmt.Errorf("%w: got %s", ErrInvalidCreditDirection, credit.Direction)
	}

	// Validate instrument match using the Instrument.Equal method
	debitInstrument := debit.Amount.Instrument
	creditInstrument := credit.Amount.Instrument

	if !debitInstrument.Equal(creditInstrument) {
		return fmt.Errorf("%w: debit instrument %s does not match credit instrument %s",
			ErrDoubleEntryInstrumentMismatch,
			debitInstrument.String(),
			creditInstrument.String())
	}

	return nil
}

// ValidateTransactionBalance validates that a set of postings is balanced for each instrument.
//
// In double-entry accounting, the fundamental equation must hold:
//
//	Sum(Debits) = Sum(Credits) for each unique instrument
//
// This function:
//  1. Groups postings by instrument code
//  2. Calculates the net balance for each group (debits - credits)
//  3. Returns an error if any instrument group has a non-zero balance
//
// The validation handles multi-currency/multi-asset transactions by validating
// each instrument group independently.
//
// Example valid transaction (single currency):
//
//	100 USD DEBIT  to Cash
//	 60 USD CREDIT to Revenue
//	 40 USD CREDIT to Tax Payable
//	Result: Valid (100 = 60 + 40)
//
// Example valid transaction (multi-currency - FX transaction):
//
//	100 USD DEBIT  to USD Cash
//	100 USD CREDIT to Currency Exchange
//	 85 EUR DEBIT  to Currency Exchange
//	 85 EUR CREDIT to EUR Cash
//	Result: Valid (USD: 100 = 100, EUR: 85 = 85)
//
// Example invalid transaction:
//
//	100 USD DEBIT  to Cash
//	 90 USD CREDIT to Revenue
//	Result: Error - USD is unbalanced by 10
func ValidateTransactionBalance(postings []*LedgerPosting) error {
	if len(postings) == 0 {
		return ErrEmptyPostings
	}

	// Group postings by instrument code.
	// We use code (not code+version) because within a single transaction,
	// all postings of the same currency should use the same version.
	type instrumentBalance struct {
		instrument Instrument
		netBalance Money // debits are positive, credits are negative
	}

	balances := make(map[string]*instrumentBalance)

	for _, posting := range postings {
		if posting == nil {
			continue
		}

		code := posting.Amount.Instrument.Code
		balance, exists := balances[code]

		if !exists {
			// First posting for this instrument - initialize with zero
			zero := ZeroMoney(posting.Amount.Instrument)
			balances[code] = &instrumentBalance{
				instrument: posting.Amount.Instrument,
				netBalance: zero,
			}
			balance = balances[code]
		}

		// Add or subtract based on direction
		var newBalance Money
		var err error

		if posting.Direction == PostingDirectionDebit {
			newBalance, err = balance.netBalance.Add(posting.Amount)
		} else {
			newBalance, err = balance.netBalance.Subtract(posting.Amount)
		}

		if err != nil {
			// This should only happen if instruments don't match (different versions)
			return fmt.Errorf("failed to calculate balance for %s: %w", code, err)
		}

		balance.netBalance = newBalance
	}

	// Check that all balances are zero
	for code, balance := range balances {
		if !balance.netBalance.IsZero() {
			direction := "debits exceed credits"
			amount := balance.netBalance.Amount
			if amount.IsNegative() {
				direction = "credits exceed debits"
				amount = amount.Abs()
			}

			return fmt.Errorf("%w: %s %s by %s %s",
				ErrUnbalancedTransaction,
				code,
				direction,
				amount.String(),
				code)
		}
	}

	return nil
}

// ValidatePostingPairBalance validates that a debit and credit posting have equal amounts.
//
// This is a convenience function that combines instrument matching and balance validation
// for a simple two-posting transaction.
//
// Returns nil if:
//   - Instruments match (same code and version)
//   - Amounts are equal
//
// This function is useful for simple transactions where you have exactly one debit
// and one credit posting.
func ValidatePostingPairBalance(debit, credit *LedgerPosting) error {
	// First validate the instruments match
	if err := ValidateDoubleEntryPair(debit, credit); err != nil {
		return err
	}

	// Then validate the amounts are equal
	if !debit.Amount.Equal(credit.Amount) {
		return fmt.Errorf("%w: debit amount %s does not equal credit amount %s",
			ErrUnbalancedTransaction,
			debit.Amount.String(),
			credit.Amount.String())
	}

	return nil
}
