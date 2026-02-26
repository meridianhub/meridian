package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toMinorUnits is a helper to extract minor units from Amount, panicking on error.
// Used in tests to keep assertions concise.
func toMinorUnits(a Amount) int64 {
	v, err := a.ToMinorUnits()
	if err != nil {
		panic(err)
	}
	return v
}

func TestNewCurrentAccount(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	assert.Equal(t, "ACC-001", account.AccountID())
	assert.Equal(t, "PARTY-001", account.PartyID())
	assert.Equal(t, int64(0), toMinorUnits(account.Balance()))
	assert.Equal(t, "GBP", account.Balance().InstrumentCode())
	assert.Equal(t, AccountStatusActive, account.Status())
	assert.Equal(t, "GB82WEST12345698765432", account.ExternalIdentifier())
	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
}

func TestNewCurrentAccount_InstrumentAndDimensionFields(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "IBAN-001", "PARTY-001", "GBP")
	require.NoError(t, err)

	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, "IBAN-001", account.ExternalIdentifier())
	// Deprecated alias should still work
	assert.Equal(t, "IBAN-001", account.AccountIdentification())
}

func TestNewCurrentAccountWithDimension_ExplicitDimension(t *testing.T) {
	account, err := NewCurrentAccountWithDimension("ACC-001", "IDENT-001", "PARTY-001", "GBP", "CURRENCY", 2)
	require.NoError(t, err)

	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, "IDENT-001", account.ExternalIdentifier())
}

func TestNewCurrentAccountWithDimension_EnergyAccount(t *testing.T) {
	account, err := NewCurrentAccountWithDimension("ACC-KWH-001", "KWH-IDENT-001", "PARTY-001", "KWH", "ENERGY")
	require.NoError(t, err)

	assert.Equal(t, "KWH", account.InstrumentCode())
	assert.Equal(t, "ENERGY", account.Dimension())
	assert.Equal(t, int64(0), toMinorUnits(account.Balance()))
}

func TestNewCurrentAccountWithDimension_CarbonAccount(t *testing.T) {
	account, err := NewCurrentAccountWithDimension("ACC-CC-001", "CC-IDENT-001", "PARTY-001", "CARBON_CREDIT", "CARBON")
	require.NoError(t, err)

	assert.Equal(t, "CARBON_CREDIT", account.InstrumentCode())
	assert.Equal(t, "CARBON", account.Dimension())
	assert.Equal(t, int64(0), toMinorUnits(account.Balance()))
}

func TestDeposit(t *testing.T) {
	tests := []struct {
		name        string
		initialBal  int64
		depositAmt  int64
		wantBalance int64
		wantErr     bool
	}{
		{
			name:        "valid deposit",
			initialBal:  1000,
			depositAmt:  500,
			wantBalance: 1500,
			wantErr:     false,
		},
		{
			name:        "zero deposit",
			initialBal:  1000,
			depositAmt:  0,
			wantBalance: 1000,
			wantErr:     true,
		},
		{
			name:        "negative deposit",
			initialBal:  1000,
			depositAmt:  -500,
			wantBalance: 1000,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build account with initial balance using builder
			initialBalance, _ := NewMoney("GBP", tt.initialBal)
			account := NewCurrentAccountBuilder().
				WithAccountID("ACC-001").
				WithExternalIdentifier("GB82WEST12345698765432").
				WithPartyID("PARTY-001").
				WithBalance(initialBalance).
				WithAvailableBalance(initialBalance).
				WithStatus(AccountStatusActive).
				WithVersion(1).
				Build()

			depositMoney, _ := NewMoney("GBP", tt.depositAmt)
			updatedAccount, err := account.Deposit(depositMoney)

			if tt.wantErr {
				assert.Error(t, err)
				// Original account should be unchanged on error
				assert.Equal(t, tt.initialBal, toMinorUnits(account.Balance()))
			} else {
				assert.NoError(t, err)
				// Updated account should have new balance
				assert.Equal(t, tt.wantBalance, toMinorUnits(updatedAccount.Balance()))
				// Original account should be unchanged (immutability)
				assert.Equal(t, tt.initialBal, toMinorUnits(account.Balance()))
			}
		})
	}
}

func TestDepositWhenFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account, _ = account.Freeze("Suspicious activity detected on account")

	depositMoney, _ := NewMoney("GBP", 1000)
	_, err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestDepositWhenClosed(t *testing.T) {
	// Build a closed account using builder (simulating a reconstructed account)
	zeroMoney, _ := NewMoney("GBP", 0)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		WithVersion(1).
		Build()

	depositMoney, _ := NewMoney("GBP", 1000)
	_, err := account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestPrepareForCredit(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// PrepareForCredit should succeed for active account
	prepared, err := account.PrepareForCredit()
	require.NoError(t, err)

	// Balance should NOT change (Position Keeping is source of truth)
	assert.Equal(t, toMinorUnits(account.Balance()), toMinorUnits(prepared.Balance()))

	// Version should be incremented for optimistic locking
	assert.Equal(t, account.Version()+1, prepared.Version())

	// UpdatedAt should be set
	assert.True(t, prepared.UpdatedAt().After(account.UpdatedAt()) || prepared.UpdatedAt().Equal(account.UpdatedAt()))
}

func TestPrepareForCreditWhenFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account, _ = account.Freeze("Suspicious activity detected on account")

	_, err = account.PrepareForCredit()

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestPrepareForCreditWhenClosed(t *testing.T) {
	// Build a closed account using builder (simulating a reconstructed account)
	zeroMoney, _ := NewMoney("GBP", 0)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		WithVersion(1).
		Build()

	_, err := account.PrepareForCredit()

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestPrepareForDebit(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000) // £100.00
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	withdrawAmount, _ := NewMoney("GBP", 5000) // £50.00

	// PrepareForDebit should succeed for active account with sufficient funds
	prepared, err := account.PrepareForDebit(withdrawAmount)
	require.NoError(t, err)

	// Balance should NOT change (Position Keeping is source of truth)
	assert.Equal(t, toMinorUnits(account.Balance()), toMinorUnits(prepared.Balance()))

	// Version should be incremented for optimistic locking
	assert.Equal(t, account.Version()+1, prepared.Version())
}

func TestPrepareForDebitInsufficientFunds(t *testing.T) {
	balance, _ := NewMoney("GBP", 5000) // £50.00
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	withdrawAmount, _ := NewMoney("GBP", 10000) // £100.00 - more than available

	_, err := account.PrepareForDebit(withdrawAmount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientFunds)
}

func TestPrepareForDebitWhenFrozen(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusFrozen).
		WithFreezeReason("Suspicious activity").
		WithVersion(1).
		Build()

	withdrawAmount, _ := NewMoney("GBP", 5000)
	_, err := account.PrepareForDebit(withdrawAmount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestPrepareForDebitWhenClosed(t *testing.T) {
	zeroMoney, _ := NewMoney("GBP", 0)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		WithVersion(1).
		Build()

	withdrawAmount, _ := NewMoney("GBP", 5000)
	_, err := account.PrepareForDebit(withdrawAmount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestWithdraw(t *testing.T) {
	tests := []struct {
		name          string
		initialBal    int64
		withdrawAmt   int64
		wantBalance   int64
		wantErr       bool
		expectedError error
	}{
		{
			name:        "valid withdrawal",
			initialBal:  1000,
			withdrawAmt: 500,
			wantBalance: 500,
			wantErr:     false,
		},
		{
			name:          "insufficient funds",
			initialBal:    1000,
			withdrawAmt:   1500,
			wantBalance:   1000,
			wantErr:       true,
			expectedError: ErrInsufficientFunds,
		},
		{
			name:        "zero withdrawal",
			initialBal:  1000,
			withdrawAmt: 0,
			wantBalance: 1000,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build account with initial balance using builder
			initialBalance, _ := NewMoney("GBP", tt.initialBal)
			account := NewCurrentAccountBuilder().
				WithAccountID("ACC-001").
				WithExternalIdentifier("GB82WEST12345698765432").
				WithPartyID("PARTY-001").
				WithBalance(initialBalance).
				WithAvailableBalance(initialBalance).
				WithStatus(AccountStatusActive).
				WithVersion(1).
				Build()

			withdrawMoney, _ := NewMoney("GBP", tt.withdrawAmt)
			updatedAccount, err := account.Withdraw(withdrawMoney)

			if tt.wantErr {
				assert.Error(t, err)
				// Original account should be unchanged on error
				assert.Equal(t, tt.initialBal, toMinorUnits(account.Balance()))
			} else {
				assert.NoError(t, err)
				// Updated account should have new balance
				assert.Equal(t, tt.wantBalance, toMinorUnits(updatedAccount.Balance()))
				// Original account should be unchanged (immutability)
				assert.Equal(t, tt.initialBal, toMinorUnits(account.Balance()))
			}

			if tt.expectedError != nil {
				assert.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestWithdraw_ExceedsAvailableBalance(t *testing.T) {
	// Build account with £10 balance
	initialBalance, _ := NewMoney("GBP", 1000) // £10
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Attempt to withdraw £20 (exceeds £10 available)
	withdrawMoney, _ := NewMoney("GBP", 2000) // £20
	_, err := account.Withdraw(withdrawMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientFunds)
}

func TestWithdraw_InstrumentMismatch(t *testing.T) {
	// Create GBP account with balance
	initialBalance, _ := NewMoney("GBP", 10000) // £100
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Attempt EUR withdrawal from GBP account
	withdrawMoney, _ := NewMoney("EUR", 5000) // €50
	_, err := account.Withdraw(withdrawMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestWithdraw_FrozenAccount(t *testing.T) {
	// Create account and freeze it
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Deposit some money first
	depositMoney, _ := NewMoney("GBP", 10000)
	account, err = account.Deposit(depositMoney)
	require.NoError(t, err)

	// Freeze the account
	account, err = account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	// Attempt withdrawal from frozen account
	withdrawMoney, _ := NewMoney("GBP", 5000)
	_, err = account.Withdraw(withdrawMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestWithdraw_ClosedAccount(t *testing.T) {
	// Build a closed account with balance using builder (simulating a legacy or reconstructed account)
	// This tests that withdrawal is blocked on closed accounts regardless of balance
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusClosed).
		WithVersion(1).
		Build()

	// Attempt withdrawal from closed account
	withdrawMoney, _ := NewMoney("GBP", 5000)
	_, err := account.Withdraw(withdrawMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestWithdraw_ExactAvailableBalance(t *testing.T) {
	// Build account with exact balance
	initialBalance, _ := NewMoney("GBP", 10000) // £100
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Withdraw exact available balance
	withdrawMoney, _ := NewMoney("GBP", 10000)
	updatedAccount, err := account.Withdraw(withdrawMoney)

	assert.NoError(t, err)
	assert.Equal(t, int64(0), toMinorUnits(updatedAccount.Balance()))
}

func TestWithdraw_ExactAvailableBalanceFromService(t *testing.T) {
	// Simulate the service layer setting availableBalance independently from balance.
	// The domain no longer manages overdraft - service sets available balance.
	initialBalance, _ := NewMoney("GBP", 1000)   // £10
	availableBalance, _ := NewMoney("GBP", 1500) // £15 (service has added overdraft externally)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(availableBalance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Withdraw exact available balance (£15)
	withdrawMoney, _ := NewMoney("GBP", 1500)
	updatedAccount, err := account.Withdraw(withdrawMoney)

	assert.NoError(t, err)
	// Balance goes from £10 to -£5 (service-managed overdraft zone)
	assert.Equal(t, int64(-500), toMinorUnits(updatedAccount.Balance()))
}

func TestWithdraw_NegativeAmount(t *testing.T) {
	initialBalance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Attempt to withdraw negative amount
	withdrawMoney, _ := NewMoney("GBP", -500)
	_, err := account.Withdraw(withdrawMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAmount)
}

func TestStatusTransitions(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Active -> Frozen
	account, err = account.Freeze("Suspicious activity detected on account")
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusFrozen, account.Status())
	assert.Equal(t, "Suspicious activity detected on account", account.FreezeReason())

	// Frozen -> Active (using Unfreeze)
	account, err = account.Unfreeze()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusActive, account.Status())
	assert.Empty(t, account.FreezeReason()) // Freeze reason should be cleared

	// Active -> Closed (balance is zero)
	account, err = account.Close("Account closure requested by customer")
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, account.Status())

	// Closed -> Active (should fail)
	_, err = account.Activate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestBuilder_InstrumentCode_Dimension(t *testing.T) {
	// Test that builder correctly sets instrumentCode and dimension fields
	balance, _ := NewMoney("GBP", 1000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, "GB82WEST12345698765432", account.ExternalIdentifier())
	// Deprecated alias
	assert.Equal(t, "GB82WEST12345698765432", account.AccountIdentification())
}

func TestInstrumentMismatch_Deposit(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	depositMoney, _ := NewMoney("USD", 1000)
	_, err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestDepositKWH_IntoKWHAccount(t *testing.T) {
	account, err := NewCurrentAccountWithDimension("ACC-KWH-001", "KWH-001", "PARTY-001", "KWH", "ENERGY")
	require.NoError(t, err)

	depositAmount, err := NewAmountFromInstrument("KWH", "ENERGY", 0, 1500) // 1500 KWH (whole units)
	require.NoError(t, err)

	updatedAccount, err := account.Deposit(depositAmount)
	require.NoError(t, err)

	assert.Equal(t, int64(1500), toMinorUnits(updatedAccount.Balance()))
	assert.Equal(t, "KWH", updatedAccount.Balance().InstrumentCode())
	assert.Equal(t, "ENERGY", updatedAccount.Balance().Dimension())
}

func TestDepositKWH_IntoGBPAccount_Fails(t *testing.T) {
	account, err := NewCurrentAccount("ACC-GBP-001", "GBP-001", "PARTY-001", "GBP")
	require.NoError(t, err)

	depositAmount, err := NewAmountFromInstrument("KWH", "ENERGY", 3, 1500)
	require.NoError(t, err)

	_, err = account.Deposit(depositAmount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentMismatch)
}

func TestWithdrawFromNonCurrencyAccount(t *testing.T) {
	account, err := NewCurrentAccountWithDimension("ACC-KWH-001", "KWH-001", "PARTY-001", "KWH", "ENERGY")
	require.NoError(t, err)

	// Deposit first
	depositAmount, err := NewAmountFromInstrument("KWH", "ENERGY", 0, 5000) // 5000 KWH
	require.NoError(t, err)
	account, err = account.Deposit(depositAmount)
	require.NoError(t, err)

	// Withdraw
	withdrawAmount, err := NewAmountFromInstrument("KWH", "ENERGY", 0, 2000) // 2000 KWH
	require.NoError(t, err)
	updatedAccount, err := account.Withdraw(withdrawAmount)
	require.NoError(t, err)

	assert.Equal(t, int64(3000), toMinorUnits(updatedAccount.Balance())) // 3000 KWH
}

func TestCreateLienOnNonCurrencyAccount(t *testing.T) {
	// PrepareForDebit acts as lien validation - verifies dimension-agnostic operation
	carbonAmount, err := NewAmountFromInstrument("CARBON_CREDIT", "CARBON", 0, 100) // 100 CARBON_CREDIT
	require.NoError(t, err)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-CC-001").
		WithExternalIdentifier("CC-001").
		WithInstrumentCode("CARBON_CREDIT").
		WithDimension("CARBON").
		WithPartyID("PARTY-001").
		WithBalance(carbonAmount).
		WithAvailableBalance(carbonAmount).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	debitAmount, err := NewAmountFromInstrument("CARBON_CREDIT", "CARBON", 0, 50)
	require.NoError(t, err)

	prepared, err := account.PrepareForDebit(debitAmount)
	require.NoError(t, err)

	// Balance unchanged, version bumped
	assert.Equal(t, int64(100), toMinorUnits(prepared.Balance()))
	assert.Equal(t, account.Version()+1, prepared.Version())
}

// Defensive test per ADR-008: Constructor validation

func TestNewCurrentAccount_InvalidInstrument_ReturnsError(t *testing.T) {
	tests := []struct {
		name      string
		currency  string
		wantErr   bool
		rationale string
	}{
		{
			name:      "empty currency",
			currency:  "",
			wantErr:   true,
			rationale: "Empty instrument code should be rejected at construction",
		},
		{
			name:      "valid currency",
			currency:  "GBP",
			wantErr:   false,
			rationale: "Valid currency should succeed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", tt.currency)

			if tt.wantErr {
				assert.Error(t, err, tt.rationale)
			} else {
				assert.NoError(t, err, tt.rationale)
			}
		})
	}
}

// Tests for large values with decimal-based Amount implementation
// Note: The decimal-based Amount implementation does not overflow on arithmetic
// operations like int64 did. Overflow is now checked when converting to minor units.

// Tests for large deposits
func TestDeposit_LargeValues(t *testing.T) {
	// Build account with large balance using builder
	balance, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Large deposit
	deposit, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)

	updatedAccount, err := account.Deposit(deposit)
	assert.NoError(t, err, "Large deposits should be handled correctly")
	assert.Equal(t, int64(2000000000000), toMinorUnits(updatedAccount.Balance()))
}

// Immutability tests

func TestImmutability_DepositDoesNotModifyOriginal(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	originalBalance := toMinorUnits(account.Balance())
	originalVersion := account.Version()

	deposit, _ := NewMoney("GBP", 10000)
	_, err = account.Deposit(deposit)
	require.NoError(t, err)

	// Original should be unchanged
	assert.Equal(t, originalBalance, toMinorUnits(account.Balance()))
	assert.Equal(t, originalVersion, account.Version())
}

func TestImmutability_WithdrawDoesNotModifyOriginal(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	originalBalance := toMinorUnits(account.Balance())
	originalVersion := account.Version()

	withdrawal, _ := NewMoney("GBP", 5000)
	_, err := account.Withdraw(withdrawal)
	require.NoError(t, err)

	// Original should be unchanged
	assert.Equal(t, originalBalance, toMinorUnits(account.Balance()))
	assert.Equal(t, originalVersion, account.Version())
}

func TestImmutability_FreezeDoesNotModifyOriginal(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	originalStatus := account.Status()

	_, err = account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	// Original should be unchanged
	assert.Equal(t, originalStatus, account.Status())
}

func TestImmutability_ChainedOperations(t *testing.T) {
	// Create initial account
	original, _ := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")

	// Perform multiple operations, capturing each result
	deposit1, _ := NewMoney("GBP", 10000)
	afterDeposit1, _ := original.Deposit(deposit1)

	deposit2, _ := NewMoney("GBP", 5000)
	afterDeposit2, _ := afterDeposit1.Deposit(deposit2)

	withdrawal, _ := NewMoney("GBP", 3000)
	afterWithdrawal, _ := afterDeposit2.Withdraw(withdrawal)

	// Verify each instance has independent state
	assert.Equal(t, int64(0), toMinorUnits(original.Balance()))
	assert.Equal(t, int64(10000), toMinorUnits(afterDeposit1.Balance()))
	assert.Equal(t, int64(15000), toMinorUnits(afterDeposit2.Balance()))
	assert.Equal(t, int64(12000), toMinorUnits(afterWithdrawal.Balance()))

	// Verify versions are incrementally updated
	assert.Equal(t, int64(1), original.Version())
	assert.Equal(t, int64(2), afterDeposit1.Version())
	assert.Equal(t, int64(3), afterDeposit2.Version())
	assert.Equal(t, int64(4), afterWithdrawal.Version())
}

func TestImmutability_FailedOperationDoesNotModify(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	originalBalance := toMinorUnits(account.Balance())
	originalVersion := account.Version()

	// Attempt an operation that will fail
	excessiveWithdrawal, _ := NewMoney("GBP", 99999)
	_, err := account.Withdraw(excessiveWithdrawal)

	// Verify original is unchanged despite the failed operation
	require.Error(t, err)
	assert.Equal(t, originalBalance, toMinorUnits(account.Balance()))
	assert.Equal(t, originalVersion, account.Version())
}

// Builder tests

func TestCurrentAccountBuilder(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	available, _ := NewMoney("GBP", 15000)
	now := time.Now()

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithPartyID("party-123").
		WithBalance(balance).
		WithAvailableBalance(available).
		WithStatus(AccountStatusActive).
		WithVersion(5).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		WithBalanceUpdatedAt(now).
		Build()

	assert.Equal(t, "ACC-001", account.AccountID())
	assert.Equal(t, "GB82WEST12345698765432", account.ExternalIdentifier())
	assert.Equal(t, "GB82WEST12345698765432", account.AccountIdentification()) // deprecated alias
	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, "party-123", account.PartyID())
	assert.Equal(t, int64(10000), toMinorUnits(account.Balance()))
	assert.Equal(t, int64(15000), toMinorUnits(account.AvailableBalance()))
	assert.Equal(t, AccountStatusActive, account.Status())
	assert.Equal(t, int64(5), account.Version())
}

// Tests for state machine enforcement

func TestFreeze_ValidTransition(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Suspicious activity detected on account")

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusFrozen, frozenAccount.Status())
	assert.Equal(t, "Suspicious activity detected on account", frozenAccount.FreezeReason())

	// Verify status history is recorded
	history := frozenAccount.StatusHistory()
	require.Len(t, history, 1)
	assert.Equal(t, AccountStatusActive, history[0].From)
	assert.Equal(t, AccountStatusFrozen, history[0].To)
	assert.Equal(t, "Suspicious activity detected on account", history[0].Reason)
}

func TestFreeze_InvalidFromFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Initial freeze reason for testing")
	require.NoError(t, err)

	// Attempt to freeze an already frozen account
	_, err = frozenAccount.Freeze("Second freeze attempt reason")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestFreeze_InvalidFromClosed(t *testing.T) {
	// Build a closed account
	zeroMoney, _ := NewMoney("GBP", 0)
	closedAccount := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		Build()

	_, err := closedAccount.Freeze("Attempting to freeze a closed account")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestFreeze_ReasonTooShort(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	tests := []struct {
		name   string
		reason string
	}{
		{"empty reason", ""},
		{"single char", "a"},
		{"9 chars", "123456789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := account.Freeze(tt.reason)

			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidFreezeReason)
		})
	}
}

func TestFreeze_ReasonExactly10Chars(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Exactly 10 characters should succeed
	frozenAccount, err := account.Freeze("1234567890")

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusFrozen, frozenAccount.Status())
}

func TestUnfreeze_ValidTransition(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	unfrozenAccount, err := frozenAccount.Unfreeze()

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusActive, unfrozenAccount.Status())
	assert.Empty(t, unfrozenAccount.FreezeReason()) // Freeze reason should be cleared

	// Verify status history records both transitions
	history := unfrozenAccount.StatusHistory()
	require.Len(t, history, 2)
	assert.Equal(t, AccountStatusActive, history[0].From)
	assert.Equal(t, AccountStatusFrozen, history[0].To)
	assert.Equal(t, AccountStatusFrozen, history[1].From)
	assert.Equal(t, AccountStatusActive, history[1].To)
}

func TestUnfreeze_InvalidFromActive(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	_, err = account.Unfreeze()

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestUnfreeze_InvalidFromClosed(t *testing.T) {
	zeroMoney, _ := NewMoney("GBP", 0)
	closedAccount := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		Build()

	_, err := closedAccount.Unfreeze()

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestClose_ValidTransition_ZeroBalance(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	closedAccount, err := account.Close("Customer requested account closure")

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closedAccount.Status())

	// Verify status history is recorded with custom reason
	history := closedAccount.StatusHistory()
	require.Len(t, history, 1)
	assert.Equal(t, AccountStatusActive, history[0].From)
	assert.Equal(t, AccountStatusClosed, history[0].To)
	assert.Equal(t, "Customer requested account closure", history[0].Reason)
}

func TestClose_ValidTransition_FromFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	closedAccount, err := frozenAccount.Close("Fraud confirmed, closing account")

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closedAccount.Status())

	// Verify status history records both transitions
	history := closedAccount.StatusHistory()
	require.Len(t, history, 2)
}

func TestClose_DefaultReason_WhenEmpty(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Close with empty reason should use default
	closedAccount, err := account.Close("")

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closedAccount.Status())

	// Verify status history uses default reason
	history := closedAccount.StatusHistory()
	require.Len(t, history, 1)
	assert.Equal(t, "Account closed", history[0].Reason)
}

func TestClose_InvalidWithNonZeroBalance(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		Build()

	_, err := account.Close("")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNonZeroBalance)
}

func TestClose_InvalidWithNegativeBalance(t *testing.T) {
	balance, _ := NewMoney("GBP", -5000) // Overdrawn account
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		Build()

	_, err := account.Close("")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNonZeroBalance)
}

func TestClose_InvalidFromClosed(t *testing.T) {
	zeroMoney, _ := NewMoney("GBP", 0)
	closedAccount := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		Build()

	_, err := closedAccount.Close("")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestClose_TerminalState(t *testing.T) {
	// Test that CLOSED is truly terminal - no transitions allowed
	zeroMoney, _ := NewMoney("GBP", 0)
	closedAccount := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(zeroMoney).
		WithAvailableBalance(zeroMoney).
		WithStatus(AccountStatusClosed).
		Build()

	t.Run("cannot freeze", func(t *testing.T) {
		_, err := closedAccount.Freeze("Attempting to freeze closed account")
		assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	})

	t.Run("cannot unfreeze", func(t *testing.T) {
		_, err := closedAccount.Unfreeze()
		assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	})

	t.Run("cannot activate", func(t *testing.T) {
		_, err := closedAccount.Activate()
		assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	})

	t.Run("cannot close again", func(t *testing.T) {
		_, err := closedAccount.Close("")
		assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	})
}

func TestStatusHistory_Immutability(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	// Get a copy of the history
	history := frozenAccount.StatusHistory()
	originalLen := len(history)

	// Modify the returned slice (use _ to satisfy ineffassign linter)
	_ = append(history, StatusChange{From: AccountStatusActive, To: AccountStatusClosed})

	// The original should be unchanged
	assert.Len(t, frozenAccount.StatusHistory(), originalLen)
}

func TestStatusHistory_MultipleTransitions(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Perform multiple state transitions
	frozen1, err := account.Freeze("First freeze - suspicious activity")
	require.NoError(t, err)

	unfrozen1, err := frozen1.Unfreeze()
	require.NoError(t, err)

	frozen2, err := unfrozen1.Freeze("Second freeze - fraud detected")
	require.NoError(t, err)

	closed, err := frozen2.Close("Final closure after fraud investigation")
	require.NoError(t, err)

	// Verify full audit trail
	history := closed.StatusHistory()
	require.Len(t, history, 4)

	assert.Equal(t, AccountStatusActive, history[0].From)
	assert.Equal(t, AccountStatusFrozen, history[0].To)
	assert.Equal(t, "First freeze - suspicious activity", history[0].Reason)

	assert.Equal(t, AccountStatusFrozen, history[1].From)
	assert.Equal(t, AccountStatusActive, history[1].To)

	assert.Equal(t, AccountStatusActive, history[2].From)
	assert.Equal(t, AccountStatusFrozen, history[2].To)
	assert.Equal(t, "Second freeze - fraud detected", history[2].Reason)

	assert.Equal(t, AccountStatusFrozen, history[3].From)
	assert.Equal(t, AccountStatusClosed, history[3].To)
}

func TestActivate_DelegatesForFrozen(t *testing.T) {
	// Test that Activate() delegates to Unfreeze() for frozen accounts
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	frozenAccount, err := account.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)

	activatedAccount, err := frozenAccount.Activate()

	assert.NoError(t, err)
	assert.Equal(t, AccountStatusActive, activatedAccount.Status())
	assert.Empty(t, activatedAccount.FreezeReason()) // Should be cleared like Unfreeze()
}

func TestActivate_IdempotentForActive(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Activating an active account should return the same account
	activatedAccount, err := account.Activate()

	assert.NoError(t, err)
	assert.Equal(t, account.Version(), activatedAccount.Version()) // No version bump
}

func TestBuilderWithFreezeReason(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusFrozen).
		WithFreezeReason("Fraud investigation in progress").
		Build()

	assert.Equal(t, AccountStatusFrozen, account.Status())
	assert.Equal(t, "Fraud investigation in progress", account.FreezeReason())
}

func TestBuilderWithStatusHistory(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	now := time.Now()
	history := []StatusChange{
		{From: AccountStatusActive, To: AccountStatusFrozen, Reason: "Initial freeze", Timestamp: now},
	}

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusFrozen).
		WithStatusHistory(history).
		Build()

	assert.Len(t, account.StatusHistory(), 1)
	assert.Equal(t, "Initial freeze", account.StatusHistory()[0].Reason)
}

// Org-scoped account tests

func TestNewCurrentAccount_WithOrgPartyID(t *testing.T) {
	orgPartyID := uuid.New()
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP",
		WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	assert.True(t, account.IsScopedToOrganization())
	require.NotNil(t, account.OrgPartyID())
	assert.Equal(t, orgPartyID, *account.OrgPartyID())
	assert.Equal(t, "PARTY-001", account.PartyID())
}

func TestNewCurrentAccount_WithoutOrgPartyID(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	assert.False(t, account.IsScopedToOrganization())
	assert.Nil(t, account.OrgPartyID())
}

func TestNewCurrentAccount_OrgScopedWithoutPartyID_ReturnsError(t *testing.T) {
	orgPartyID := uuid.New()
	_, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "", "GBP",
		WithOrgPartyID(orgPartyID))

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrOrgScopedWithoutParty)
}

func TestOrgPartyID_PreservedAcrossOperations(t *testing.T) {
	orgPartyID := uuid.New()
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP",
		WithOrgPartyID(orgPartyID))
	require.NoError(t, err)

	// Deposit
	deposit, _ := NewMoney("GBP", 10000)
	afterDeposit, err := account.Deposit(deposit)
	require.NoError(t, err)
	require.NotNil(t, afterDeposit.OrgPartyID())
	assert.Equal(t, orgPartyID, *afterDeposit.OrgPartyID())

	// Withdraw
	withdrawal, _ := NewMoney("GBP", 5000)
	afterWithdraw, err := afterDeposit.Withdraw(withdrawal)
	require.NoError(t, err)
	require.NotNil(t, afterWithdraw.OrgPartyID())
	assert.Equal(t, orgPartyID, *afterWithdraw.OrgPartyID())

	// Freeze
	frozen, err := afterWithdraw.Freeze("Suspicious activity detected on account")
	require.NoError(t, err)
	require.NotNil(t, frozen.OrgPartyID())
	assert.Equal(t, orgPartyID, *frozen.OrgPartyID())

	// Unfreeze
	unfrozen, err := frozen.Unfreeze()
	require.NoError(t, err)
	require.NotNil(t, unfrozen.OrgPartyID())
	assert.Equal(t, orgPartyID, *unfrozen.OrgPartyID())

	// PrepareForCredit
	prepared, err := unfrozen.PrepareForCredit()
	require.NoError(t, err)
	require.NotNil(t, prepared.OrgPartyID())
	assert.Equal(t, orgPartyID, *prepared.OrgPartyID())
}

func TestBuilder_WithOrgPartyID(t *testing.T) {
	orgPartyID := uuid.New()
	balance, _ := NewMoney("GBP", 10000)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("party-123").
		WithOrgPartyID(&orgPartyID).
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	assert.True(t, account.IsScopedToOrganization())
	require.NotNil(t, account.OrgPartyID())
	assert.Equal(t, orgPartyID, *account.OrgPartyID())
}

func TestBuilder_WithNilOrgPartyID(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithExternalIdentifier("GB82WEST12345698765432").
		WithPartyID("party-123").
		WithOrgPartyID(nil).
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	assert.False(t, account.IsScopedToOrganization())
	assert.Nil(t, account.OrgPartyID())
}

// Precision resolution tests

func TestNewCurrentAccountWithDimension_CURRENCY_CorrectPrecision_Succeeds(t *testing.T) {
	tests := []struct {
		currency  string
		precision int
	}{
		{"GBP", 2},
		{"USD", 2},
		{"EUR", 2},
		{"JPY", 0},
		{"CHF", 2},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			account, err := NewCurrentAccountWithDimension("ACC-001", "IDENT-001", "PARTY-001", tt.currency, "CURRENCY", tt.precision)
			require.NoError(t, err)
			assert.Equal(t, tt.currency, account.InstrumentCode())
			assert.Equal(t, "CURRENCY", account.Dimension())
		})
	}
}

func TestNewCurrentAccountWithDimension_CURRENCY_PrecisionMismatch_ReturnsError(t *testing.T) {
	tests := []struct {
		name      string
		currency  string
		precision int
	}{
		{"GBP with precision 3 (expected 2)", "GBP", 3},
		{"JPY with precision 2 (expected 0)", "JPY", 2},
		{"USD with precision 0 (expected 2)", "USD", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCurrentAccountWithDimension("ACC-001", "IDENT-001", "PARTY-001", tt.currency, "CURRENCY", tt.precision)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrPrecisionMismatch)
		})
	}
}

func TestNewCurrentAccount_DerivesCorrectPrecision(t *testing.T) {
	// NewCurrentAccount derives precision from the currency registry automatically.
	tests := []struct {
		currency string
	}{
		{"GBP"},
		{"JPY"},
		{"USD"},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			account, err := NewCurrentAccount("ACC-001", "IDENT-001", "PARTY-001", tt.currency)
			require.NoError(t, err)
			assert.Equal(t, tt.currency, account.InstrumentCode())
			assert.Equal(t, "CURRENCY", account.Dimension())
		})
	}
}

func TestNewCurrentAccount_InvalidCurrency_StillReturnsError(t *testing.T) {
	_, err := NewCurrentAccount("ACC-001", "IDENT-001", "PARTY-001", "INVALID")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCurrency)
}
