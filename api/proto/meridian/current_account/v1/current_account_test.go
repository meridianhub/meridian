package currentaccountv1_test

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestCurrentAccountFacility_BasicConstruction tests basic message construction
// for subtask 5.1: Define CurrentAccountFacility message with account identification and status management
func TestCurrentAccountFacility_BasicConstruction(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:          "ACC-12345",
		ExternalIdentifier: "GB29NWBK60161331926819",
		AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		InstrumentCode:     "GBP",
		CreatedAt:          now,
		UpdatedAt:          now,
		Version:            1,
	}

	if facility.GetAccountId() == "" {
		t.Error("AccountId should not be empty")
	}
	if facility.GetExternalIdentifier() == "" {
		t.Error("ExternalIdentifier should not be empty")
	}
	if facility.GetAccountStatus() != currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Error("AccountStatus should be ACTIVE")
	}
	if facility.GetInstrumentCode() != "GBP" {
		t.Errorf("Expected instrument code GBP, got %s", facility.GetInstrumentCode())
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
		AccountId:          "ACC-12345",
		ExternalIdentifier: "GB29NWBK60161331926819",
		AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		InstrumentCode:     "GBP",
		CreatedAt:          now,
		UpdatedAt:          now,
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
		AccountId:          "ACC-12345",
		ExternalIdentifier: "GB29NWBK60161331926819",
		AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		InstrumentCode:     "GBP",
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

// TestCurrentAccountFacility_InstrumentCode tests asset-agnostic instrument code field
func TestCurrentAccountFacility_InstrumentCode(t *testing.T) {
	now := timestamppb.New(time.Now())

	tests := []struct {
		name           string
		instrumentCode string
		dimension      string
	}{
		{"currency GBP", "GBP", "CURRENCY"},
		{"energy kWh", "KWH", "ENERGY"},
		{"compute GPU hours", "GPU_HOUR", "COMPUTE"},
		{"carbon credits", "TONNE_CO2E", "CARBON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facility := &currentaccountv1.CurrentAccountFacility{
				AccountId:          "ACC-12345",
				ExternalIdentifier: "ACCT-001",
				AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
				InstrumentCode:     tt.instrumentCode,
				Dimension:          tt.dimension,
				CreatedAt:          now,
				UpdatedAt:          now,
				Version:            1,
			}

			if facility.GetInstrumentCode() != tt.instrumentCode {
				t.Errorf("Expected instrument code %s, got %s", tt.instrumentCode, facility.GetInstrumentCode())
			}
			if facility.GetDimension() != tt.dimension {
				t.Errorf("Expected dimension %s, got %s", tt.dimension, facility.GetDimension())
			}
		})
	}
}

// TestCurrentAccountFacility_WithTransactionHistory tests account with history
func TestCurrentAccountFacility_WithTransactionHistory(t *testing.T) {
	now := timestamppb.New(time.Now())

	facility := &currentaccountv1.CurrentAccountFacility{
		AccountId:          "ACC-12345",
		ExternalIdentifier: "GB29NWBK60161331926819",
		AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		InstrumentCode:     "GBP",
		CurrentBalance: &currentaccountv1.AccountBalance{
			CurrentBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 500},
			},
			AvailableBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 500},
			},
			LastUpdated: now,
		},
		TransactionHistory: &currentaccountv1.TransactionHistory{
			AccountId: "ACC-12345",
			Transactions: []*currentaccountv1.AccountTransaction{
				{
					TransactionId: "TXN-1",
					AccountId:     "ACC-12345",
					Direction:     commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
					Amount: &commonv1.MoneyAmount{
						Amount: &money.Money{CurrencyCode: "GBP", Units: 1000},
					},
					Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
					Timestamp: now,
				},
				{
					TransactionId: "TXN-2",
					AccountId:     "ACC-12345",
					Direction:     commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
					Amount: &commonv1.MoneyAmount{
						Amount: &money.Money{CurrencyCode: "GBP", Units: 500},
					},
					Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
					Timestamp: now,
				},
			},
			TotalCount:  2,
			LastUpdated: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	if facility.GetTransactionHistory() == nil {
		t.Error("TransactionHistory should not be nil")
	}
	if len(facility.GetTransactionHistory().GetTransactions()) != 2 {
		t.Errorf("Expected 2 transactions in history, got %d",
			len(facility.GetTransactionHistory().GetTransactions()))
	}

	// Verify transaction types
	txns := facility.GetTransactionHistory().GetTransactions()
	if txns[0].GetDirection() != commonv1.PostingDirection_POSTING_DIRECTION_CREDIT {
		t.Error("First transaction should be CREDIT")
	}
	if txns[1].GetDirection() != commonv1.PostingDirection_POSTING_DIRECTION_DEBIT {
		t.Error("Second transaction should be DEBIT")
	}
}

// TestValidation_ExternalIdentifierFormat tests external identifier validation
// The field now accepts any string up to 255 characters; format is validated by product type CEL rules.
func TestValidation_ExternalIdentifierFormat(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name       string
		identifier string
		wantError  bool
	}{
		{
			name:       "valid IBAN",
			identifier: "GB29NWBK60161331926819",
			wantError:  false,
		},
		{
			name:       "valid sort code and account number",
			identifier: "20-00-00/12345678",
			wantError:  false,
		},
		{
			name:       "valid meter ID",
			identifier: "METER-UK-001-2024",
			wantError:  false,
		},
		{
			name:       "valid GPU node reference",
			identifier: "NODE-A100-CLUSTER-42",
			wantError:  false,
		},
		{
			name:       "empty string fails (min_len: 1)",
			identifier: "",
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facility := &currentaccountv1.CurrentAccountFacility{
				AccountId:          "acc-123",
				ExternalIdentifier: tt.identifier,
				AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
				InstrumentCode:     "GBP",
				CreatedAt:          now,
				UpdatedAt:          now,
				Version:            1,
			}

			err := validator.Validate(facility)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error for identifier %q but got none", tt.identifier)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for identifier %q: %v", tt.identifier, err)
				}
			}
		})
	}
}

// TestValidation_AccountIDPattern_CurrentAccount tests account ID pattern validation
func TestValidation_AccountIDPattern_CurrentAccount(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name      string
		accountID string
		wantError bool
	}{
		{
			name:      "valid alphanumeric",
			accountID: "ACC123",
			wantError: false,
		},
		{
			name:      "valid with hyphens",
			accountID: "ACC-123-XYZ",
			wantError: false,
		},
		{
			name:      "valid with underscores",
			accountID: "ACC_123_XYZ",
			wantError: false,
		},
		{
			name:      "valid mixed",
			accountID: "ACC-123_XYZ-789",
			wantError: false,
		},
		{
			name:      "invalid with spaces",
			accountID: "ACC 123",
			wantError: true,
		},
		{
			name:      "invalid with special chars",
			accountID: "ACC@123",
			wantError: true,
		},
		{
			name:      "invalid with slashes",
			accountID: "ACC/123",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facility := &currentaccountv1.CurrentAccountFacility{
				AccountId:          tt.accountID,
				ExternalIdentifier: "GB29NWBK60161331926819",
				AccountStatus:      currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
				InstrumentCode:     "GBP",
				CreatedAt:          now,
				UpdatedAt:          now,
				Version:            1,
			}

			err := validator.Validate(facility)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error for account ID %q but got none", tt.accountID)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for account ID %q: %v", tt.accountID, err)
				}
			}
		})
	}
}

// TestValidation_ReferencePattern tests reference format validation
func TestValidation_ReferencePattern(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name      string
		reference string
		wantError bool
	}{
		{
			name:      "valid alphanumeric",
			reference: "REF123456",
			wantError: false,
		},
		{
			name:      "valid with hyphens",
			reference: "REF-123-456",
			wantError: false,
		},
		{
			name:      "valid with underscores",
			reference: "REF_123_456",
			wantError: false,
		},
		{
			name:      "valid with slashes",
			reference: "INV/2024/001",
			wantError: false,
		},
		{
			name:      "valid mixed",
			reference: "PAY-2024_01/001",
			wantError: false,
		},
		{
			name:      "valid empty (optional field)",
			reference: "",
			wantError: false,
		},
		{
			name:      "invalid with spaces",
			reference: "REF 123",
			wantError: true,
		},
		{
			name:      "invalid with special chars",
			reference: "REF@123",
			wantError: true,
		},
		{
			name:      "invalid with dots",
			reference: "REF.123",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transaction := &currentaccountv1.AccountTransaction{
				TransactionId: "txn-123",
				AccountId:     "acc-456",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
						Nanos:        0,
					},
				},
				Direction:   commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				Reference:   tt.reference,
				Timestamp:   now,
				Status:      commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				Description: "Test transaction",
			}

			err := validator.Validate(transaction)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error for reference %q but got none", tt.reference)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for reference %q: %v", tt.reference, err)
				}
			}
		})
	}
}

// TestValidation_TransactionIDPattern tests transaction ID pattern validation
func TestValidation_TransactionIDPattern(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name    string
		txnID   string
		wantErr bool
	}{
		{
			name:    "valid alphanumeric",
			txnID:   "TXN123",
			wantErr: false,
		},
		{
			name:    "valid with hyphens",
			txnID:   "TXN-123-ABC",
			wantErr: false,
		},
		{
			name:    "valid with underscores",
			txnID:   "TXN_123_ABC",
			wantErr: false,
		},
		{
			name:    "invalid with spaces",
			txnID:   "TXN 123",
			wantErr: true,
		},
		{
			name:    "invalid with special chars",
			txnID:   "TXN@123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transaction := &currentaccountv1.AccountTransaction{
				TransactionId: tt.txnID,
				AccountId:     "acc-123",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
						Nanos:        0,
					},
				},
				Direction:   commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				Timestamp:   now,
				Status:      commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				Description: "Test transaction",
			}

			err := validator.Validate(transaction)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected validation error for transaction ID %q but got none", tt.txnID)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for transaction ID %q: %v", tt.txnID, err)
				}
			}
		})
	}
}
