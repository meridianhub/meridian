package currentaccountv1_test

import (
	"testing"
	"time"

	commonv1 "github.com/bjcoombs/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/bjcoombs/meridian/api/proto/meridian/current_account/v1"
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
