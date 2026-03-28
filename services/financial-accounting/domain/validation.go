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

	// ErrFungibilityMismatch is returned when debit and credit postings have attributes
	// that evaluate to different fungibility keys. This prevents invalid transactions
	// like exchanging RICE-KG with different batch IDs or grades when the instrument
	// defines these as non-fungible attributes.
	ErrFungibilityMismatch = errors.New("fungibility validation failed: debit and credit have incompatible attributes")

	// ErrFungibilityKeyEvaluation is returned when the CEL program fails to evaluate
	// the fungibility key expression.
	ErrFungibilityKeyEvaluation = errors.New("failed to evaluate fungibility key expression")

	// ErrFungibilityKeyResultType is returned when the CEL program returns a non-string type.
	ErrFungibilityKeyResultType = errors.New("fungibility key expression must return string")
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

	balances, err := aggregatePostingBalances(postings)
	if err != nil {
		return err
	}

	return checkBalancesZero(balances)
}

// instrumentBalance tracks the net balance for a single instrument.
type instrumentBalance struct {
	instrument Instrument
	netBalance Money // debits are positive, credits are negative
}

// aggregatePostingBalances groups postings by instrument code and calculates net balances.
func aggregatePostingBalances(postings []*LedgerPosting) (map[string]*instrumentBalance, error) {
	balances := make(map[string]*instrumentBalance)

	for _, posting := range postings {
		if posting == nil {
			continue
		}

		code := posting.Amount.Instrument.Code
		balance, exists := balances[code]

		if !exists {
			zero := ZeroMoney(posting.Amount.Instrument)
			balances[code] = &instrumentBalance{
				instrument: posting.Amount.Instrument,
				netBalance: zero,
			}
			balance = balances[code]
		}

		var newBalance Money
		var err error

		if posting.Direction == PostingDirectionDebit {
			newBalance, err = balance.netBalance.Add(posting.Amount)
		} else {
			newBalance, err = balance.netBalance.Subtract(posting.Amount)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to calculate balance for %s: %w", code, err)
		}

		balance.netBalance = newBalance
	}

	return balances, nil
}

// checkBalancesZero verifies all instrument balances are zero.
func checkBalancesZero(balances map[string]*instrumentBalance) error {
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

// FungibilityKeyProgram represents a pre-compiled CEL program for evaluating
// fungibility keys. This is a simplified interface that wraps cel.Program
// for easier testing and decoupling from the CEL library.
//
// The Eval method takes an activation (map of variable bindings) and returns:
//   - result: The fungibility key as a string
//   - error: Any evaluation error
//
// For production use, wrap cel.Program using CELProgramAdapter.
// For testing, use mockFungibilityKeyProgram or similar test doubles.
type FungibilityKeyProgram interface {
	Eval(activation interface{}) (interface{}, error)
}

// CELProgramAdapter wraps a cel.Program to implement FungibilityKeyProgram.
// This adapter handles the three-value return of cel.Program.Eval() and
// extracts the string value from ref.Val.
type CELProgramAdapter struct {
	// program is the underlying cel.Program.
	// We use interface{} here to avoid importing cel-go in the domain layer.
	// The actual type is cel.Program.
	program interface {
		Eval(interface{}) (interface{ Value() interface{} }, interface{}, error)
	}
}

// NewCELProgramAdapter creates an adapter that wraps a cel.Program.
// The program parameter should be a cel.Program from github.com/google/cel-go.
func NewCELProgramAdapter(program interface{}) *CELProgramAdapter {
	// Type assertion to verify the program has the expected Eval signature
	if p, ok := program.(interface {
		Eval(interface{}) (interface{ Value() interface{} }, interface{}, error)
	}); ok {
		return &CELProgramAdapter{program: p}
	}
	return nil
}

// Eval evaluates the CEL program with the given activation and returns the result.
func (a *CELProgramAdapter) Eval(activation interface{}) (interface{}, error) {
	if a.program == nil {
		return "", nil
	}

	result, _, err := a.program.Eval(activation)
	if err != nil {
		return nil, err
	}

	// Extract the actual value from ref.Val
	if result != nil {
		return result.Value(), nil
	}
	return "", nil
}

// ValidateFungibility checks that debit and credit postings have compatible
// fungibility attributes as defined by the instrument's fungibility_key_expression.
//
// Fungibility determines whether two quantities of the same instrument can be
// exchanged. For example:
//   - USD is fully fungible: $100 from any source equals $100 from any other source
//   - RICE-KG may NOT be fungible across batch_id or grade: 100kg of Grade-1 rice
//     from batch 2024-A cannot be exchanged for 100kg of Grade-2 rice from batch 2024-B
//
// Parameters:
//   - program: Pre-compiled CEL program for evaluating fungibility keys.
//     If nil, the instrument is fully fungible (returns nil immediately).
//   - debitAttrs: Attributes from the debit posting
//   - creditAttrs: Attributes from the credit posting
//
// Returns:
//   - nil if fungibility validation passes (keys match or instrument is fully fungible)
//   - ErrFungibilityMismatch if keys don't match
//   - ErrFungibilityKeyEvaluation if CEL evaluation fails
//
// Thread-safety: Safe for concurrent use as CEL programs are thread-safe.
func ValidateFungibility(program FungibilityKeyProgram, debitAttrs, creditAttrs map[string]string) error {
	// No program means fully fungible - all quantities are interchangeable
	if program == nil {
		return nil
	}

	// Normalize nil attributes to empty maps for CEL evaluation
	if debitAttrs == nil {
		debitAttrs = make(map[string]string)
	}
	if creditAttrs == nil {
		creditAttrs = make(map[string]string)
	}

	// Evaluate fungibility key for debit posting
	debitKey, err := evaluateFungibilityKey(program, debitAttrs)
	if err != nil {
		return fmt.Errorf("%w: debit attributes: %w", ErrFungibilityKeyEvaluation, err)
	}

	// Evaluate fungibility key for credit posting
	creditKey, err := evaluateFungibilityKey(program, creditAttrs)
	if err != nil {
		return fmt.Errorf("%w: credit attributes: %w", ErrFungibilityKeyEvaluation, err)
	}

	// Compare keys - they must match for the transaction to be valid
	if debitKey != creditKey {
		return fmt.Errorf("%w: debit key %q does not match credit key %q",
			ErrFungibilityMismatch,
			debitKey,
			creditKey)
	}

	return nil
}

// evaluateFungibilityKey evaluates the CEL program with the given attributes
// and returns the resulting fungibility key string.
func evaluateFungibilityKey(program FungibilityKeyProgram, attributes map[string]string) (string, error) {
	// Build CEL activation with attributes variable
	// The bucket key environment expects: attributes: map[string]string
	activation := map[string]interface{}{
		"attributes": attributes,
	}

	result, err := program.Eval(activation)
	if err != nil {
		return "", err
	}

	// Extract string value from result
	// For cel.Program, result is ref.Val; for mocks, it may be string directly
	switch v := result.(type) {
	case string:
		return v, nil
	default:
		// Handle cel.Program's ref.Val interface
		if valuer, ok := result.(interface{ Value() interface{} }); ok {
			if str, ok := valuer.Value().(string); ok {
				return str, nil
			}
		}
		return "", fmt.Errorf("%w: got %T", ErrFungibilityKeyResultType, result)
	}
}
