package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestNewTransactionLogEntry(t *testing.T) {
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	zeroMoney, _ := NewMoney(decimal.Zero, CurrencyGBP)
	negativeMoney, _ := NewMoney(decimal.NewFromInt(-100), CurrencyGBP)

	tests := []struct {
		name          string
		transactionID uuid.UUID
		accountID     string
		amount        Money
		direction     PostingDirection
		timestamp     time.Time
		description   string
		reference     string
		source        TransactionSource
		wantErr       bool
		expectedErr   error
	}{
		{
			name:          "valid entry",
			transactionID: uuid.New(),
			accountID:     "ACC-001",
			amount:        validMoney,
			direction:     PostingDirectionDebit,
			timestamp:     time.Now(),
			description:   "Payment received",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       false,
		},
		{
			name:          "nil transaction ID",
			transactionID: uuid.Nil,
			accountID:     "ACC-001",
			amount:        validMoney,
			direction:     PostingDirectionDebit,
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       true,
			expectedErr:   ErrInvalidTransactionID,
		},
		{
			name:          "empty account ID",
			transactionID: uuid.New(),
			accountID:     "",
			amount:        validMoney,
			direction:     PostingDirectionDebit,
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       true,
			expectedErr:   ErrInvalidAccountID,
		},
		{
			name:          "zero amount",
			transactionID: uuid.New(),
			accountID:     "ACC-001",
			amount:        zeroMoney,
			direction:     PostingDirectionDebit,
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       true,
			expectedErr:   ErrInvalidEntryAmount,
		},
		{
			name:          "negative amount",
			transactionID: uuid.New(),
			accountID:     "ACC-001",
			amount:        negativeMoney,
			direction:     PostingDirectionDebit,
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       true,
			expectedErr:   ErrInvalidEntryAmount,
		},
		{
			name:          "invalid posting direction",
			transactionID: uuid.New(),
			accountID:     "ACC-001",
			amount:        validMoney,
			direction:     PostingDirection("INVALID"),
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSourceManual,
			wantErr:       true,
			expectedErr:   ErrInvalidPostingDirection,
		},
		{
			name:          "invalid source defaults to manual",
			transactionID: uuid.New(),
			accountID:     "ACC-001",
			amount:        validMoney,
			direction:     PostingDirectionCredit,
			timestamp:     time.Now(),
			description:   "Payment",
			reference:     "REF-123",
			source:        TransactionSource("INVALID"),
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := NewTransactionLogEntry(
				tt.transactionID,
				tt.accountID,
				tt.amount,
				tt.direction,
				tt.timestamp,
				tt.description,
				tt.reference,
				tt.source,
			)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if entry.EntryID == uuid.Nil {
				t.Error("Expected non-nil entry ID")
			}

			if entry.TransactionID != tt.transactionID {
				t.Errorf("Expected transaction ID %v, got %v", tt.transactionID, entry.TransactionID)
			}

			if entry.AccountID != tt.accountID {
				t.Errorf("Expected account ID %v, got %v", tt.accountID, entry.AccountID)
			}
		})
	}
}
