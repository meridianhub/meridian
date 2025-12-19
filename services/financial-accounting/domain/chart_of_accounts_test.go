package domain

import (
	"errors"
	"testing"
)

func TestAccountClassification_IsValid(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "ASSET is valid",
			classification: AccountClassificationAsset,
			want:           true,
		},
		{
			name:           "LIABILITY is valid",
			classification: AccountClassificationLiability,
			want:           true,
		},
		{
			name:           "empty is invalid",
			classification: AccountClassification(""),
			want:           false,
		},
		{
			name:           "CLEARING is not a valid classification",
			classification: AccountClassification("CLEARING"),
			want:           false,
		},
		{
			name:           "NOSTRO is not a valid classification",
			classification: AccountClassification("NOSTRO"),
			want:           false,
		},
		{
			name:           "unknown classification is invalid",
			classification: AccountClassification("UNKNOWN"),
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.classification.IsValid(); got != tt.want {
				t.Errorf("AccountClassification.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountClassification_String(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           string
	}{
		{
			name:           "ASSET string",
			classification: AccountClassificationAsset,
			want:           "ASSET",
		},
		{
			name:           "LIABILITY string",
			classification: AccountClassificationLiability,
			want:           "LIABILITY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.classification.String(); got != tt.want {
				t.Errorf("AccountClassification.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountClassification_IncreasesWithDebit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "ASSET increases with debit",
			classification: AccountClassificationAsset,
			want:           true,
		},
		{
			name:           "LIABILITY does not increase with debit",
			classification: AccountClassificationLiability,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.classification.IncreasesWithDebit(); got != tt.want {
				t.Errorf("AccountClassification.IncreasesWithDebit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountClassification_IncreasesWithCredit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "LIABILITY increases with credit",
			classification: AccountClassificationLiability,
			want:           true,
		},
		{
			name:           "ASSET does not increase with credit",
			classification: AccountClassificationAsset,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.classification.IncreasesWithCredit(); got != tt.want {
				t.Errorf("AccountClassification.IncreasesWithCredit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewAccount(t *testing.T) {
	tests := []struct {
		name           string
		accountCode    string
		accountName    string
		classification AccountClassification
		currency       Currency
		wantErr        bool
		expectedErr    error
	}{
		{
			name:           "valid asset account",
			accountCode:    "1000",
			accountName:    "Cash Account",
			classification: AccountClassificationAsset,
			currency:       CurrencyGBP,
			wantErr:        false,
		},
		{
			name:           "valid liability account",
			accountCode:    "2000",
			accountName:    "Accounts Payable",
			classification: AccountClassificationLiability,
			currency:       CurrencyUSD,
			wantErr:        false,
		},
		{
			name:           "empty account code",
			accountCode:    "",
			accountName:    "Test Account",
			classification: AccountClassificationAsset,
			currency:       CurrencyGBP,
			wantErr:        true,
			expectedErr:    ErrInvalidAccountCode,
		},
		{
			name:           "empty account name",
			accountCode:    "1000",
			accountName:    "",
			classification: AccountClassificationAsset,
			currency:       CurrencyGBP,
			wantErr:        true,
			expectedErr:    ErrInvalidAccountName,
		},
		{
			name:           "invalid classification",
			accountCode:    "1000",
			accountName:    "Test Account",
			classification: AccountClassification("INVALID"),
			currency:       CurrencyGBP,
			wantErr:        true,
			expectedErr:    ErrInvalidAccountClassification,
		},
		{
			name:           "invalid currency",
			accountCode:    "1000",
			accountName:    "Test Account",
			classification: AccountClassificationAsset,
			currency:       Currency("INVALID"),
			wantErr:        true,
			expectedErr:    ErrInvalidAccountCurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount(tt.accountCode, tt.accountName, tt.classification, tt.currency)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if account.AccountCode != tt.accountCode {
				t.Errorf("Expected AccountCode %v, got %v", tt.accountCode, account.AccountCode)
			}
			if account.Name != tt.accountName {
				t.Errorf("Expected Name %v, got %v", tt.accountName, account.Name)
			}
			if account.Classification != tt.classification {
				t.Errorf("Expected Classification %v, got %v", tt.classification, account.Classification)
			}
			if account.Currency != tt.currency {
				t.Errorf("Expected Currency %v, got %v", tt.currency, account.Currency)
			}
			if !account.IsActive {
				t.Error("Expected new account to be active")
			}
			if account.CreatedAt.IsZero() {
				t.Error("Expected CreatedAt to be set")
			}
			if account.UpdatedAt.IsZero() {
				t.Error("Expected UpdatedAt to be set")
			}
		})
	}
}

func TestNewClearingAccount(t *testing.T) {
	account, err := NewClearingAccount("CLR-001", "Test Clearing", CurrencyGBP)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	// Clearing accounts are classified as ASSET (temporary holding, debit balance)
	if account.Classification != AccountClassificationAsset {
		t.Errorf("Expected ASSET classification for clearing account, got %v", account.Classification)
	}
	if account.AccountCode != "CLR-001" {
		t.Errorf("Expected account code 'CLR-001', got %v", account.AccountCode)
	}
	if account.Name != "Test Clearing" {
		t.Errorf("Expected name 'Test Clearing', got %v", account.Name)
	}
	if !account.IncreasesWithDebit() {
		t.Error("Clearing accounts should increase with debit")
	}
}

func TestNewNostroAccount(t *testing.T) {
	account, err := NewNostroAccount("NOS-001", "Nostro at Bank ABC", CurrencyUSD)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	// Nostro accounts are ASSET (our money held at another bank)
	if account.Classification != AccountClassificationAsset {
		t.Errorf("Expected ASSET classification for nostro account, got %v", account.Classification)
	}
	if account.AccountCode != "NOS-001" {
		t.Errorf("Expected account code 'NOS-001', got %v", account.AccountCode)
	}
	if !account.IncreasesWithDebit() {
		t.Error("Nostro accounts should increase with debit (they are assets)")
	}
}

func TestNewVostroAccount(t *testing.T) {
	account, err := NewVostroAccount("VOS-001", "Vostro for Bank XYZ", CurrencyEUR)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	// Vostro accounts are LIABILITY (their money held at our bank)
	if account.Classification != AccountClassificationLiability {
		t.Errorf("Expected LIABILITY classification for vostro account, got %v", account.Classification)
	}
	if account.AccountCode != "VOS-001" {
		t.Errorf("Expected account code 'VOS-001', got %v", account.AccountCode)
	}
	if !account.IncreasesWithCredit() {
		t.Error("Vostro accounts should increase with credit (they are liabilities)")
	}
}

func TestNewAssetAccount(t *testing.T) {
	account, err := NewAssetAccount("1000", "Cash Reserve", CurrencyEUR)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationAsset {
		t.Errorf("Expected ASSET classification, got %v", account.Classification)
	}
	if !account.IncreasesWithDebit() {
		t.Error("Asset accounts should increase with debit")
	}
}

func TestNewLiabilityAccount(t *testing.T) {
	account, err := NewLiabilityAccount("2000", "Customer Deposits", CurrencyGBP)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationLiability {
		t.Errorf("Expected LIABILITY classification, got %v", account.Classification)
	}
	if !account.IncreasesWithCredit() {
		t.Error("Liability accounts should increase with credit")
	}
}

func TestAccount_IncreasesWithDebit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "ASSET increases with debit",
			classification: AccountClassificationAsset,
			want:           true,
		},
		{
			name:           "LIABILITY does not increase with debit",
			classification: AccountClassificationLiability,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount("1000", "Test", tt.classification, CurrencyGBP)
			if err != nil {
				t.Fatalf("Failed to create account: %v", err)
			}

			if got := account.IncreasesWithDebit(); got != tt.want {
				t.Errorf("Account.IncreasesWithDebit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccount_IncreasesWithCredit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "LIABILITY increases with credit",
			classification: AccountClassificationLiability,
			want:           true,
		},
		{
			name:           "ASSET does not increase with credit",
			classification: AccountClassificationAsset,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount("1000", "Test", tt.classification, CurrencyGBP)
			if err != nil {
				t.Fatalf("Failed to create account: %v", err)
			}

			if got := account.IncreasesWithCredit(); got != tt.want {
				t.Errorf("Account.IncreasesWithCredit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccount_WithActive_Immutability(t *testing.T) {
	original, err := NewAccount("1000", "Test Account", AccountClassificationAsset, CurrencyGBP)
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	if !original.IsActive {
		t.Error("Expected new account to be active")
	}

	// Deactivate should return a new copy, not modify original
	deactivated := original.WithActive(false)

	// Original should still be active
	if !original.IsActive {
		t.Error("Original account should still be active (immutability)")
	}

	// Deactivated copy should be inactive
	if deactivated.IsActive {
		t.Error("Deactivated copy should be inactive")
	}

	// UpdatedAt should be different on the copy
	if deactivated.UpdatedAt.Equal(original.UpdatedAt) || deactivated.UpdatedAt.Before(original.UpdatedAt) {
		t.Error("Deactivated copy should have later UpdatedAt")
	}

	// Reactivate the deactivated copy
	reactivated := deactivated.WithActive(true)
	if !reactivated.IsActive {
		t.Error("Reactivated copy should be active")
	}

	// Deactivated copy should still be inactive
	if deactivated.IsActive {
		t.Error("Deactivated copy should still be inactive (immutability)")
	}
}

func TestAccount_ValidateForPosting(t *testing.T) {
	t.Run("active account can be used for posting", func(t *testing.T) {
		account, err := NewAccount("1000", "Test Account", AccountClassificationAsset, CurrencyGBP)
		if err != nil {
			t.Fatalf("Failed to create account: %v", err)
		}

		if err := account.ValidateForPosting(); err != nil {
			t.Errorf("Expected no error for active account, got %v", err)
		}
	})

	t.Run("inactive account cannot be used for posting", func(t *testing.T) {
		account, err := NewAccount("1000", "Test Account", AccountClassificationAsset, CurrencyGBP)
		if err != nil {
			t.Fatalf("Failed to create account: %v", err)
		}

		inactive := account.WithActive(false)

		if err := inactive.ValidateForPosting(); !errors.Is(err, ErrAccountInactive) {
			t.Errorf("Expected ErrAccountInactive, got %v", err)
		}
	})
}

func TestNostroVostroSemantics(t *testing.T) {
	t.Run("nostro is our money at their bank - an asset", func(t *testing.T) {
		nostro, err := NewNostroAccount("NOS-001", "Our USD at Citibank", CurrencyUSD)
		if err != nil {
			t.Fatalf("Failed to create nostro account: %v", err)
		}

		if nostro.Classification != AccountClassificationAsset {
			t.Error("Nostro should be classified as ASSET")
		}
		if !nostro.IncreasesWithDebit() {
			t.Error("Nostro should increase with debit (normal asset behavior)")
		}
		if nostro.IncreasesWithCredit() {
			t.Error("Nostro should not increase with credit")
		}
	})

	t.Run("vostro is their money at our bank - a liability", func(t *testing.T) {
		vostro, err := NewVostroAccount("VOS-001", "Citibank USD at Our Bank", CurrencyUSD)
		if err != nil {
			t.Fatalf("Failed to create vostro account: %v", err)
		}

		if vostro.Classification != AccountClassificationLiability {
			t.Error("Vostro should be classified as LIABILITY")
		}
		if !vostro.IncreasesWithCredit() {
			t.Error("Vostro should increase with credit (normal liability behavior)")
		}
		if vostro.IncreasesWithDebit() {
			t.Error("Vostro should not increase with debit")
		}
	})
}
