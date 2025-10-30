package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestNewLedgerPosting(t *testing.T) {
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	zeroMoney, _ := NewMoney(decimal.Zero, CurrencyGBP)
	negativeMoney, _ := NewMoney(decimal.NewFromInt(-100), CurrencyGBP)

	tests := []struct {
		name          string
		bookingLogID  uuid.UUID
		direction     PostingDirection
		amount        Money
		accountID     string
		valueDate     time.Time
		correlationID string
		wantErr       bool
		expectedErr   error
	}{
		{
			name:          "valid posting",
			bookingLogID:  uuid.New(),
			direction:     PostingDirectionDebit,
			amount:        validMoney,
			accountID:     "ACC-001",
			valueDate:     time.Now(),
			correlationID: "corr-123",
			wantErr:       false,
		},
		{
			name:          "nil booking log ID",
			bookingLogID:  uuid.Nil,
			direction:     PostingDirectionDebit,
			amount:        validMoney,
			accountID:     "ACC-001",
			valueDate:     time.Now(),
			correlationID: "corr-123",
			wantErr:       true,
			expectedErr:   ErrInvalidBookingLogID,
		},
		{
			name:          "zero amount",
			bookingLogID:  uuid.New(),
			direction:     PostingDirectionDebit,
			amount:        zeroMoney,
			accountID:     "ACC-001",
			valueDate:     time.Now(),
			correlationID: "corr-123",
			wantErr:       true,
			expectedErr:   ErrInvalidPostingAmount,
		},
		{
			name:          "negative amount",
			bookingLogID:  uuid.New(),
			direction:     PostingDirectionDebit,
			amount:        negativeMoney,
			accountID:     "ACC-001",
			valueDate:     time.Now(),
			correlationID: "corr-123",
			wantErr:       true,
			expectedErr:   ErrInvalidPostingAmount,
		},
		{
			name:          "empty account ID",
			bookingLogID:  uuid.New(),
			direction:     PostingDirectionDebit,
			amount:        validMoney,
			accountID:     "",
			valueDate:     time.Now(),
			correlationID: "corr-123",
			wantErr:       true,
			expectedErr:   ErrInvalidAccountID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posting, err := NewLedgerPosting(
				tt.bookingLogID,
				tt.direction,
				tt.amount,
				tt.accountID,
				tt.valueDate,
				tt.correlationID,
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

			if posting.Status != TransactionStatusPending {
				t.Errorf("Expected status PENDING, got %v", posting.Status)
			}

			if posting.ID == uuid.Nil {
				t.Error("Expected non-nil posting ID")
			}
		})
	}
}

func TestLedgerPosting_Post(t *testing.T) {
	money, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	posting, _ := NewLedgerPosting(
		uuid.New(),
		PostingDirectionDebit,
		money,
		"ACC-001",
		time.Now(),
		"corr-123",
	)

	err := posting.Post("Successfully posted")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if posting.Status != TransactionStatusPosted {
		t.Errorf("Expected status POSTED, got %v", posting.Status)
	}

	if posting.PostingResult != "Successfully posted" {
		t.Errorf("Expected result 'Successfully posted', got %v", posting.PostingResult)
	}

	// Try to post again
	err = posting.Post("Second attempt")
	if err == nil {
		t.Error("Expected error when posting already posted transaction")
	}
}

func TestLedgerPosting_Fail(t *testing.T) {
	money, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	posting, _ := NewLedgerPosting(
		uuid.New(),
		PostingDirectionDebit,
		money,
		"ACC-001",
		time.Now(),
		"corr-123",
	)

	err := posting.Fail("Validation failed")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if posting.Status != TransactionStatusFailed {
		t.Errorf("Expected status FAILED, got %v", posting.Status)
	}

	// Test cannot fail already posted
	money2, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	posting2, _ := NewLedgerPosting(
		uuid.New(),
		PostingDirectionCredit,
		money2,
		"ACC-002",
		time.Now(),
		"corr-124",
	)
	_ = posting2.Post("Posted successfully")

	err = posting2.Fail("Attempt to fail")
	if err == nil {
		t.Error("Expected error when failing posted transaction")
	}
}

func TestLedgerPosting_IsPosted(t *testing.T) {
	money, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	posting, _ := NewLedgerPosting(
		uuid.New(),
		PostingDirectionDebit,
		money,
		"ACC-001",
		time.Now(),
		"corr-123",
	)

	if posting.IsPosted() {
		t.Error("Expected IsPosted to be false for pending posting")
	}

	_ = posting.Post("Posted")

	if !posting.IsPosted() {
		t.Error("Expected IsPosted to be true for posted posting")
	}
}
