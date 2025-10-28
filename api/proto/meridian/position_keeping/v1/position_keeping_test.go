package positionkeepingv1_test

import (
	"testing"
	"time"

	commonv1 "github.com/bjcoombs/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/bjcoombs/meridian/api/proto/meridian/position_keeping/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/genproto/googleapis/type/money"
)

// TestTransactionLogEntry_BasicConstruction tests basic message construction
func TestTransactionLogEntry_BasicConstruction(t *testing.T) {
	entry := &positionkeepingv1.TransactionLogEntry{
		EntryId:       "550e8400-e29b-41d4-a716-446655440000",
		TransactionId: "550e8400-e29b-41d4-a716-446655440001",
		AccountId:     "ACC-12345",
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		Direction:   commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		Timestamp:   timestamppb.New(time.Now()),
		Description: "Test transaction",
		Reference:   "REF-123",
	}

	if entry.GetEntryId() == "" {
		t.Error("EntryId should not be empty")
	}
	if entry.GetTransactionId() == "" {
		t.Error("TransactionId should not be empty")
	}
	if entry.GetAccountId() == "" {
		t.Error("AccountId should not be empty")
	}
	if entry.GetAmount() == nil {
		t.Error("Amount should not be nil")
	}
	if entry.GetDirection() != commonv1.PostingDirection_POSTING_DIRECTION_DEBIT {
		t.Error("Direction should be DEBIT")
	}
}

// TestTransactionLineage_BasicConstruction tests lineage message construction
func TestTransactionLineage_BasicConstruction(t *testing.T) {
	lineage := &positionkeepingv1.TransactionLineage{
		TransactionId:       "550e8400-e29b-41d4-a716-446655440000",
		ParentTransactionId: "550e8400-e29b-41d4-a716-446655440001",
		ChildTransactionIds: []string{
			"550e8400-e29b-41d4-a716-446655440002",
			"550e8400-e29b-41d4-a716-446655440003",
		},
		RelatedTransactionIds: []string{
			"550e8400-e29b-41d4-a716-446655440004",
		},
		TransactionType: "payment",
		CreatedAt:       timestamppb.New(time.Now()),
	}

	if lineage.GetTransactionId() == "" {
		t.Error("TransactionId should not be empty")
	}
	if len(lineage.GetChildTransactionIds()) != 2 {
		t.Errorf("Expected 2 child transactions, got %d", len(lineage.GetChildTransactionIds()))
	}
	if lineage.GetTransactionType() != "payment" {
		t.Error("TransactionType should be 'payment'")
	}
}

// TestAuditTrailEntry_BasicConstruction tests audit trail message construction
func TestAuditTrailEntry_BasicConstruction(t *testing.T) {
	audit := &positionkeepingv1.AuditTrailEntry{
		AuditId:   "550e8400-e29b-41d4-a716-446655440000",
		Timestamp: timestamppb.New(time.Now()),
		UserId:    "user-123",
		Action:    "created",
		Details:   "Financial position log created",
		IpAddress: "192.168.1.1",
		SystemContext: map[string]string{
			"service": "position-keeping",
			"version": "1.0.0",
		},
	}

	if audit.GetAuditId() == "" {
		t.Error("AuditId should not be empty")
	}
	if audit.GetUserId() == "" {
		t.Error("UserId should not be empty")
	}
	if audit.GetAction() == "" {
		t.Error("Action should not be empty")
	}
	if len(audit.GetSystemContext()) != 2 {
		t.Errorf("Expected 2 system context entries, got %d", len(audit.GetSystemContext()))
	}
}

// TestStatusTracking_BasicConstruction tests status tracking message construction
func TestStatusTracking_BasicConstruction(t *testing.T) {
	status := &positionkeepingv1.StatusTracking{
		CurrentStatus:     commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		PreviousStatus:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		StatusUpdatedAt:   timestamppb.New(time.Now()),
		StatusReason:      "Transaction posted successfully",
		FailureReason:     "",
	}

	if status.GetCurrentStatus() != commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED {
		t.Error("CurrentStatus should be POSTED")
	}
	if status.GetPreviousStatus() != commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING {
		t.Error("PreviousStatus should be PENDING")
	}
	if status.GetStatusReason() == "" {
		t.Error("StatusReason should not be empty")
	}
}

// TestFinancialPositionLog_BasicConstruction tests the main log message construction
func TestFinancialPositionLog_BasicConstruction(t *testing.T) {
	now := timestamppb.New(time.Now())

	log := &positionkeepingv1.FinancialPositionLog{
		LogId:     "550e8400-e29b-41d4-a716-446655440000",
		AccountId: "ACC-12345",
		TransactionLogEntries: []*positionkeepingv1.TransactionLogEntry{
			{
				EntryId:       "550e8400-e29b-41d4-a716-446655440001",
				TransactionId: "550e8400-e29b-41d4-a716-446655440002",
				AccountId:     "ACC-12345",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        100,
					},
				},
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				Timestamp: now,
			},
		},
		TransactionLineage: &positionkeepingv1.TransactionLineage{
			TransactionId:   "550e8400-e29b-41d4-a716-446655440002",
			TransactionType: "payment",
			CreatedAt:       now,
		},
		AuditTrail: []*positionkeepingv1.AuditTrailEntry{
			{
				AuditId:   "550e8400-e29b-41d4-a716-446655440003",
				Timestamp: now,
				UserId:    "user-123",
				Action:    "created",
			},
		},
		StatusTracking: &positionkeepingv1.StatusTracking{
			CurrentStatus:   commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			StatusUpdatedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}

	if log.GetLogId() == "" {
		t.Error("LogId should not be empty")
	}
	if log.GetAccountId() == "" {
		t.Error("AccountId should not be empty")
	}
	if len(log.GetTransactionLogEntries()) != 1 {
		t.Errorf("Expected 1 transaction entry, got %d", len(log.GetTransactionLogEntries()))
	}
	if log.GetTransactionLineage() == nil {
		t.Error("TransactionLineage should not be nil")
	}
	if len(log.GetAuditTrail()) != 1 {
		t.Errorf("Expected 1 audit entry, got %d", len(log.GetAuditTrail()))
	}
	if log.GetStatusTracking() == nil {
		t.Error("StatusTracking should not be nil")
	}
	if log.GetVersion() != 1 {
		t.Errorf("Expected version 1, got %d", log.GetVersion())
	}
}

// TestInitiateFinancialPositionLogRequest_BasicConstruction tests request message construction
func TestInitiateFinancialPositionLogRequest_BasicConstruction(t *testing.T) {
	req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "ACC-12345",
		InitialEntry: &positionkeepingv1.TransactionLogEntry{
			EntryId:       "550e8400-e29b-41d4-a716-446655440000",
			TransactionId: "550e8400-e29b-41d4-a716-446655440001",
			AccountId:     "ACC-12345",
			Amount: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
				},
			},
			Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			Timestamp: timestamppb.New(time.Now()),
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "idempotency-key-123",
			TtlSeconds: 300,
		},
	}

	if req.GetAccountId() == "" {
		t.Error("AccountId should not be empty")
	}
	if req.GetInitialEntry() == nil {
		t.Error("InitialEntry should not be nil")
	}
	if req.GetIdempotencyKey() == nil {
		t.Error("IdempotencyKey should not be nil")
	}
}

// TestBulkImportTransactionsRequest_BasicConstruction tests bulk import request
func TestBulkImportTransactionsRequest_BasicConstruction(t *testing.T) {
	now := timestamppb.New(time.Now())

	req := &positionkeepingv1.BulkImportTransactionsRequest{
		LogId: "550e8400-e29b-41d4-a716-446655440000",
		Entries: []*positionkeepingv1.TransactionLogEntry{
			{
				EntryId:       "550e8400-e29b-41d4-a716-446655440001",
				TransactionId: "550e8400-e29b-41d4-a716-446655440002",
				AccountId:     "ACC-12345",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        50,
					},
				},
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				Timestamp: now,
			},
			{
				EntryId:       "550e8400-e29b-41d4-a716-446655440003",
				TransactionId: "550e8400-e29b-41d4-a716-446655440004",
				AccountId:     "ACC-12345",
				Amount: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        75,
					},
				},
				Direction: commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				Timestamp: now,
			},
		},
		AuditEntry: &positionkeepingv1.AuditTrailEntry{
			AuditId:   "550e8400-e29b-41d4-a716-446655440005",
			Timestamp: now,
			UserId:    "user-123",
			Action:    "bulk_import",
		},
		Version: 1,
	}

	if req.GetLogId() == "" {
		t.Error("LogId should not be empty")
	}
	if len(req.GetEntries()) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(req.GetEntries()))
	}
	if req.GetAuditEntry() == nil {
		t.Error("AuditEntry should not be nil")
	}
}
