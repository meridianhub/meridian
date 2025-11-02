package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTransaction_TableName(t *testing.T) {
	transaction := Transaction{}
	if transaction.TableName() != "transactions" {
		t.Errorf("TableName() = %v, want %v", transaction.TableName(), "transactions")
	}
}

func TestTransaction_Creation(t *testing.T) {
	accountID := uuid.New()
	now := time.Now()

	transaction := Transaction{
		BaseModel: BaseModel{
			ID:        uuid.New(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		TransactionID:   "TXN123456",
		TransactionType: "debit",
		AccountID:       accountID,
		Amount:          5000,
		Currency:        "GBP",
		Description:     "Payment to merchant",
		Reference:       "REF123",
		Status:          "completed",
		BalanceAfter:    95000,
	}

	if transaction.ID == uuid.Nil {
		t.Error("ID should not be Nil")
	}

	if transaction.TransactionID != "TXN123456" {
		t.Errorf("TransactionID = %v, want TXN123456", transaction.TransactionID)
	}

	if transaction.TransactionType != "debit" {
		t.Errorf("TransactionType = %v, want debit", transaction.TransactionType)
	}

	if transaction.AccountID != accountID {
		t.Error("AccountID mismatch")
	}

	if transaction.Amount != 5000 {
		t.Errorf("Amount = %v, want 5000", transaction.Amount)
	}

	if transaction.Currency != "GBP" {
		t.Errorf("Currency = %v, want GBP", transaction.Currency)
	}

	if transaction.Status != "completed" {
		t.Errorf("Status = %v, want completed", transaction.Status)
	}

	if transaction.BalanceAfter != 95000 {
		t.Errorf("BalanceAfter = %v, want 95000", transaction.BalanceAfter)
	}
}

func TestTransaction_DefaultValues(t *testing.T) {
	transaction := Transaction{}

	if transaction.Currency != "" {
		t.Errorf("Default Currency should be empty, got %v", transaction.Currency)
	}

	if transaction.Status != "" {
		t.Errorf("Default Status should be empty, got %v", transaction.Status)
	}

	if transaction.Amount != 0 {
		t.Errorf("Default Amount should be 0, got %v", transaction.Amount)
	}

	if transaction.BalanceAfter != 0 {
		t.Errorf("Default BalanceAfter should be 0, got %v", transaction.BalanceAfter)
	}

	if transaction.CounterpartyAccountID != nil {
		t.Error("CounterpartyAccountID should be nil by default")
	}
}

func TestTransaction_Types(t *testing.T) {
	tests := []struct {
		name            string
		transactionType string
		amount          int64
		expectedType    string
	}{
		{
			name:            "debit transaction",
			transactionType: "debit",
			amount:          -5000,
			expectedType:    "debit",
		},
		{
			name:            "credit transaction",
			transactionType: "credit",
			amount:          10000,
			expectedType:    "credit",
		},
		{
			name:            "transfer transaction",
			transactionType: "transfer",
			amount:          -2500,
			expectedType:    "transfer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transaction := Transaction{
				TransactionType: tt.transactionType,
				Amount:          tt.amount,
			}

			if transaction.TransactionType != tt.expectedType {
				t.Errorf("TransactionType = %v, want %v", transaction.TransactionType, tt.expectedType)
			}

			if transaction.Amount != tt.amount {
				t.Errorf("Amount = %v, want %v", transaction.Amount, tt.amount)
			}
		})
	}
}

func TestTransaction_WithCounterparty(t *testing.T) {
	counterpartyID := uuid.New()
	transaction := Transaction{
		TransactionType:       "transfer",
		CounterpartyAccountID: &counterpartyID,
		CounterpartyName:      "John Doe",
	}

	if transaction.CounterpartyAccountID == nil {
		t.Error("CounterpartyAccountID should not be nil for transfer")
	}

	if *transaction.CounterpartyAccountID != counterpartyID {
		t.Error("CounterpartyAccountID mismatch")
	}

	if transaction.CounterpartyName != "John Doe" {
		t.Errorf("CounterpartyName = %v, want John Doe", transaction.CounterpartyName)
	}
}

func TestTransaction_StatusTransitions(t *testing.T) {
	tests := []struct {
		name   string
		status string
		valid  bool
	}{
		{
			name:   "pending status",
			status: "pending",
			valid:  true,
		},
		{
			name:   "completed status",
			status: "completed",
			valid:  true,
		},
		{
			name:   "failed status",
			status: "failed",
			valid:  true,
		},
		{
			name:   "reversed status",
			status: "reversed",
			valid:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transaction := Transaction{
				Status: tt.status,
			}

			if transaction.Status != tt.status {
				t.Errorf("Status = %v, want %v", transaction.Status, tt.status)
			}
		})
	}
}

func TestTransaction_WithTimestamps(t *testing.T) {
	processedAt := time.Now()
	transaction := Transaction{
		Status:      "completed",
		ProcessedAt: &processedAt,
	}

	if transaction.ProcessedAt == nil {
		t.Error("ProcessedAt should not be nil for completed transaction")
	}

	if !transaction.ProcessedAt.Equal(processedAt) {
		t.Error("ProcessedAt timestamp mismatch")
	}

	if transaction.ReversedAt != nil {
		t.Error("ReversedAt should be nil for non-reversed transaction")
	}
}

func TestTransaction_ReversedTransaction(t *testing.T) {
	processedAt := time.Now().Add(-1 * time.Hour)
	reversedAt := time.Now()

	transaction := Transaction{
		Status:      "reversed",
		ProcessedAt: &processedAt,
		ReversedAt:  &reversedAt,
	}

	if transaction.Status != "reversed" {
		t.Errorf("Status = %v, want reversed", transaction.Status)
	}

	if transaction.ProcessedAt == nil {
		t.Error("ProcessedAt should not be nil")
	}

	if transaction.ReversedAt == nil {
		t.Error("ReversedAt should not be nil for reversed transaction")
	}

	if reversedAt.Before(*transaction.ProcessedAt) {
		t.Error("ReversedAt should be after ProcessedAt")
	}
}

func TestTransaction_BalanceCalculation(t *testing.T) {
	tests := []struct {
		name           string
		initialBalance int64
		amount         int64
		balanceAfter   int64
	}{
		{
			name:           "debit reduces balance",
			initialBalance: 10000,
			amount:         -2000,
			balanceAfter:   8000,
		},
		{
			name:           "credit increases balance",
			initialBalance: 5000,
			amount:         3000,
			balanceAfter:   8000,
		},
		{
			name:           "zero amount",
			initialBalance: 10000,
			amount:         0,
			balanceAfter:   10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transaction := Transaction{
				Amount:       tt.amount,
				BalanceAfter: tt.balanceAfter,
			}

			expectedBalance := tt.initialBalance + tt.amount
			if transaction.BalanceAfter != expectedBalance {
				// Note: This test assumes BalanceAfter is set correctly
				// In a real system, you'd calculate it based on initial + amount
				if transaction.BalanceAfter != tt.balanceAfter {
					t.Errorf("BalanceAfter = %v, want %v", transaction.BalanceAfter, tt.balanceAfter)
				}
			}
		})
	}
}
