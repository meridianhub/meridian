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
			name:           "CLEARING is valid",
			classification: AccountClassificationClearing,
			want:           true,
		},
		{
			name:           "NOSTRO is valid",
			classification: AccountClassificationNostro,
			want:           true,
		},
		{
			name:           "empty is invalid",
			classification: AccountClassification(""),
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
		{
			name:           "CLEARING string",
			classification: AccountClassificationClearing,
			want:           "CLEARING",
		},
		{
			name:           "NOSTRO string",
			classification: AccountClassificationNostro,
			want:           "NOSTRO",
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
			name:           "valid clearing account",
			accountCode:    "3000",
			accountName:    "Clearing Account",
			classification: AccountClassificationClearing,
			currency:       CurrencyEUR,
			wantErr:        false,
		},
		{
			name:           "valid nostro account",
			accountCode:    "4000",
			accountName:    "Nostro at Bank X",
			classification: AccountClassificationNostro,
			currency:       CurrencyGBP,
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
		})
	}
}

func TestNewClearingAccount(t *testing.T) {
	account, err := NewClearingAccount("Test Clearing", CurrencyGBP)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationClearing {
		t.Errorf("Expected CLEARING classification, got %v", account.Classification)
	}
	if account.Name != "Test Clearing" {
		t.Errorf("Expected name 'Test Clearing', got %v", account.Name)
	}
	if account.Currency != CurrencyGBP {
		t.Errorf("Expected currency GBP, got %v", account.Currency)
	}
	if !account.IsActive {
		t.Error("Expected new clearing account to be active")
	}
	if account.AccountCode == "" {
		t.Error("Expected non-empty account code")
	}
}

func TestNewNostroAccount(t *testing.T) {
	account, err := NewNostroAccount("Nostro at Bank ABC", CurrencyUSD)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationNostro {
		t.Errorf("Expected NOSTRO classification, got %v", account.Classification)
	}
	if account.Name != "Nostro at Bank ABC" {
		t.Errorf("Expected name 'Nostro at Bank ABC', got %v", account.Name)
	}
	if account.Currency != CurrencyUSD {
		t.Errorf("Expected currency USD, got %v", account.Currency)
	}
	if !account.IsActive {
		t.Error("Expected new nostro account to be active")
	}
}

func TestNewAssetAccount(t *testing.T) {
	account, err := NewAssetAccount("Cash Reserve", CurrencyEUR)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationAsset {
		t.Errorf("Expected ASSET classification, got %v", account.Classification)
	}
	if account.Name != "Cash Reserve" {
		t.Errorf("Expected name 'Cash Reserve', got %v", account.Name)
	}
}

func TestNewLiabilityAccount(t *testing.T) {
	account, err := NewLiabilityAccount("Customer Deposits", CurrencyGBP)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if account.Classification != AccountClassificationLiability {
		t.Errorf("Expected LIABILITY classification, got %v", account.Classification)
	}
	if account.Name != "Customer Deposits" {
		t.Errorf("Expected name 'Customer Deposits', got %v", account.Name)
	}
}

func TestAccount_CanDebit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "ASSET can debit",
			classification: AccountClassificationAsset,
			want:           true,
		},
		{
			name:           "CLEARING can debit",
			classification: AccountClassificationClearing,
			want:           true,
		},
		{
			name:           "LIABILITY cannot debit",
			classification: AccountClassificationLiability,
			want:           false,
		},
		{
			name:           "NOSTRO cannot debit",
			classification: AccountClassificationNostro,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount("1000", "Test", tt.classification, CurrencyGBP)
			if err != nil {
				t.Fatalf("Failed to create account: %v", err)
			}

			if got := account.CanDebit(); got != tt.want {
				t.Errorf("Account.CanDebit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccount_CanCredit(t *testing.T) {
	tests := []struct {
		name           string
		classification AccountClassification
		want           bool
	}{
		{
			name:           "LIABILITY can credit",
			classification: AccountClassificationLiability,
			want:           true,
		},
		{
			name:           "NOSTRO can credit",
			classification: AccountClassificationNostro,
			want:           true,
		},
		{
			name:           "ASSET cannot credit",
			classification: AccountClassificationAsset,
			want:           false,
		},
		{
			name:           "CLEARING cannot credit",
			classification: AccountClassificationClearing,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount("1000", "Test", tt.classification, CurrencyGBP)
			if err != nil {
				t.Fatalf("Failed to create account: %v", err)
			}

			if got := account.CanCredit(); got != tt.want {
				t.Errorf("Account.CanCredit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccount_Deactivate(t *testing.T) {
	account, err := NewAccount("1000", "Test Account", AccountClassificationAsset, CurrencyGBP)
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	if !account.IsActive {
		t.Error("Expected new account to be active")
	}

	account.Deactivate()

	if account.IsActive {
		t.Error("Expected account to be inactive after Deactivate()")
	}
}

func TestAccount_Activate(t *testing.T) {
	account, err := NewAccount("1000", "Test Account", AccountClassificationAsset, CurrencyGBP)
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	account.Deactivate()
	if account.IsActive {
		t.Error("Expected account to be inactive after Deactivate()")
	}

	account.Activate()
	if !account.IsActive {
		t.Error("Expected account to be active after Activate()")
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

		account.Deactivate()

		if err := account.ValidateForPosting(); !errors.Is(err, ErrAccountInactive) {
			t.Errorf("Expected ErrAccountInactive, got %v", err)
		}
	})
}
