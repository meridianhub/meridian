package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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
			time.Sleep(time.Millisecond)
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
			time.Sleep(time.Millisecond)
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

	money, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
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

	money, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
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
