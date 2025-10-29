package financialaccountingv1_test

import (
	"testing"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestFinancialBookingLogCreation tests creation of FinancialBookingLog messages
func TestFinancialBookingLogCreation(t *testing.T) {
	now := timestamppb.New(time.Now())

	log := &financialaccountingv1.FinancialBookingLog{
		Id:                      "log-123",
		FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
		ProductServiceReference: "prod-456",
		BusinessUnitReference:   "bu-789",
		ChartOfAccountsRules:    "rules-001",
		BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
		Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CreatedAt:               now,
		UpdatedAt:               now,
		Postings:                []*financialaccountingv1.LedgerPosting{},
	}

	if log.Id != "log-123" {
		t.Errorf("Expected ID log-123, got %s", log.Id)
	}
	if log.FinancialAccountType != commonv1.AccountType_ACCOUNT_TYPE_DEBIT {
		t.Errorf("Expected DEBIT account type, got %v", log.FinancialAccountType)
	}
	if log.BaseCurrency != commonv1.Currency_CURRENCY_GBP {
		t.Errorf("Expected GBP currency, got %v", log.BaseCurrency)
	}
}

// TestLedgerPostingCreation tests creation of LedgerPosting messages
func TestLedgerPostingCreation(t *testing.T) {
	now := timestamppb.New(time.Now())

	posting := &financialaccountingv1.LedgerPosting{
		Id:                    "posting-123",
		FinancialBookingLogId: "log-456",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        50,
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
	if posting.PostingAmount.CurrencyCode != "GBP" {
		t.Errorf("Expected GBP currency, got %s", posting.PostingAmount.CurrencyCode)
	}
	if posting.PostingAmount.Units != 100 {
		t.Errorf("Expected 100 units, got %d", posting.PostingAmount.Units)
	}
}

// TestInitiateFinancialBookingLogRequest tests request message creation
func TestInitiateFinancialBookingLogRequest(t *testing.T) {
	req := &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
		ProductServiceReference: "prod-123",
		BusinessUnitReference:   "bu-456",
		ChartOfAccountsRules:    "rules-001",
		BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "idem-key-123",
			TtlSeconds: 3600,
		},
	}

	if req.FinancialAccountType != commonv1.AccountType_ACCOUNT_TYPE_DEBIT {
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
		PostingAmount: &money.Money{
			CurrencyCode: "USD",
			Units:        250,
			Nanos:        0,
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
				FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
				ProductServiceReference: "prod-456",
				BusinessUnitReference:   "bu-789",
				ChartOfAccountsRules:    "rules-001",
				BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
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
				FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
				ProductServiceReference: "prod-456",
				BusinessUnitReference:   "bu-789",
				ChartOfAccountsRules:    "updated-rules",
				BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
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
					FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
					ProductServiceReference: "prod-1",
					BusinessUnitReference:   "bu-1",
					ChartOfAccountsRules:    "rules-1",
					BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
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
