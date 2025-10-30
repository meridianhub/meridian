package domain

import (
	"testing"
)

func TestNewCurrentAccount(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")

	if account.AccountID != "ACC-001" {
		t.Errorf("Expected account ID ACC-001, got %s", account.AccountID)
	}

	if account.Balance.AmountCents != 0 {
		t.Errorf("Expected initial balance 0, got %d", account.Balance.AmountCents)
	}

	if account.Status != AccountStatusActive {
		t.Errorf("Expected status ACTIVE, got %s", account.Status)
	}
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
			account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
			account.Balance.AmountCents = tt.initialBal
			account.AvailableBalance.AmountCents = tt.initialBal

			err := account.Deposit(Money{AmountCents: tt.depositAmt, Currency: "GBP"})

			if (err != nil) != tt.wantErr {
				t.Errorf("Deposit() error = %v, wantErr %v", err, tt.wantErr)
			}

			if account.Balance.AmountCents != tt.wantBalance {
				t.Errorf("Balance = %d, want %d", account.Balance.AmountCents, tt.wantBalance)
			}
		})
	}
}

func TestDepositWhenFrozen(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	_ = account.Freeze()

	err := account.Deposit(Money{AmountCents: 1000, Currency: "GBP"})

	if err != ErrAccountFrozen {
		t.Errorf("Expected ErrAccountFrozen, got %v", err)
	}
}

func TestDepositWhenClosed(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	_ = account.Close()

	err := account.Deposit(Money{AmountCents: 1000, Currency: "GBP"})

	if err != ErrAccountClosed {
		t.Errorf("Expected ErrAccountClosed, got %v", err)
	}
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
			account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
			account.Balance.AmountCents = tt.initialBal
			account.AvailableBalance.AmountCents = tt.initialBal

			err := account.Withdraw(Money{AmountCents: tt.withdrawAmt, Currency: "GBP"})

			if (err != nil) != tt.wantErr {
				t.Errorf("Withdraw() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.expectedError != nil && err != tt.expectedError {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}

			if account.Balance.AmountCents != tt.wantBalance {
				t.Errorf("Balance = %d, want %d", account.Balance.AmountCents, tt.wantBalance)
			}
		})
	}
}

func TestWithdrawWithOverdraft(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	account.Balance.AmountCents = 1000

	// Set overdraft limit of £500
	_ = account.SetOverdraftLimit(Money{AmountCents: 500, Currency: "GBP"}, 19.9, true)

	// Should be able to withdraw £1200 (balance + overdraft)
	err := account.Withdraw(Money{AmountCents: 1200, Currency: "GBP"})
	if err != nil {
		t.Errorf("Expected successful withdrawal with overdraft, got error: %v", err)
	}

	if account.Balance.AmountCents != -200 {
		t.Errorf("Expected balance -200, got %d", account.Balance.AmountCents)
	}
}

func TestStatusTransitions(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")

	// Active -> Frozen
	err := account.Freeze()
	if err != nil {
		t.Errorf("Freeze() failed: %v", err)
	}
	if account.Status != AccountStatusFrozen {
		t.Errorf("Expected status FROZEN, got %s", account.Status)
	}

	// Frozen -> Active
	err = account.Activate()
	if err != nil {
		t.Errorf("Activate() failed: %v", err)
	}
	if account.Status != AccountStatusActive {
		t.Errorf("Expected status ACTIVE, got %s", account.Status)
	}

	// Active -> Closed
	err = account.Close()
	if err != nil {
		t.Errorf("Close() failed: %v", err)
	}
	if account.Status != AccountStatusClosed {
		t.Errorf("Expected status CLOSED, got %s", account.Status)
	}

	// Closed -> Active (should fail)
	err = account.Activate()
	if err != ErrInvalidStatusTransition {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}
}

func TestSetOverdraftLimit(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	account.Balance.AmountCents = 1000

	err := account.SetOverdraftLimit(Money{AmountCents: 500, Currency: "GBP"}, 19.9, true)
	if err != nil {
		t.Errorf("SetOverdraftLimit() failed: %v", err)
	}

	if account.OverdraftLimit.AmountCents != 500 {
		t.Errorf("Expected overdraft limit 500, got %d", account.OverdraftLimit.AmountCents)
	}

	if account.OverdraftRate != 19.9 {
		t.Errorf("Expected overdraft rate 19.9, got %f", account.OverdraftRate)
	}

	if !account.OverdraftEnabled {
		t.Error("Expected overdraft enabled")
	}

	if account.AvailableBalance.AmountCents != 1500 {
		t.Errorf("Expected available balance 1500, got %d", account.AvailableBalance.AmountCents)
	}
}

func TestCurrencyMismatch(t *testing.T) {
	account := NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")

	err := account.Deposit(Money{AmountCents: 1000, Currency: "USD"})

	if err == nil {
		t.Error("Expected currency mismatch error")
	}
}
