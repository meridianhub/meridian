//nolint:staticcheck // Tests intentionally use deprecated AmountCents() to verify backward compatibility
package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCurrentAccount(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	assert.Equal(t, "ACC-001", account.AccountID())
	assert.Equal(t, "PARTY-001", account.PartyID())
	assert.Equal(t, int64(0), account.Balance().AmountCents())
	assert.Equal(t, CurrencyGBP, account.Balance().Currency())
	assert.Equal(t, AccountStatusActive, account.Status())
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
				WithAccountIdentification("GB82WEST12345698765432").
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
				assert.Equal(t, tt.initialBal, account.Balance().AmountCents())
			} else {
				assert.NoError(t, err)
				// Updated account should have new balance
				assert.Equal(t, tt.wantBalance, updatedAccount.Balance().AmountCents())
				// Original account should be unchanged (immutability)
				assert.Equal(t, tt.initialBal, account.Balance().AmountCents())
			}
		})
	}
}

func TestDepositWhenFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account, _ = account.Freeze()

	depositMoney, _ := NewMoney("GBP", 1000)
	_, err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestDepositWhenClosed(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account, _ = account.Close()

	depositMoney, _ := NewMoney("GBP", 1000)
	_, err = account.Deposit(depositMoney)

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
				WithAccountIdentification("GB82WEST12345698765432").
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
				assert.Equal(t, tt.initialBal, account.Balance().AmountCents())
			} else {
				assert.NoError(t, err)
				// Updated account should have new balance
				assert.Equal(t, tt.wantBalance, updatedAccount.Balance().AmountCents())
				// Original account should be unchanged (immutability)
				assert.Equal(t, tt.initialBal, account.Balance().AmountCents())
			}

			if tt.expectedError != nil {
				assert.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestWithdrawWithOverdraft(t *testing.T) {
	// Build account with initial balance using builder
	initialBalance, _ := NewMoney("GBP", 1000)
	zeroMoney, _ := NewMoney("GBP", 0)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithOverdraftLimit(zeroMoney).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Set overdraft limit of £500
	overdraftLimit, _ := NewMoney("GBP", 500)
	account, err := account.SetOverdraftLimit(overdraftLimit, 19.9, true)
	assert.NoError(t, err)

	// Should be able to withdraw £1200 (balance + overdraft)
	withdrawMoney, _ := NewMoney("GBP", 1200)
	updatedAccount, err := account.Withdraw(withdrawMoney)
	assert.NoError(t, err)

	assert.Equal(t, int64(-200), updatedAccount.Balance().AmountCents())
}

func TestStatusTransitions(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Active -> Frozen
	account, err = account.Freeze()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusFrozen, account.Status())

	// Frozen -> Active
	account, err = account.Activate()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusActive, account.Status())

	// Active -> Closed
	account, err = account.Close()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, account.Status())

	// Closed -> Active (should fail)
	_, err = account.Activate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestSetOverdraftLimit(t *testing.T) {
	// Build account with initial balance using builder
	initialBalance, _ := NewMoney("GBP", 1000)
	zeroMoney, _ := NewMoney("GBP", 0)
	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(initialBalance).
		WithAvailableBalance(initialBalance).
		WithOverdraftLimit(zeroMoney).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	overdraftLimit, _ := NewMoney("GBP", 500)
	updatedAccount, err := account.SetOverdraftLimit(overdraftLimit, 19.9, true)
	assert.NoError(t, err)

	assert.Equal(t, int64(500), updatedAccount.OverdraftLimit().AmountCents())
	assert.Equal(t, 19.9, updatedAccount.OverdraftRate())
	assert.True(t, updatedAccount.OverdraftEnabled())
	assert.Equal(t, int64(1500), updatedAccount.AvailableBalance().AmountCents())

	// Original account should be unchanged (immutability)
	assert.Equal(t, int64(0), account.OverdraftLimit().AmountCents())
	assert.False(t, account.OverdraftEnabled())
}

func TestCurrencyMismatch(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	depositMoney, _ := NewMoney("USD", 1000)
	_, err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCurrencyMismatch)
}

// Defensive test per ADR-008: Constructor validation

func TestNewCurrentAccount_InvalidCurrency_ReturnsError(t *testing.T) {
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
			rationale: "Empty currency should be rejected at construction",
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
				assert.ErrorIs(t, err, ErrInvalidCurrency, "Should return ErrInvalidCurrency")
			} else {
				assert.NoError(t, err, tt.rationale)
			}
		})
	}
}

// Tests for large values with decimal-based Money implementation
// Note: The new decimal-based Money implementation does not overflow on arithmetic
// operations like int64 did. Overflow is now checked when converting to minor units.

func TestSetOverdraftLimit_LargeValues(t *testing.T) {
	// Build account with large balance using builder
	largeBalance, err := NewMoney("GBP", 1000000000000) // 10 billion cents = 100 million GBP
	require.NoError(t, err)
	zeroMoney, _ := NewMoney("GBP", 0)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(largeBalance).
		WithAvailableBalance(largeBalance).
		WithOverdraftLimit(zeroMoney).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Set overdraft
	overdraftLimit, err := NewMoney("GBP", 100000000000) // 1 billion cents
	require.NoError(t, err)

	updatedAccount, err := account.SetOverdraftLimit(overdraftLimit, 0.1, true)
	assert.NoError(t, err, "Large values should be handled correctly")

	// Available balance should be sum
	assert.Equal(t, int64(1100000000000), updatedAccount.AvailableBalance().AmountCents())
}

func TestSetOverdraftLimit_DisabledDoesNotAddToAvailable(t *testing.T) {
	// Build account with balance using builder
	balance, err := NewMoney("GBP", 100000)
	require.NoError(t, err)
	zeroMoney, _ := NewMoney("GBP", 0)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithOverdraftLimit(zeroMoney).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Set overdraft disabled
	overdraftLimit, err := NewMoney("GBP", 50000)
	require.NoError(t, err)

	updatedAccount, err := account.SetOverdraftLimit(overdraftLimit, 0.1, false)
	assert.NoError(t, err)

	// Available balance should NOT include overdraft when disabled
	assert.Equal(t, int64(100000), updatedAccount.AvailableBalance().AmountCents())
}

// Tests for large deposits
func TestDeposit_LargeValues(t *testing.T) {
	// Build account with large balance using builder
	balance, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)
	zeroMoney, _ := NewMoney("GBP", 0)

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("PARTY-001").
		WithBalance(balance).
		WithAvailableBalance(balance).
		WithOverdraftLimit(zeroMoney).
		WithStatus(AccountStatusActive).
		WithVersion(1).
		Build()

	// Large deposit
	deposit, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)

	updatedAccount, err := account.Deposit(deposit)
	assert.NoError(t, err, "Large deposits should be handled correctly")
	assert.Equal(t, int64(2000000000000), updatedAccount.Balance().AmountCents())
}

// Immutability tests

func TestImmutability_DepositDoesNotModifyOriginal(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	originalBalance := account.Balance().AmountCents()
	originalVersion := account.Version()

	deposit, _ := NewMoney("GBP", 10000)
	_, err = account.Deposit(deposit)
	require.NoError(t, err)

	// Original should be unchanged
	assert.Equal(t, originalBalance, account.Balance().AmountCents())
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

	originalBalance := account.Balance().AmountCents()
	originalVersion := account.Version()

	withdrawal, _ := NewMoney("GBP", 5000)
	_, err := account.Withdraw(withdrawal)
	require.NoError(t, err)

	// Original should be unchanged
	assert.Equal(t, originalBalance, account.Balance().AmountCents())
	assert.Equal(t, originalVersion, account.Version())
}

func TestImmutability_FreezeDoesNotModifyOriginal(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	originalStatus := account.Status()

	_, err = account.Freeze()
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
	assert.Equal(t, int64(0), original.Balance().AmountCents())
	assert.Equal(t, int64(10000), afterDeposit1.Balance().AmountCents())
	assert.Equal(t, int64(15000), afterDeposit2.Balance().AmountCents())
	assert.Equal(t, int64(12000), afterWithdrawal.Balance().AmountCents())

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

	originalBalance := account.Balance().AmountCents()
	originalVersion := account.Version()

	// Attempt an operation that will fail
	excessiveWithdrawal, _ := NewMoney("GBP", 99999)
	_, err := account.Withdraw(excessiveWithdrawal)

	// Verify original is unchanged despite the failed operation
	require.Error(t, err)
	assert.Equal(t, originalBalance, account.Balance().AmountCents())
	assert.Equal(t, originalVersion, account.Version())
}

// Builder tests

func TestCurrentAccountBuilder(t *testing.T) {
	balance, _ := NewMoney("GBP", 10000)
	available, _ := NewMoney("GBP", 15000)
	overdraft, _ := NewMoney("GBP", 5000)
	now := time.Now()

	account := NewCurrentAccountBuilder().
		WithAccountID("ACC-001").
		WithAccountIdentification("GB82WEST12345698765432").
		WithPartyID("party-123").
		WithBalance(balance).
		WithAvailableBalance(available).
		WithStatus(AccountStatusActive).
		WithOverdraftLimit(overdraft).
		WithOverdraftEnabled(true).
		WithOverdraftRate(0.15).
		WithVersion(5).
		WithCreatedAt(now).
		WithUpdatedAt(now).
		WithBalanceUpdatedAt(now).
		Build()

	assert.Equal(t, "ACC-001", account.AccountID())
	assert.Equal(t, "GB82WEST12345698765432", account.AccountIdentification())
	assert.Equal(t, "party-123", account.PartyID())
	assert.Equal(t, int64(10000), account.Balance().AmountCents())
	assert.Equal(t, int64(15000), account.AvailableBalance().AmountCents())
	assert.Equal(t, AccountStatusActive, account.Status())
	assert.Equal(t, int64(5000), account.OverdraftLimit().AmountCents())
	assert.True(t, account.OverdraftEnabled())
	assert.Equal(t, 0.15, account.OverdraftRate())
	assert.Equal(t, int64(5), account.Version())
}

// calculateAvailableBalance tests

func TestCalculateAvailableBalance(t *testing.T) {
	t.Run("without overdraft returns balance", func(t *testing.T) {
		balance, _ := NewMoney("GBP", 10000)
		overdraft, _ := NewMoney("GBP", 5000)

		result := calculateAvailableBalance(balance, overdraft, false)

		assert.Equal(t, balance, result)
	})

	t.Run("with overdraft returns balance plus limit", func(t *testing.T) {
		balance, _ := NewMoney("GBP", 10000)
		overdraft, _ := NewMoney("GBP", 5000)

		result := calculateAvailableBalance(balance, overdraft, true)

		assert.Equal(t, int64(15000), result.AmountCents())
	})

	t.Run("negative balance with overdraft", func(t *testing.T) {
		balance, _ := NewMoney("GBP", -3000)
		overdraft, _ := NewMoney("GBP", 5000)

		result := calculateAvailableBalance(balance, overdraft, true)

		assert.Equal(t, int64(2000), result.AmountCents())
	})
}
