//nolint:staticcheck // Tests intentionally use deprecated AmountCents() to verify backward compatibility
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCurrentAccount(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	assert.Equal(t, "ACC-001", account.AccountID)
	assert.Equal(t, "PARTY-001", account.PartyID)
	assert.Equal(t, int64(0), account.Balance.AmountCents())
	assert.Equal(t, CurrencyGBP, account.Balance.Currency())
	assert.Equal(t, AccountStatusActive, account.Status)
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
			account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
			require.NoError(t, err)
			// Set initial balance using immutable Money
			account.Balance, _ = NewMoney("GBP", tt.initialBal)
			account.AvailableBalance, _ = NewMoney("GBP", tt.initialBal)

			depositMoney, _ := NewMoney("GBP", tt.depositAmt)
			err = account.Deposit(depositMoney)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.wantBalance, account.Balance.AmountCents())
		})
	}
}

func TestDepositWhenFrozen(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	_ = account.Freeze()

	depositMoney, _ := NewMoney("GBP", 1000)
	err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestDepositWhenClosed(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	_ = account.Close()

	depositMoney, _ := NewMoney("GBP", 1000)
	err = account.Deposit(depositMoney)

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
			account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
			require.NoError(t, err)
			account.Balance, _ = NewMoney("GBP", tt.initialBal)
			account.AvailableBalance, _ = NewMoney("GBP", tt.initialBal)

			withdrawMoney, _ := NewMoney("GBP", tt.withdrawAmt)
			err = account.Withdraw(withdrawMoney)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedError != nil {
				assert.ErrorIs(t, err, tt.expectedError)
			}

			assert.Equal(t, tt.wantBalance, account.Balance.AmountCents())
		})
	}
}

func TestWithdrawWithOverdraft(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account.Balance, _ = NewMoney("GBP", 1000)

	// Set overdraft limit of £500
	overdraftLimit, _ := NewMoney("GBP", 500)
	err = account.SetOverdraftLimit(overdraftLimit, 19.9, true)
	assert.NoError(t, err)

	// Should be able to withdraw £1200 (balance + overdraft)
	withdrawMoney, _ := NewMoney("GBP", 1200)
	err = account.Withdraw(withdrawMoney)
	assert.NoError(t, err)

	assert.Equal(t, int64(-200), account.Balance.AmountCents())
}

func TestStatusTransitions(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Active -> Frozen
	err = account.Freeze()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusFrozen, account.Status)

	// Frozen -> Active
	err = account.Activate()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusActive, account.Status)

	// Active -> Closed
	err = account.Close()
	assert.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, account.Status)

	// Closed -> Active (should fail)
	err = account.Activate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestSetOverdraftLimit(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)
	account.Balance, _ = NewMoney("GBP", 1000)

	overdraftLimit, _ := NewMoney("GBP", 500)
	err = account.SetOverdraftLimit(overdraftLimit, 19.9, true)
	assert.NoError(t, err)

	assert.Equal(t, int64(500), account.OverdraftLimit.AmountCents())
	assert.Equal(t, 19.9, account.OverdraftRate)
	assert.True(t, account.OverdraftEnabled)
	assert.Equal(t, int64(1500), account.AvailableBalance.AmountCents())
}

func TestCurrencyMismatch(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	depositMoney, _ := NewMoney("USD", 1000)
	err = account.Deposit(depositMoney)

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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Set a large balance (in minor units - cents/pence)
	largeBalance, err := NewMoney("GBP", 1000000000000) // 10 billion cents = 100 million GBP
	require.NoError(t, err)
	account.Balance = largeBalance
	account.AvailableBalance = largeBalance

	// Set overdraft
	overdraftLimit, err := NewMoney("GBP", 100000000000) // 1 billion cents
	require.NoError(t, err)

	err = account.SetOverdraftLimit(overdraftLimit, 0.1, true)
	assert.NoError(t, err, "Large values should be handled correctly")

	// Available balance should be sum
	assert.Equal(t, int64(1100000000000), account.AvailableBalance.AmountCents())
}

func TestSetOverdraftLimit_DisabledDoesNotAddToAvailable(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Set balance
	balance, err := NewMoney("GBP", 100000)
	require.NoError(t, err)
	account.Balance = balance
	account.AvailableBalance = balance

	// Set overdraft disabled
	overdraftLimit, err := NewMoney("GBP", 50000)
	require.NoError(t, err)

	err = account.SetOverdraftLimit(overdraftLimit, 0.1, false)
	assert.NoError(t, err)

	// Available balance should NOT include overdraft when disabled
	assert.Equal(t, int64(100000), account.AvailableBalance.AmountCents())
}

// Tests for large deposits
func TestDeposit_LargeValues(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "PARTY-001", "GBP")
	require.NoError(t, err)

	// Set initial balance
	balance, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)
	account.Balance = balance
	account.AvailableBalance = balance

	// Large deposit
	deposit, err := NewMoney("GBP", 1000000000000)
	require.NoError(t, err)

	err = account.Deposit(deposit)
	assert.NoError(t, err, "Large deposits should be handled correctly")
	assert.Equal(t, int64(2000000000000), account.Balance.AmountCents())
}
