package financialaccountingv1_test

import (
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestFinancialBookingLogCreation tests creation of FinancialBookingLog messages
func TestFinancialBookingLogCreation(t *testing.T) {
	now := timestamppb.New(time.Now())

	log := &financialaccountingv1.FinancialBookingLog{
		Id:                      "log-123",
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "prod-456",
		BusinessUnitReference:   "bu-789",
		ChartOfAccountsRules:    "rules-001",
		BaseInstrumentCode:      "GBP",
		Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CreatedAt:               now,
		UpdatedAt:               now,
		Postings:                []*financialaccountingv1.LedgerPosting{},
	}

	if log.Id != "log-123" {
		t.Errorf("Expected ID log-123, got %s", log.Id)
	}
	if log.FinancialAccountType != "DEBIT" {
		t.Errorf("Expected DEBIT account type, got %v", log.FinancialAccountType)
	}
	if log.BaseInstrumentCode != "GBP" {
		t.Errorf("Expected GBP instrument code, got %v", log.BaseInstrumentCode)
	}
}

// TestLedgerPostingCreation tests creation of LedgerPosting messages
func TestLedgerPostingCreation(t *testing.T) {
	now := timestamppb.New(time.Now())

	posting := &financialaccountingv1.LedgerPosting{
		Id:                    "posting-123",
		FinancialBookingLogId: "log-456",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100.00000005",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId:     "acc-789",
		ValueDate:     now,
		PostingResult: "SUCCESS",
		CreatedAt:     now,
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	if posting.Id != "posting-123" {
		t.Errorf("Expected ID posting-123, got %s", posting.Id)
	}
	if posting.PostingDirection != commonv1.PostingDirection_POSTING_DIRECTION_DEBIT {
		t.Errorf("Expected DEBIT direction, got %v", posting.PostingDirection)
	}
	if posting.PostingAmount.InstrumentCode != "GBP" {
		t.Errorf("Expected GBP currency, got %s", posting.PostingAmount.InstrumentCode)
	}
	if posting.PostingAmount.Amount != "100.00000005" {
		t.Errorf("Expected 100.00000005 amount, got %s", posting.PostingAmount.Amount)
	}
}

// TestInitiateFinancialBookingLogRequest tests request message creation
func TestInitiateFinancialBookingLogRequest(t *testing.T) {
	req := &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "prod-123",
		BusinessUnitReference:   "bu-456",
		ChartOfAccountsRules:    "rules-001",
		BaseInstrumentCode:      "GBP",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "idem-key-123",
			TtlSeconds: 3600,
		},
	}

	if req.FinancialAccountType != "DEBIT" {
		t.Errorf("Expected DEBIT account type, got %v", req.FinancialAccountType)
	}
	if req.IdempotencyKey == nil {
		t.Error("Expected idempotency key to be set")
	}
	if req.IdempotencyKey.Key != "idem-key-123" {
		t.Errorf("Expected idempotency key idem-key-123, got %s", req.IdempotencyKey.Key)
	}
}

// TestUpdateFinancialBookingLogRequest tests update request message
func TestUpdateFinancialBookingLogRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *financialaccountingv1.UpdateFinancialBookingLogRequest
	}{
		{
			name: "update with new status and rules",
			req: &financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:                   "log-123",
				Status:               commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				ChartOfAccountsRules: "new-rules",
			},
		},
		{
			name: "update with status only",
			req: &financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:                   "log-456",
				Status:               commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
				ChartOfAccountsRules: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.req.Id == "" {
				t.Error("Expected ID to be set")
			}
			if tt.req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
				t.Error("Expected status to be specified")
			}
		})
	}
}

// TestCaptureLedgerPostingRequest tests posting request message
func TestCaptureLedgerPostingRequest(t *testing.T) {
	now := timestamppb.New(time.Now())

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "log-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "250",
			InstrumentCode: "USD",
			Version:        1,
		},
		AccountId: "acc-456",
		ValueDate: now,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "idem-key-789",
			TtlSeconds: 7200,
		},
	}

	if req.PostingDirection != commonv1.PostingDirection_POSTING_DIRECTION_CREDIT {
		t.Errorf("Expected CREDIT direction, got %v", req.PostingDirection)
	}
	if req.PostingAmount == nil {
		t.Error("Expected posting amount to be set")
	}
	if req.IdempotencyKey == nil {
		t.Error("Expected idempotency key to be set")
	}
}

// TestListFinancialBookingLogsRequest tests list request with filters
func TestListFinancialBookingLogsRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *financialaccountingv1.ListFinancialBookingLogsRequest
	}{
		{
			name: "list with pagination and status filter",
			req: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{
					PageSize:  50,
					PageToken: "",
				},
				Status:                commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				BusinessUnitReference: "bu-123",
			},
		},
		{
			name: "list without pagination",
			req: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Status:                commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				BusinessUnitReference: "bu-456",
			},
		},
		{
			name: "list with only business unit filter",
			req: &financialaccountingv1.ListFinancialBookingLogsRequest{
				BusinessUnitReference: "bu-789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			// Verify request structure is valid by successful creation
			_ = tt.req
		})
	}
}

// TestRetrieveRequests tests retrieve request messages
func TestRetrieveRequests(t *testing.T) {
	t.Run("retrieve booking log", func(t *testing.T) {
		req := &financialaccountingv1.RetrieveFinancialBookingLogRequest{
			Id: "log-123",
		}
		if req.Id != "log-123" {
			t.Errorf("Expected ID log-123, got %s", req.Id)
		}
	})

	t.Run("retrieve posting", func(t *testing.T) {
		req := &financialaccountingv1.RetrieveLedgerPostingRequest{
			Id: "posting-456",
		}
		if req.Id != "posting-456" {
			t.Errorf("Expected ID posting-456, got %s", req.Id)
		}
	})
}

// TestResponseMessages tests response message structures
func TestResponseMessages(t *testing.T) {
	now := timestamppb.New(time.Now())

	t.Run("initiate response", func(t *testing.T) {
		resp := &financialaccountingv1.InitiateFinancialBookingLogResponse{
			FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
				Id:                      "log-123",
				FinancialAccountType:    "DEBIT",
				ProductServiceReference: "prod-456",
				BusinessUnitReference:   "bu-789",
				ChartOfAccountsRules:    "rules-001",
				BaseInstrumentCode:      "GBP",
				Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				CreatedAt:               now,
				UpdatedAt:               now,
			},
		}
		if resp.FinancialBookingLog == nil {
			t.Error("Expected booking log to be set")
		}
	})

	t.Run("update response", func(t *testing.T) {
		resp := &financialaccountingv1.UpdateFinancialBookingLogResponse{
			FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
				Id:                      "log-123",
				FinancialAccountType:    "DEBIT",
				ProductServiceReference: "prod-456",
				BusinessUnitReference:   "bu-789",
				ChartOfAccountsRules:    "updated-rules",
				BaseInstrumentCode:      "GBP",
				Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				CreatedAt:               now,
				UpdatedAt:               now,
			},
		}
		if resp.FinancialBookingLog.ChartOfAccountsRules != "updated-rules" {
			t.Errorf("Expected updated-rules, got %s", resp.FinancialBookingLog.ChartOfAccountsRules)
		}
	})

	t.Run("list response", func(t *testing.T) {
		resp := &financialaccountingv1.ListFinancialBookingLogsResponse{
			FinancialBookingLogs: []*financialaccountingv1.FinancialBookingLog{
				{
					Id:                      "log-1",
					FinancialAccountType:    "DEBIT",
					ProductServiceReference: "prod-1",
					BusinessUnitReference:   "bu-1",
					ChartOfAccountsRules:    "rules-1",
					BaseInstrumentCode:      "GBP",
					Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
					CreatedAt:               now,
					UpdatedAt:               now,
				},
			},
			Pagination: &commonv1.PaginationResponse{
				NextPageToken: "next-token",
				TotalCount:    100,
			},
		}
		if len(resp.FinancialBookingLogs) != 1 {
			t.Errorf("Expected 1 booking log, got %d", len(resp.FinancialBookingLogs))
		}
		if resp.Pagination.TotalCount != 100 {
			t.Errorf("Expected total count 100, got %d", resp.Pagination.TotalCount)
		}
	})
}

// TestPostingDirections tests posting direction enum values
func TestPostingDirections(t *testing.T) {
	tests := []struct {
		name      string
		direction commonv1.PostingDirection
		expected  string
	}{
		{
			name:      "debit direction",
			direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			expected:  "POSTING_DIRECTION_DEBIT",
		},
		{
			name:      "credit direction",
			direction: commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
			expected:  "POSTING_DIRECTION_CREDIT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.direction.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.direction.String())
			}
		})
	}
}

// TestValidation_PositivePostingAmount tests positive amount validation for LedgerPosting
func TestValidation_PositivePostingAmount(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name      string
		posting   *financialaccountingv1.LedgerPosting
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid positive amount with units",
			posting: &financialaccountingv1.LedgerPosting{
				Id:                    "posting-1",
				FinancialBookingLogId: "log-1",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "acc-123",
				ValueDate: now,
				CreatedAt: now,
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
			wantError: false,
		},
		{
			name: "valid positive amount with nanos only",
			posting: &financialaccountingv1.LedgerPosting{
				Id:                    "posting-2",
				FinancialBookingLogId: "log-2",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "0.05",
					InstrumentCode: "USD",
					Version:        1,
				},
				AccountId: "acc-456",
				ValueDate: now,
				CreatedAt: now,
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
			wantError: false,
		},
		{
			name: "invalid zero amount",
			posting: &financialaccountingv1.LedgerPosting{
				Id:                    "posting-3",
				FinancialBookingLogId: "log-3",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "acc-789",
				ValueDate: now,
				CreatedAt: now,
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
			wantError: true,
			errorMsg:  "posting amount must be greater than zero",
		},
		{
			name: "invalid negative amount",
			posting: &financialaccountingv1.LedgerPosting{
				Id:                    "posting-4",
				FinancialBookingLogId: "log-4",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "-50",
					InstrumentCode: "EUR",
					Version:        1,
				},
				AccountId: "acc-999",
				ValueDate: now,
				CreatedAt: now,
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
			wantError: true,
			errorMsg:  "posting amount must be greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(tt.posting)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error but got none")
				} else if tt.errorMsg != "" && !containsError(err, tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

// TestValidation_AccountIDPattern tests account ID pattern validation
func TestValidation_AccountIDPattern(t *testing.T) {
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
			accountID: "acc123",
			wantError: false,
		},
		{
			name:      "valid with hyphens",
			accountID: "acc-123-xyz",
			wantError: false,
		},
		{
			name:      "valid with underscores",
			accountID: "acc_123_xyz",
			wantError: false,
		},
		{
			name:      "valid mixed",
			accountID: "acc-123_xyz-789",
			wantError: false,
		},
		{
			name:      "invalid with spaces",
			accountID: "acc 123",
			wantError: true,
		},
		{
			name:      "invalid with special chars",
			accountID: "acc@123",
			wantError: true,
		},
		{
			name:      "invalid with slashes",
			accountID: "acc/123",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posting := &financialaccountingv1.LedgerPosting{
				Id:                    "posting-1",
				FinancialBookingLogId: "log-1",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: tt.accountID,
				ValueDate: now,
				CreatedAt: now,
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			}

			err := validator.Validate(posting)
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

// TestValidation_CaptureLedgerPostingRequest tests request validation
func TestValidation_CaptureLedgerPostingRequest(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	now := timestamppb.New(time.Now())

	tests := []struct {
		name      string
		req       *financialaccountingv1.CaptureLedgerPostingRequest
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid request",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: "log-123",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100.00000005",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "acc-valid-123",
				ValueDate: now,
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key:        "idem-key-123",
					TtlSeconds: 3600,
				},
			},
			wantError: false,
		},
		{
			name: "invalid zero posting amount",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: "log-123",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "acc-123",
				ValueDate: now,
			},
			wantError: true,
			errorMsg:  "posting amount must be greater than zero",
		},
		{
			name: "invalid account ID with special chars",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: "log-123",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "acc@invalid#123",
				ValueDate: now,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(tt.req)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error but got none")
				} else if tt.errorMsg != "" && !containsError(err, tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

// containsError checks if error message contains the expected text
func containsError(err error, expectedMsg string) bool {
	if err == nil {
		return false
	}
	if expectedMsg == "" {
		return true
	}
	return strings.Contains(err.Error(), expectedMsg)
}
