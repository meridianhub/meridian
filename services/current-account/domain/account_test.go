package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCurrentAccount(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	assert.Equal(t, "ACC-001", account.AccountID)
	assert.Equal(t, int64(0), account.Balance.AmountCents())
	assert.Equal(t, "GBP", account.Balance.Currency())
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
			account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	_ = account.Freeze()

	depositMoney, _ := NewMoney("GBP", 1000)
	err = account.Deposit(depositMoney)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountFrozen)
}

func TestDepositWhenClosed(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
			account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
			_, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", tt.currency)

			if tt.wantErr {
				assert.Error(t, err, tt.rationale)
				assert.ErrorIs(t, err, ErrInvalidCurrency, "Should return ErrInvalidCurrency")
			} else {
				assert.NoError(t, err, tt.rationale)
			}
		})
	}
}

// Defensive test per ADR-008: Overflow prevention in SetOverdraftLimit

func TestSetOverdraftLimit_OverflowPrevention(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	// Set balance near max int64
	largeBalance, err := NewMoney("GBP", 9223372036854775000) // Near MaxInt64
	require.NoError(t, err)
	account.Balance = largeBalance

	// Try to set overdraft that would cause overflow
	largeLimit, err := NewMoney("GBP", 1000)
	require.NoError(t, err)

	err = account.SetOverdraftLimit(largeLimit, 0.1, true)

	assert.Error(t, err, "SetOverdraftLimit should reject overdraft that causes overflow")
	assert.ErrorIs(t, err, ErrAmountOverflow, "Should return ErrAmountOverflow")
}

func TestSetOverdraftLimit_DisabledDoesNotCheckOverflow(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	// Set balance near max int64
	largeBalance, err := NewMoney("GBP", 9223372036854775000)
	require.NoError(t, err)
	account.Balance = largeBalance

	// Set overdraft disabled - should succeed even if adding would overflow
	largeLimit, err := NewMoney("GBP", 1000)
	require.NoError(t, err)

	err = account.SetOverdraftLimit(largeLimit, 0.1, false)

	assert.NoError(t, err, "SetOverdraftLimit with enabled=false should not check overflow")
}

// Nice-to-have tests: Overflow in Deposit/Withdraw operations

func TestDeposit_Overflow_ReturnsError(t *testing.T) {
	account, err := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)

	// Set balance near max
	largeBalance, err := NewMoney("GBP", 9223372036854775000)
	require.NoError(t, err)
	account.Balance = largeBalance

	// Try to deposit amount that causes overflow
	deposit, err := NewMoney("GBP", 1000)
	require.NoError(t, err)

	err = account.Deposit(deposit)

	assert.Error(t, err, "Deposit causing overflow should fail")
	assert.ErrorIs(t, err, ErrAmountOverflow)
}

// Note: Withdraw underflow is already covered by Money.Subtract tests
// This test would be complex to set up due to available balance checks
