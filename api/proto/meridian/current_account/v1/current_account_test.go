package currentaccountv1_test

import (
	"testing"
	"time"

	commonv1 "github.com/bjcoombs/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/bjcoombs/meridian/api/proto/meridian/current_account/v1"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestCurrentAccountFacility_BasicConstruction tests basic message construction
// for subtask 5.1: Define CurrentAccountFacility message with account identification and status management
func TestCurrentAccountFacility_BasicConstruction(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:            "ACC-12345",
		AccountIdentification: "GB29NWBK60161331926819",
		AccountStatus:        currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		BaseCurrency:         commonv1.Currency_CURRENCY_GBP,
		CreatedAt:            now,
		UpdatedAt:            now,
		Version:              1,
	}

	if facility.GetAccountId() == "" {
		t.Error("AccountId should not be empty")
	}
	if facility.GetAccountIdentification() == "" {
		t.Error("AccountIdentification should not be empty")
	}
	if facility.GetAccountStatus() != currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Error("AccountStatus should be ACTIVE")
	}
	if facility.GetBaseCurrency() != commonv1.Currency_CURRENCY_GBP {
		t.Error("BaseCurrency should be GBP")
	}
	if facility.GetVersion() != 1 {
		t.Errorf("Expected version 1, got %d", facility.GetVersion())
	}
}

// TestAccountStatus_EnumValues tests that account status enum is properly defined
func TestAccountStatus_EnumValues(t *testing.T) {
	tests := []struct {
		name   string
		status currentaccountv1.AccountStatus
	}{
		{"unspecified", currentaccountv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED},
		{"active", currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE},
		{"frozen", currentaccountv1.AccountStatus_ACCOUNT_STATUS_FROZEN},
		{"closed", currentaccountv1.AccountStatus_ACCOUNT_STATUS_CLOSED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.status.String() == "" {
				t.Errorf("AccountStatus %s should have a string representation", tt.name)
			}
		})
	}
}

// TestCurrentAccountFacility_StatusTransitions tests status management
func TestCurrentAccountFacility_StatusTransitions(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:            "ACC-12345",
		AccountIdentification: "GB29NWBK60161331926819",
		AccountStatus:        currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		BaseCurrency:         commonv1.Currency_CURRENCY_GBP,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	// Test transition to frozen
	facility.AccountStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_FROZEN
	if facility.GetAccountStatus() != currentaccountv1.AccountStatus_ACCOUNT_STATUS_FROZEN {
		t.Error("AccountStatus should transition to FROZEN")
	}

	// Test transition to closed
	facility.AccountStatus = currentaccountv1.AccountStatus_ACCOUNT_STATUS_CLOSED
	if facility.GetAccountStatus() != currentaccountv1.AccountStatus_ACCOUNT_STATUS_CLOSED {
		t.Error("AccountStatus should transition to CLOSED")
	}
}

// TestCurrentAccountFacility_BalanceTracking tests balance tracking functionality
// for subtask 5.2: Implement account balance tracking and overdraft limits
func TestCurrentAccountFacility_BalanceTracking(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:            "ACC-12345",
		AccountIdentification: "GB29NWBK60161331926819",
		AccountStatus:        currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		BaseCurrency:         commonv1.Currency_CURRENCY_GBP,
		CurrentBalance: &currentaccountv1.AccountBalance{
			AvailableBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        1000,
					Nanos:        0,
				},
			},
			CurrentBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        1000,
					Nanos:        0,
				},
			},
			LastUpdated: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	if facility.GetCurrentBalance() == nil {
		t.Error("CurrentBalance should not be nil")
	}
	if facility.GetCurrentBalance().GetCurrentBalance() == nil {
		t.Error("CurrentBalance.CurrentBalance should not be nil")
	}
	if facility.GetCurrentBalance().GetCurrentBalance().GetAmount().GetUnits() != 1000 {
		t.Errorf("Expected balance 1000, got %d", facility.GetCurrentBalance().GetCurrentBalance().GetAmount().GetUnits())
	}
	if facility.GetCurrentBalance().GetAvailableBalance().GetAmount().GetUnits() != 1000 {
		t.Errorf("Expected available balance 1000, got %d", facility.GetCurrentBalance().GetAvailableBalance().GetAmount().GetUnits())
	}
}

// TestCurrentAccountFacility_OverdraftLimit tests overdraft limit functionality
func TestCurrentAccountFacility_OverdraftLimit(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:            "ACC-12345",
		AccountIdentification: "GB29NWBK60161331926819",
		AccountStatus:        currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		BaseCurrency:         commonv1.Currency_CURRENCY_GBP,
		OverdraftLimit: &currentaccountv1.OverdraftConfiguration{
			OverdraftLimit: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        500,
					Nanos:        0,
				},
			},
			InterestRate:  12.5,
			IsEnabled:     true,
			LastUpdated:   now,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	if facility.GetOverdraftLimit() == nil {
		t.Error("OverdraftLimit should not be nil")
	}
	if facility.GetOverdraftLimit().GetOverdraftLimit() == nil {
		t.Error("OverdraftLimit.OverdraftLimit should not be nil")
	}
	if facility.GetOverdraftLimit().GetOverdraftLimit().GetAmount().GetUnits() != 500 {
		t.Errorf("Expected overdraft limit 500, got %d", facility.GetOverdraftLimit().GetOverdraftLimit().GetAmount().GetUnits())
	}
	if !facility.GetOverdraftLimit().GetIsEnabled() {
		t.Error("Overdraft should be enabled")
	}
	if facility.GetOverdraftLimit().GetInterestRate() != 12.5 {
		t.Errorf("Expected interest rate 12.5, got %f", facility.GetOverdraftLimit().GetInterestRate())
	}
}

// TestCurrentAccountFacility_BalanceWithOverdraft tests balance calculation with overdraft
func TestCurrentAccountFacility_BalanceWithOverdraft(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:            "ACC-12345",
		AccountIdentification: "GB29NWBK60161331926819",
		AccountStatus:        currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		BaseCurrency:         commonv1.Currency_CURRENCY_GBP,
		CurrentBalance: &currentaccountv1.AccountBalance{
			CurrentBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
				},
			},
			AvailableBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        600, // 100 (current) + 500 (overdraft)
					Nanos:        0,
				},
			},
			LastUpdated: now,
		},
		OverdraftLimit: &currentaccountv1.OverdraftConfiguration{
			OverdraftLimit: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        500,
					Nanos:        0,
				},
			},
			InterestRate:  10.0,
			IsEnabled:     true,
			LastUpdated:   now,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	// Test that available balance includes overdraft
	currentBal := facility.GetCurrentBalance().GetCurrentBalance().GetAmount().GetUnits()
	overdraftLim := facility.GetOverdraftLimit().GetOverdraftLimit().GetAmount().GetUnits()
	availableBal := facility.GetCurrentBalance().GetAvailableBalance().GetAmount().GetUnits()

	expectedAvailable := currentBal + overdraftLim
	if availableBal != expectedAvailable {
		t.Errorf("Expected available balance %d (current %d + overdraft %d), got %d",
			expectedAvailable, currentBal, overdraftLim, availableBal)
	}
}
