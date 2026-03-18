package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// testMoneyForBookingLog creates a Money value for use in booking log tests.
func testMoneyForBookingLog(amount int64, currency Currency) Money {
	inst := MustCurrencyToInstrument(currency)
	return NewMoney(decimal.NewFromInt(amount), inst)
}

func TestNewFinancialBookingLog(t *testing.T) {
	tests := []struct {
		name                 string
		accountType          string
		productServiceRef    string
		businessUnitRef      string
		chartOfAccountsRules string
		baseCurrency         Currency
	}{
		{
			name:                 "valid booking log with GBP",
			accountType:          "ASSET",
			productServiceRef:    "PROD-001",
			businessUnitRef:      "BU-TREASURY",
			chartOfAccountsRules: "UK-GAAP-2024",
			baseCurrency:         CurrencyGBP,
		},
		{
			name:                 "valid booking log with USD",
			accountType:          "LIABILITY",
			productServiceRef:    "PROD-002",
			businessUnitRef:      "BU-LENDING",
			chartOfAccountsRules: "US-GAAP-2024",
			baseCurrency:         CurrencyUSD,
		},
		{
			name:                 "valid booking log with empty optional fields",
			accountType:          "",
			productServiceRef:    "",
			businessUnitRef:      "",
			chartOfAccountsRules: "",
			baseCurrency:         CurrencyEUR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeCreate := time.Now().UTC()

			log := NewFinancialBookingLog(
				tt.accountType,
				tt.productServiceRef,
				tt.businessUnitRef,
				tt.chartOfAccountsRules,
				tt.baseCurrency,
			)

			afterCreate := time.Now().UTC()

			if log.ID == uuid.Nil {
				t.Error("Expected non-nil ID")
			}

			if log.FinancialAccountType != tt.accountType {
				t.Errorf("Expected account type %v, got %v", tt.accountType, log.FinancialAccountType)
			}

			if log.ProductServiceReference != tt.productServiceRef {
				t.Errorf("Expected product service ref %v, got %v", tt.productServiceRef, log.ProductServiceReference)
			}

			if log.BusinessUnitReference != tt.businessUnitRef {
				t.Errorf("Expected business unit ref %v, got %v", tt.businessUnitRef, log.BusinessUnitReference)
			}

			if log.ChartOfAccountsRules != tt.chartOfAccountsRules {
				t.Errorf("Expected chart of accounts rules %v, got %v", tt.chartOfAccountsRules, log.ChartOfAccountsRules)
			}

			if log.BaseCurrency != tt.baseCurrency {
				t.Errorf("Expected base currency %v, got %v", tt.baseCurrency, log.BaseCurrency)
			}

			if log.Status != TransactionStatusPending {
				t.Errorf("Expected status PENDING, got %v", log.Status)
			}

			if log.CreatedAt.Before(beforeCreate) || log.CreatedAt.After(afterCreate) {
				t.Errorf("CreatedAt %v should be between %v and %v", log.CreatedAt, beforeCreate, afterCreate)
			}

			if log.UpdatedAt.Before(beforeCreate) || log.UpdatedAt.After(afterCreate) {
				t.Errorf("UpdatedAt %v should be between %v and %v", log.UpdatedAt, beforeCreate, afterCreate)
			}

			if len(log.Postings()) != 0 {
				t.Errorf("Expected empty postings, got %d", len(log.Postings()))
			}
		})
	}
}

func TestFinancialBookingLog_WithStatus(t *testing.T) {
	original := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	tests := []struct {
		name      string
		newStatus TransactionStatus
	}{
		{
			name:      "transition to POSTED",
			newStatus: TransactionStatusPosted,
		},
		{
			name:      "transition to FAILED",
			newStatus: TransactionStatusFailed,
		},
		{
			name:      "transition to CANCELLED",
			newStatus: TransactionStatusCancelled,
		},
		{
			name:      "transition to REVERSED",
			newStatus: TransactionStatusReversed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Intentional sleep: Ensure time has advanced for distinct UpdatedAt timestamps
			time.Sleep(time.Millisecond) //nolint:forbidigo // ensures distinct timestamps
			beforeUpdate := time.Now().UTC()

			updated := original.WithStatus(tt.newStatus)

			afterUpdate := time.Now().UTC()

			// Original should be unchanged (immutability)
			if original.Status != TransactionStatusPending {
				t.Errorf("Original status changed from PENDING to %v", original.Status)
			}

			// New instance should have updated status
			if updated.Status != tt.newStatus {
				t.Errorf("Expected status %v, got %v", tt.newStatus, updated.Status)
			}

			// ID should be preserved
			if updated.ID != original.ID {
				t.Error("ID should be preserved across status update")
			}

			// CreatedAt should be preserved
			if !updated.CreatedAt.Equal(original.CreatedAt) {
				t.Error("CreatedAt should be preserved across status update")
			}

			// UpdatedAt should be updated
			if updated.UpdatedAt.Before(beforeUpdate) || updated.UpdatedAt.After(afterUpdate) {
				t.Errorf("UpdatedAt %v should be between %v and %v", updated.UpdatedAt, beforeUpdate, afterUpdate)
			}

			// Other fields should be preserved
			if updated.FinancialAccountType != original.FinancialAccountType {
				t.Error("FinancialAccountType should be preserved")
			}

			if updated.ProductServiceReference != original.ProductServiceReference {
				t.Error("ProductServiceReference should be preserved")
			}

			if updated.BusinessUnitReference != original.BusinessUnitReference {
				t.Error("BusinessUnitReference should be preserved")
			}

			if updated.ChartOfAccountsRules != original.ChartOfAccountsRules {
				t.Error("ChartOfAccountsRules should be preserved")
			}

			if updated.BaseCurrency != original.BaseCurrency {
				t.Error("BaseCurrency should be preserved")
			}
		})
	}
}

func TestFinancialBookingLog_WithChartOfAccountsRules(t *testing.T) {
	original := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	tests := []struct {
		name     string
		newRules string
	}{
		{
			name:     "update to IFRS rules",
			newRules: "IFRS-2024",
		},
		{
			name:     "update to empty rules",
			newRules: "",
		},
		{
			name:     "update to complex rule string",
			newRules: "UK-GAAP-2024;IFRS-2024;CUSTOM-OVERRIDE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Intentional sleep: Ensure time has advanced for distinct UpdatedAt timestamps
			time.Sleep(time.Millisecond) //nolint:forbidigo // ensures distinct timestamps
			beforeUpdate := time.Now().UTC()

			updated := original.WithChartOfAccountsRules(tt.newRules)

			afterUpdate := time.Now().UTC()

			// Original should be unchanged (immutability)
			if original.ChartOfAccountsRules != "UK-GAAP-2024" {
				t.Errorf("Original rules changed from UK-GAAP-2024 to %v", original.ChartOfAccountsRules)
			}

			// New instance should have updated rules
			if updated.ChartOfAccountsRules != tt.newRules {
				t.Errorf("Expected rules %v, got %v", tt.newRules, updated.ChartOfAccountsRules)
			}

			// ID should be preserved
			if updated.ID != original.ID {
				t.Error("ID should be preserved across rules update")
			}

			// Status should be preserved
			if updated.Status != original.Status {
				t.Error("Status should be preserved across rules update")
			}

			// CreatedAt should be preserved
			if !updated.CreatedAt.Equal(original.CreatedAt) {
				t.Error("CreatedAt should be preserved across rules update")
			}

			// UpdatedAt should be updated
			if updated.UpdatedAt.Before(beforeUpdate) || updated.UpdatedAt.After(afterUpdate) {
				t.Errorf("UpdatedAt %v should be between %v and %v", updated.UpdatedAt, beforeUpdate, afterUpdate)
			}
		})
	}
}

func TestFinancialBookingLog_WithPosting(t *testing.T) {
	original := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	money := testMoneyForBookingLog(100, CurrencyGBP)
	posting1, _ := NewLedgerPosting(
		original.ID,
		PostingDirectionDebit,
		money,
		"ACC-001",
		time.Now(),
		"corr-001",
	)

	posting2, _ := NewLedgerPosting(
		original.ID,
		PostingDirectionCredit,
		money,
		"ACC-002",
		time.Now(),
		"corr-001",
	)

	t.Run("add first posting", func(t *testing.T) {
		updated := original.WithPosting(posting1)

		// Original should be unchanged (immutability)
		if len(original.Postings()) != 0 {
			t.Errorf("Original postings count changed to %d", len(original.Postings()))
		}

		// New instance should have the posting
		postings := updated.Postings()
		if len(postings) != 1 {
			t.Fatalf("Expected 1 posting, got %d", len(postings))
		}

		if postings[0].ID != posting1.ID {
			t.Error("First posting ID mismatch")
		}

		// ID should be preserved
		if updated.ID != original.ID {
			t.Error("ID should be preserved across posting addition")
		}
	})

	t.Run("add multiple postings sequentially", func(t *testing.T) {
		withFirst := original.WithPosting(posting1)
		withBoth := withFirst.WithPosting(posting2)

		// Original should still have 0 postings
		if len(original.Postings()) != 0 {
			t.Error("Original postings should remain empty")
		}

		// First update should have 1 posting
		if len(withFirst.Postings()) != 1 {
			t.Errorf("First update should have 1 posting, got %d", len(withFirst.Postings()))
		}

		// Second update should have 2 postings
		postings := withBoth.Postings()
		if len(postings) != 2 {
			t.Fatalf("Expected 2 postings, got %d", len(postings))
		}

		if postings[0].ID != posting1.ID {
			t.Error("First posting ID mismatch")
		}

		if postings[1].ID != posting2.ID {
			t.Error("Second posting ID mismatch")
		}
	})

	t.Run("postings returns defensive copy", func(t *testing.T) {
		withPosting := original.WithPosting(posting1)

		firstCopy := withPosting.Postings()
		secondCopy := withPosting.Postings()

		// Modifying one copy should not affect the other
		firstCopy[0] = nil
		if secondCopy[0] == nil {
			t.Error("Modifying one copy should not affect another")
		}
	})
}

func TestFinancialBookingLog_Postings_EmptySlice(t *testing.T) {
	log := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	postings := log.Postings()

	if postings == nil {
		t.Error("Postings() should return empty slice, not nil")
	}

	if len(postings) != 0 {
		t.Errorf("Expected empty slice, got %d elements", len(postings))
	}
}

func TestFinancialBookingLog_IsTerminal(t *testing.T) {
	tests := []struct {
		name           string
		status         TransactionStatus
		expectTerminal bool
	}{
		{
			name:           "PENDING is not terminal",
			status:         TransactionStatusPending,
			expectTerminal: false,
		},
		{
			name:           "POSTED is terminal",
			status:         TransactionStatusPosted,
			expectTerminal: true,
		},
		{
			name:           "FAILED is terminal",
			status:         TransactionStatusFailed,
			expectTerminal: true,
		},
		{
			name:           "CANCELLED is terminal",
			status:         TransactionStatusCancelled,
			expectTerminal: true,
		},
		{
			name:           "REVERSED is not terminal for booking log",
			status:         TransactionStatusReversed,
			expectTerminal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewFinancialBookingLog(
				"ASSET",
				"PROD-001",
				"BU-TREASURY",
				"UK-GAAP-2024",
				CurrencyGBP,
			)

			updated := log.WithStatus(tt.status)

			if updated.IsTerminal() != tt.expectTerminal {
				t.Errorf("Expected IsTerminal() = %v for status %v", tt.expectTerminal, tt.status)
			}
		})
	}
}

func TestFinancialBookingLog_ImmutabilityChain(t *testing.T) {
	// Test that chaining operations maintains immutability
	original := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	originalID := original.ID

	money := testMoneyForBookingLog(100, CurrencyGBP)
	posting, _ := NewLedgerPosting(
		original.ID,
		PostingDirectionDebit,
		money,
		"ACC-001",
		time.Now(),
		"corr-001",
	)

	// Chain multiple operations
	result := original.
		WithChartOfAccountsRules("IFRS-2024").
		WithPosting(posting).
		WithStatus(TransactionStatusPosted)

	// Verify original is unchanged
	if original.ChartOfAccountsRules != "UK-GAAP-2024" {
		t.Error("Original rules should be unchanged")
	}

	if len(original.Postings()) != 0 {
		t.Error("Original postings should be empty")
	}

	if original.Status != TransactionStatusPending {
		t.Error("Original status should be PENDING")
	}

	// Verify result has all updates
	if result.ChartOfAccountsRules != "IFRS-2024" {
		t.Error("Result should have IFRS-2024 rules")
	}

	if len(result.Postings()) != 1 {
		t.Error("Result should have 1 posting")
	}

	if result.Status != TransactionStatusPosted {
		t.Error("Result should have POSTED status")
	}

	// ID should be preserved throughout
	if result.ID != originalID {
		t.Error("ID should be preserved through chain")
	}
}

func TestFinancialBookingLog_TimestampsUTC(t *testing.T) {
	log := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	if log.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt should be UTC, got %v", log.CreatedAt.Location())
	}

	if log.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt should be UTC, got %v", log.UpdatedAt.Location())
	}

	// After status update
	updated := log.WithStatus(TransactionStatusPosted)

	if updated.UpdatedAt.Location() != time.UTC {
		t.Errorf("Updated UpdatedAt should be UTC, got %v", updated.UpdatedAt.Location())
	}
}

func TestFinancialBookingLog_AllSupportedCurrencies(t *testing.T) {
	currencies := []Currency{
		CurrencyGBP,
		CurrencyUSD,
		CurrencyEUR,
		CurrencyJPY,
		CurrencyCHF,
		CurrencyCAD,
		CurrencyAUD,
	}

	for _, currency := range currencies {
		t.Run(string(currency), func(t *testing.T) {
			log := NewFinancialBookingLog(
				"ASSET",
				"PROD-001",
				"BU-TREASURY",
				"RULES",
				currency,
			)

			if log.BaseCurrency != currency {
				t.Errorf("Expected currency %v, got %v", currency, log.BaseCurrency)
			}
		})
	}
}

func TestControlAction_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		action  ControlAction
		isValid bool
	}{
		{
			name:    "SUSPEND is valid",
			action:  ControlActionSuspend,
			isValid: true,
		},
		{
			name:    "RESUME is valid",
			action:  ControlActionResume,
			isValid: true,
		},
		{
			name:    "TERMINATE is valid",
			action:  ControlActionTerminate,
			isValid: true,
		},
		{
			name:    "empty string is invalid",
			action:  ControlAction(""),
			isValid: false,
		},
		{
			name:    "unknown action is invalid",
			action:  ControlAction("UNKNOWN"),
			isValid: false,
		},
		{
			name:    "lowercase suspend is invalid",
			action:  ControlAction("suspend"),
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.IsValid(); got != tt.isValid {
				t.Errorf("ControlAction.IsValid() = %v, want %v", got, tt.isValid)
			}
		})
	}
}

func TestControlAction_String(t *testing.T) {
	tests := []struct {
		name   string
		action ControlAction
		want   string
	}{
		{
			name:   "SUSPEND string representation",
			action: ControlActionSuspend,
			want:   "SUSPEND",
		},
		{
			name:   "RESUME string representation",
			action: ControlActionResume,
			want:   "RESUME",
		},
		{
			name:   "TERMINATE string representation",
			action: ControlActionTerminate,
			want:   "TERMINATE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.String(); got != tt.want {
				t.Errorf("ControlAction.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFinancialBookingLog_ControlLog_Suspend(t *testing.T) {
	t.Run("suspend from PENDING succeeds", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		)

		updated, err := log.ControlLog(ControlActionSuspend, "Suspicious activity detected")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if updated.Status != TransactionStatusFailed {
			t.Errorf("Expected status FAILED (suspended), got %v", updated.Status)
		}

		// Verify immutability - original unchanged
		if log.Status != TransactionStatusPending {
			t.Error("Original log status should remain PENDING")
		}
	})

	t.Run("suspend from POSTED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusPosted)

		_, err := log.ControlLog(ControlActionSuspend, "Attempting to suspend")
		if err == nil {
			t.Fatal("Expected error for suspending terminal state")
		}
		if !errors.Is(err, ErrCannotSuspendTerminal) {
			t.Errorf("Expected ErrCannotSuspendTerminal, got %v", err)
		}
	})

	t.Run("suspend from CANCELLED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusCancelled)

		_, err := log.ControlLog(ControlActionSuspend, "Attempting to suspend")
		if !errors.Is(err, ErrCannotSuspendTerminal) {
			t.Errorf("Expected ErrCannotSuspendTerminal, got %v", err)
		}
	})

	t.Run("suspend without reason fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		)

		_, err := log.ControlLog(ControlActionSuspend, "")
		if !errors.Is(err, ErrReasonRequired) {
			t.Errorf("Expected ErrReasonRequired, got %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_Resume(t *testing.T) {
	t.Run("resume from FAILED (suspended) succeeds", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusFailed) // Simulates suspended state

		updated, err := log.ControlLog(ControlActionResume, "Issue resolved, resuming")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if updated.Status != TransactionStatusPending {
			t.Errorf("Expected status PENDING, got %v", updated.Status)
		}
	})

	t.Run("resume from PENDING fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		)

		_, err := log.ControlLog(ControlActionResume, "Attempting to resume")
		if !errors.Is(err, ErrCannotResumePending) {
			t.Errorf("Expected ErrCannotResumePending, got %v", err)
		}
	})

	t.Run("resume from POSTED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusPosted)

		_, err := log.ControlLog(ControlActionResume, "Attempting to resume")
		if !errors.Is(err, ErrCannotResumePending) {
			t.Errorf("Expected ErrCannotResumePending, got %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_Terminate(t *testing.T) {
	t.Run("terminate from PENDING succeeds", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		)

		updated, err := log.ControlLog(ControlActionTerminate, "Business decision to cancel")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if updated.Status != TransactionStatusCancelled {
			t.Errorf("Expected status CANCELLED, got %v", updated.Status)
		}
	})

	t.Run("terminate from FAILED (suspended) succeeds", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusFailed)

		updated, err := log.ControlLog(ControlActionTerminate, "Cannot resolve issue, terminating")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if updated.Status != TransactionStatusCancelled {
			t.Errorf("Expected status CANCELLED, got %v", updated.Status)
		}
	})

	t.Run("terminate from POSTED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusPosted)

		_, err := log.ControlLog(ControlActionTerminate, "Attempting to terminate")
		if !errors.Is(err, ErrCannotTerminateTerminal) {
			t.Errorf("Expected ErrCannotTerminateTerminal, got %v", err)
		}
	})

	t.Run("terminate from CANCELLED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusCancelled)

		_, err := log.ControlLog(ControlActionTerminate, "Attempting to terminate again")
		if !errors.Is(err, ErrCannotTerminateTerminal) {
			t.Errorf("Expected ErrCannotTerminateTerminal, got %v", err)
		}
	})

	t.Run("terminate from REVERSED fails", func(t *testing.T) {
		log := NewFinancialBookingLog(
			"ASSET",
			"PROD-001",
			"BU-TREASURY",
			"UK-GAAP-2024",
			CurrencyGBP,
		).WithStatus(TransactionStatusReversed)

		_, err := log.ControlLog(ControlActionTerminate, "Attempting to terminate reversed")
		if !errors.Is(err, ErrCannotTerminateTerminal) {
			t.Errorf("Expected ErrCannotTerminateTerminal, got %v", err)
		}
	})
}

func TestFinancialBookingLog_ControlLog_InvalidAction(t *testing.T) {
	log := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	_, err := log.ControlLog(ControlAction("INVALID"), "Some reason")
	if !errors.Is(err, ErrInvalidControlAction) {
		t.Errorf("Expected ErrInvalidControlAction, got %v", err)
	}
}

func TestFinancialBookingLog_ControlLog_Immutability(t *testing.T) {
	original := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)
	originalID := original.ID
	originalCreatedAt := original.CreatedAt

	updated, err := original.ControlLog(ControlActionSuspend, "Test suspension")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Original unchanged
	if original.Status != TransactionStatusPending {
		t.Error("Original status should remain PENDING")
	}

	// Updated has new status
	if updated.Status != TransactionStatusFailed {
		t.Error("Updated status should be FAILED (suspended)")
	}

	// ID preserved
	if updated.ID != originalID {
		t.Error("ID should be preserved")
	}

	// CreatedAt preserved
	if !updated.CreatedAt.Equal(originalCreatedAt) {
		t.Error("CreatedAt should be preserved")
	}

	// UpdatedAt should be more recent
	if !updated.UpdatedAt.After(original.UpdatedAt) && !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Error("UpdatedAt should be updated or equal")
	}

	// Other fields preserved
	if updated.FinancialAccountType != original.FinancialAccountType {
		t.Error("FinancialAccountType should be preserved")
	}
	if updated.ProductServiceReference != original.ProductServiceReference {
		t.Error("ProductServiceReference should be preserved")
	}
	if updated.BusinessUnitReference != original.BusinessUnitReference {
		t.Error("BusinessUnitReference should be preserved")
	}
	if updated.ChartOfAccountsRules != original.ChartOfAccountsRules {
		t.Error("ChartOfAccountsRules should be preserved")
	}
	if updated.BaseCurrency != original.BaseCurrency {
		t.Error("BaseCurrency should be preserved")
	}
}

func TestFinancialBookingLog_IsSuspended(t *testing.T) {
	tests := []struct {
		name          string
		status        TransactionStatus
		wantSuspended bool
	}{
		{
			name:          "FAILED is suspended",
			status:        TransactionStatusFailed,
			wantSuspended: true,
		},
		{
			name:          "PENDING is not suspended",
			status:        TransactionStatusPending,
			wantSuspended: false,
		},
		{
			name:          "POSTED is not suspended",
			status:        TransactionStatusPosted,
			wantSuspended: false,
		},
		{
			name:          "CANCELLED is not suspended",
			status:        TransactionStatusCancelled,
			wantSuspended: false,
		},
		{
			name:          "REVERSED is not suspended",
			status:        TransactionStatusReversed,
			wantSuspended: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewFinancialBookingLog(
				"ASSET",
				"PROD-001",
				"BU-TREASURY",
				"UK-GAAP-2024",
				CurrencyGBP,
			).WithStatus(tt.status)

			if got := log.IsSuspended(); got != tt.wantSuspended {
				t.Errorf("IsSuspended() = %v, want %v", got, tt.wantSuspended)
			}
		})
	}
}

func TestFinancialBookingLog_ControlLog_SuspendResumeChain(t *testing.T) {
	// Test the complete suspend -> resume flow
	log := NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		CurrencyGBP,
	)

	// Step 1: Suspend
	suspended, err := log.ControlLog(ControlActionSuspend, "Flagged for review")
	if err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	if suspended.Status != TransactionStatusFailed {
		t.Errorf("Expected FAILED after suspend, got %v", suspended.Status)
	}

	// Step 2: Resume
	resumed, err := suspended.ControlLog(ControlActionResume, "Review completed, cleared")
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if resumed.Status != TransactionStatusPending {
		t.Errorf("Expected PENDING after resume, got %v", resumed.Status)
	}

	// Verify original is unchanged throughout
	if log.Status != TransactionStatusPending {
		t.Error("Original log should remain PENDING")
	}
}
