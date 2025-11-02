package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccount_TableName(t *testing.T) {
	account := Account{}
	if account.TableName() != "accounts" {
		t.Errorf("TableName() = %v, want %v", account.TableName(), "accounts")
	}
}

func TestAccount_Creation(t *testing.T) {
	customerID := uuid.New()
	now := time.Now()

	account := Account{
		BaseModel: BaseModel{
			ID:        uuid.New(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		AccountNumber:    "GB29NWBK60161331926819",
		AccountType:      "current",
		Currency:         "GBP",
		Status:           "active",
		CustomerID:       customerID,
		Balance:          10000,
		AvailableBalance: 8000,
		OverdraftLimit:   2000,
	}

	if account.ID == uuid.Nil {
		t.Error("ID should not be Nil")
	}

	if account.AccountNumber != "GB29NWBK60161331926819" {
		t.Errorf("AccountNumber = %v, want GB29NWBK60161331926819", account.AccountNumber)
	}

	if account.AccountType != "current" {
		t.Errorf("AccountType = %v, want current", account.AccountType)
	}

	if account.Currency != "GBP" {
		t.Errorf("Currency = %v, want GBP", account.Currency)
	}

	if account.Status != "active" {
		t.Errorf("Status = %v, want active", account.Status)
	}

	if account.CustomerID != customerID {
		t.Error("CustomerID mismatch")
	}

	if account.Balance != 10000 {
		t.Errorf("Balance = %v, want 10000", account.Balance)
	}

	if account.AvailableBalance != 8000 {
		t.Errorf("AvailableBalance = %v, want 8000", account.AvailableBalance)
	}

	if account.OverdraftLimit != 2000 {
		t.Errorf("OverdraftLimit = %v, want 2000", account.OverdraftLimit)
	}
}

func TestAccount_DefaultValues(t *testing.T) {
	account := Account{}

	// Test default zero values
	if account.Currency != "" {
		t.Errorf("Default Currency should be empty, got %v", account.Currency)
	}

	if account.Status != "" {
		t.Errorf("Default Status should be empty, got %v", account.Status)
	}

	if account.Balance != 0 {
		t.Errorf("Default Balance should be 0, got %v", account.Balance)
	}

	if account.AvailableBalance != 0 {
		t.Errorf("Default AvailableBalance should be 0, got %v", account.AvailableBalance)
	}

	if account.OverdraftLimit != 0 {
		t.Errorf("Default OverdraftLimit should be 0, got %v", account.OverdraftLimit)
	}
}

func TestAccount_WithTimestamps(t *testing.T) {
	openedAt := time.Now().Add(-24 * time.Hour)
	account := Account{
		OpenedAt: &openedAt,
	}

	if account.OpenedAt == nil {
		t.Error("OpenedAt should not be nil")
	}

	if !account.OpenedAt.Equal(openedAt) {
		t.Error("OpenedAt timestamp mismatch")
	}

	if account.ClosedAt != nil {
		t.Error("ClosedAt should be nil for active account")
	}
}

func TestAccount_ClosedAccount(t *testing.T) {
	openedAt := time.Now().Add(-30 * 24 * time.Hour)
	closedAt := time.Now()

	account := Account{
		Status:   "closed",
		OpenedAt: &openedAt,
		ClosedAt: &closedAt,
	}

	if account.Status != "closed" {
		t.Errorf("Status = %v, want closed", account.Status)
	}

	if account.OpenedAt == nil {
		t.Error("OpenedAt should not be nil")
	}

	if account.ClosedAt == nil {
		t.Error("ClosedAt should not be nil for closed account")
	}

	if closedAt.Before(*account.OpenedAt) {
		t.Error("ClosedAt should be after OpenedAt")
	}
}

func TestAccount_BalanceCalculations(t *testing.T) {
	tests := []struct {
		name             string
		balance          int64
		overdraftLimit   int64
		availableBalance int64
		valid            bool
	}{
		{
			name:             "positive balance",
			balance:          10000,
			overdraftLimit:   0,
			availableBalance: 10000,
			valid:            true,
		},
		{
			name:             "with overdraft",
			balance:          5000,
			overdraftLimit:   2000,
			availableBalance: 7000,
			valid:            true,
		},
		{
			name:             "zero balance with overdraft",
			balance:          0,
			overdraftLimit:   1000,
			availableBalance: 1000,
			valid:            true,
		},
		{
			name:             "negative balance within overdraft",
			balance:          -500,
			overdraftLimit:   1000,
			availableBalance: 500,
			valid:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := Account{
				Balance:          tt.balance,
				OverdraftLimit:   tt.overdraftLimit,
				AvailableBalance: tt.availableBalance,
			}

			if account.Balance != tt.balance {
				t.Errorf("Balance = %v, want %v", account.Balance, tt.balance)
			}

			if account.OverdraftLimit != tt.overdraftLimit {
				t.Errorf("OverdraftLimit = %v, want %v", account.OverdraftLimit, tt.overdraftLimit)
			}

			if account.AvailableBalance != tt.availableBalance {
				t.Errorf("AvailableBalance = %v, want %v", account.AvailableBalance, tt.availableBalance)
			}
		})
	}
}
